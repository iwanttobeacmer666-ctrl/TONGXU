package groupassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// These kinds identify fixed server-owned scopes, never caller-supplied paths.
const (
	FileDeleteSelected = "selected"
	FileClearDocuments = "documents"
	FileClearTemporary = "temporary"
)

type ManagedFile struct {
	Name      string
	Bytes     int64
	SHA256    string `json:",omitempty"`
	Directory bool   `json:",omitempty"`
	Blocked   bool   `json:",omitempty"`
}

type FileOutcome struct {
	ManagedFile
	Status string `json:",omitempty"` // deleted, failed, or not_attempted
	Code   string `json:",omitempty"` // no raw host paths or OS error messages
}

type FileOperationView struct {
	Token, Kind, Group, User, Version string
	Expires                           time.Time
	Files, Directories, Bytes         int64
	Total, NextOffset                 int
	Items                             []FileOutcome
	ReviewComplete, Consumed          bool
	Complete, Busy                    bool
	Deleted, Failed                   int
	Remaining                         int
}

type fileOperationPlan struct {
	view     FileOperationView
	actor    string
	entries  []ManagedFile
	results  []FileOutcome
	rootInfo fs.FileInfo
	reviewed int
}

func filePage(items []FileOutcome, offset, limit int) ([]FileOutcome, int, error) {
	if offset < 0 || offset > len(items) || limit < 0 || limit > 100 {
		return nil, 0, ErrInvalid
	}
	if limit == 0 {
		limit = 50
	}
	end, budget := offset, 0
	for end < len(items) && end-offset < limit {
		encoded, err := json.Marshal(items[end])
		if err != nil {
			return nil, 0, err
		}
		if budget+len(encoded) > 100<<10 {
			break
		}
		budget += len(encoded)
		end++
	}
	if end == offset && offset < len(items) {
		return nil, 0, ErrInvalid
	}
	return append([]FileOutcome(nil), items[offset:end]...), end, nil
}

func (p *fileOperationPlan) page(offset, limit int) (FileOperationView, error) {
	if !p.view.Consumed && offset > p.reviewed {
		return FileOperationView{}, ErrInvalid
	}
	items, next, err := filePage(p.results, offset, limit)
	if err != nil {
		return FileOperationView{}, err
	}
	if offset <= p.reviewed {
		p.reviewed = max(p.reviewed, next)
	}
	out := p.view
	out.Items, out.Total, out.NextOffset = items, len(p.results), next
	out.ReviewComplete = p.reviewed >= len(p.entries)
	return out, nil
}

func (s *Service) pruneFilePlans() {
	for t, plan := range s.filePlans {
		if !time.Now().Before(plan.view.Expires) {
			delete(s.filePlans, t)
		}
	}
}

func (s *Service) temporaryIdle() error {
	// Both Handle and AdmitMedia register tasks under accessMu.RLock, before
	// releasing admission. Tasks stay registered through snapshot cleanup.
	// Control queries hold that read lock; native member deletion holds the
	// write lock. Callers must hold accessMu exclusively before checking this.
	s.tasksMu.Lock()
	busy := len(s.tasks) != 0
	s.tasksMu.Unlock()
	if busy || len(s.inspectGate) != 0 {
		return ErrBusy
	}
	if ready, ok := s.runner.(interface{ Ready() error }); ok && ready.Ready() != nil {
		return ErrUnavailable
	}
	return nil
}

func pathsOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = strings.ToLower(filepath.Clean(a)), strings.ToLower(filepath.Clean(b))
	sep := string(filepath.Separator)
	return a == b || strings.HasPrefix(a, b+sep) || strings.HasPrefix(b, a+sep)
}

func (s *Service) temporaryRoot() (*os.Root, error) {
	work := filepath.Clean(s.cfg.WorkDir)
	if !filepath.IsAbs(work) || filepath.Dir(work) == work {
		return nil, ErrDenied
	}
	for _, protected := range []string{s.store.root.Name(), s.cfg.StateDir, s.cfg.SoftwareDir, s.cfg.CodexHome} {
		if pathsOverlap(work, protected) {
			return nil, ErrDenied
		}
		if actual, err := filepath.EvalSymlinks(protected); err == nil && pathsOverlap(work, actual) {
			return nil, ErrDenied
		}
	}
	// Reject aliases/reparse roots rather than risk clearing a different area.
	if err := os.MkdirAll(work, 0700); err != nil {
		return nil, err
	}
	actual, err := filepath.EvalSymlinks(work)
	if err != nil || !strings.EqualFold(work, filepath.Clean(actual)) {
		return nil, ErrDenied
	}
	info, err := os.Lstat(work)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrDenied
	}
	return os.OpenRoot(work)
}

func (s *Service) operationRoot(group, user, kind string) (*os.Root, error) {
	if kind == FileClearTemporary {
		if group != "" || user != "" {
			return nil, ErrInvalid
		}
		return s.temporaryRoot()
	}
	if kind != FileDeleteSelected && kind != FileClearDocuments || !validIdentity(Identity{group, user}) {
		return nil, ErrInvalid
	}
	r, _, err := s.store.adminScope(Identity{group, s.store.admin}, FileRef{Owner: user, Name: "listing"}, true)
	return r, err
}

func operationInventory(ctx context.Context, r *os.Root, kind string, names []string) ([]ManagedFile, error) {
	if kind != FileDeleteSelected {
		return walkManaged(ctx, r, kind == FileClearDocuments, true)
	}
	if len(names) == 0 || len(names) > 100 {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	items := make([]ManagedFile, 0, len(names))
	for _, name := range names {
		if _, err := cleanName(name); err != nil || seen[strings.ToLower(name)] {
			return nil, ErrInvalid
		}
		seen[strings.ToLower(name)] = true
		item, _, err := inspectFile(ctx, r, name, 0, false)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func inventoryDigest(items []ManagedFile) string {
	h := sha256.New()
	encoder := json.NewEncoder(h)
	for _, item := range items {
		_ = encoder.Encode(item)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// PreviewFileOperation stages one immutable manifest; page it fully before
// confirmation. No task is stopped, no file is deleted, and no scope is inferred.
func (s *Service) PreviewFileOperation(ctx context.Context, owner, group, user, kind, version string, names []string, limit int) (FileOperationView, error) {
	if owner != s.store.admin || version == "" {
		return FileOperationView{}, ErrDenied
	}
	if limit < 0 || limit > 100 {
		return FileOperationView{}, ErrInvalid
	}
	if kind != FileDeleteSelected && len(names) != 0 {
		return FileOperationView{}, ErrInvalid
	}
	if kind == FileClearTemporary {
		if !s.accessMu.TryLock() {
			return FileOperationView{}, ErrBusy
		}
	} else {
		s.accessMu.Lock()
	}
	defer s.accessMu.Unlock()
	if kind == FileClearTemporary {
		if err := s.temporaryIdle(); err != nil {
			return FileOperationView{}, err
		}
	}
	s.pruneFilePlans()
	// Keep bounded recent results without blocking the ninth successful action.
	if len(s.filePlans) >= 8 {
		oldest := ""
		for t, plan := range s.filePlans {
			if plan.view.Consumed && (oldest == "" || plan.view.Expires.Before(s.filePlans[oldest].view.Expires)) {
				oldest = t
			}
		}
		if oldest != "" {
			delete(s.filePlans, oldest)
		}
	}
	if len(s.filePlans) >= 8 {
		return FileOperationView{}, ErrBusy
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	r, err := s.operationRoot(group, user, kind)
	if err != nil {
		return FileOperationView{}, err
	}
	defer r.Close()
	rootInfo, err := r.Stat(".")
	if err != nil {
		return FileOperationView{}, err
	}
	items, err := operationInventory(ctx, r, kind, names)
	if err != nil {
		return FileOperationView{}, err
	}
	t, err := token()
	if err != nil {
		return FileOperationView{}, err
	}
	p := &fileOperationPlan{actor: owner, rootInfo: rootInfo, entries: items, view: FileOperationView{Token: t, Kind: kind, Group: group, User: user, Version: version, Expires: time.Now().Add(5 * time.Minute)}}
	for _, item := range items {
		if item.Directory {
			p.view.Directories++
		} else {
			p.view.Files++
			p.view.Bytes += item.Bytes
		}
		p.results = append(p.results, FileOutcome{ManagedFile: item})
	}
	page, err := p.page(0, limit)
	if err != nil {
		return FileOperationView{}, err
	}
	if s.filePlans == nil {
		s.filePlans = map[string]*fileOperationPlan{}
	}
	s.filePlans[t] = p
	return page, nil
}

func (s *Service) filePlan(owner, group, user, kind, version, confirmation string) (*fileOperationPlan, error) {
	s.pruneFilePlans()
	p := s.filePlans[confirmation]
	if owner != s.store.admin || p == nil || p.actor != owner || p.view.Group != group || p.view.User != user || p.view.Kind != kind || p.view.Version != version {
		return nil, ErrDenied
	}
	return p, nil
}

func (s *Service) FileOperationPage(owner, group, user, kind, version, confirmation string, offset, limit int) (FileOperationView, error) {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	p, err := s.filePlan(owner, group, user, kind, version, confirmation)
	if err != nil {
		return FileOperationView{}, err
	}
	return p.page(offset, limit)
}

func (p *fileOperationPlan) rejected(err error) (FileOperationView, error) {
	for i := range p.results {
		p.results[i].Status, p.results[i].Code = "not_attempted", "preview_changed_or_unavailable"
	}
	p.view.Failed = len(p.results)
	view, _ := p.page(0, 50)
	return view, err
}

func (s *Service) ConfirmFileOperation(ctx context.Context, owner, group, user, kind, version, confirmation string) (FileOperationView, error) {
	if owner != s.store.admin {
		return FileOperationView{}, ErrDenied
	}
	if kind == FileClearTemporary {
		if !s.accessMu.TryLock() {
			return FileOperationView{}, ErrBusy
		}
	} else {
		s.accessMu.Lock()
	}
	defer s.accessMu.Unlock()
	p, err := s.filePlan(owner, group, user, kind, version, confirmation)
	if err != nil {
		return FileOperationView{}, err
	}
	if p.view.Consumed || p.reviewed != len(p.entries) {
		return FileOperationView{}, ErrDenied
	}
	if kind == FileClearTemporary {
		if err := s.temporaryIdle(); err != nil {
			return FileOperationView{}, err
		}
	}
	p.view.Consumed = true // failures/stale content require a fresh preview
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	r, err := s.operationRoot(group, user, kind)
	if err != nil {
		return p.rejected(err)
	}
	defer r.Close()
	info, err := r.Stat(".")
	if err != nil || !os.SameFile(info, p.rootInfo) {
		return p.rejected(ErrChanged)
	}
	var names []string
	for _, item := range p.entries {
		names = append(names, item.Name)
	}
	current, err := operationInventory(ctx, r, kind, names)
	if err != nil || inventoryDigest(current) != inventoryDigest(p.entries) {
		return p.rejected(errors.Join(ErrChanged, err))
	}
	p.deleteEntries(ctx, r)
	if kind != FileDeleteSelected {
		p.checkRemaining(ctx, r)
	}
	p.view.Complete = p.view.Failed == 0
	page, pageErr := p.page(0, 50)
	if !p.view.Complete {
		return page, errors.Join(ErrUnavailable, pageErr)
	}
	return page, pageErr
}

func (p *fileOperationPlan) deleteEntries(ctx context.Context, r *os.Root) {
	// Reverse lexical order removes descendants before their directories.
	// Every entry gets an outcome; failure never becomes an overall success.
	for i := len(p.entries) - 1; i >= 0; i-- {
		var err error
		item := p.entries[i]
		outcome := &p.results[i]
		if err := ctx.Err(); err != nil {
			outcome.Status, outcome.Code = "not_attempted", "cancelled"
			p.view.Failed++
			continue
		}
		if item.Directory {
			var info fs.FileInfo
			info, err = regularPath(r, item.Name)
			if err == nil && !info.IsDir() {
				err = ErrChanged
			}
		} else {
			var latest ManagedFile
			latest, _, err = inspectFile(ctx, r, item.Name, 0, false)
			if err == nil && latest != item {
				err = ErrChanged
			}
		}
		if err == nil {
			err = r.Remove(item.Name)
		}
		if err != nil {
			outcome.Status, outcome.Code = "failed", "changed_or_unavailable"
			p.view.Failed++
		} else {
			outcome.Status = "deleted"
			p.view.Deleted++
		}
	}
}

func (p *fileOperationPlan) checkRemaining(ctx context.Context, r *os.Root) {
	// Count every remaining entry, including new reserved files or links;
	// none may be silently omitted from a claim that a scope is empty.
	remaining, scanErr := walkManaged(ctx, r, false, false)
	p.view.Remaining = len(remaining)
	if scanErr != nil {
		p.view.Failed++
	}
	known := map[string]bool{}
	for _, item := range p.entries {
		known[item.Name] = true
	}
	for _, item := range remaining {
		if !known[item.Name] {
			p.results = append(p.results, FileOutcome{ManagedFile: item, Status: "not_attempted", Code: "new_entry"})
			p.view.Failed++
		}
	}
	if p.view.Remaining != 0 && p.view.Failed == 0 {
		p.view.Failed++
	}
}

// TemporaryFiles exposes only bounded metadata, never temporary file contents.
func (s *Service) TemporaryFiles(ctx context.Context, owner string, offset, limit int) (FileOperationView, error) {
	if owner != s.store.admin {
		return FileOperationView{}, ErrDenied
	}
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	s.tasksMu.Lock()
	busy := len(s.tasks) != 0
	s.tasksMu.Unlock()
	r, err := s.temporaryRoot()
	if err != nil {
		return FileOperationView{}, err
	}
	defer r.Close()
	items, err := walkManaged(ctx, r, false, false)
	if err != nil {
		return FileOperationView{}, err
	}
	view := FileOperationView{Kind: FileClearTemporary, Busy: busy || len(s.inspectGate) != 0, Total: len(items)}
	var rows []FileOutcome
	for _, item := range items {
		rows = append(rows, FileOutcome{ManagedFile: item})
		if item.Directory {
			view.Directories++
		} else {
			view.Files++
			view.Bytes += item.Bytes
		}
	}
	view.Items, view.NextOffset, err = filePage(rows, offset, limit)
	return view, err
}
