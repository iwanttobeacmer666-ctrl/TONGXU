package groupassistant

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/chenhg5/cc-connect/core"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Store) register(id Identity) error {
	for _, u := range s.members[id.Group] {
		if u == id.User {
			return nil
		}
	}
	old := s.members[id.Group]
	s.members[id.Group] = append(append([]string(nil), old...), id.User)
	data, e := json.Marshal(s.members)
	if e != nil || len(data) > 64<<10 {
		s.members[id.Group] = old
		return ErrInvalid
	}
	tmp, e := token()
	if e != nil {
		return e
	}
	tmp = ".ccga-members-" + tmp
	defer s.root.Remove(tmp)
	if e = s.root.WriteFile(tmp, data, 0600); e == nil {
		e = s.root.Rename(tmp, ".ccga-members.json")
	}
	if e != nil {
		s.members[id.Group] = old
	}
	return e
}

type SnapshotEntry struct {
	Ref  FileRef
	Hash [32]byte
}
type Snapshot struct {
	Identity       Identity
	Dir            string
	Entries        map[string]SnapshotEntry
	Members        map[string]string
	Images         []core.ImageAttachment
	Approve        func(context.Context, core.Event) (bool, error)
	DynamicTools   []core.DynamicTool
	DynamicHandler core.DynamicToolHandler
	ReadOnlyRoots  []string
	ResumeID       string
	Settings       GroupSettings
	Compact        bool
	Report         func(Conversation) error
	Progress       func(core.Event)
}

// Snapshot gives native Codex a private working copy. A full shell can freely
// edit this copy; persistent originals stay outside its permission profile.
func (s *Store) Snapshot(id Identity, parent string) (*Snapshot, error) {
	return s.snapshot(id, parent, nil, nil)
}

func (s *Store) snapshot(id Identity, parent string, selected []string, request *string) (*Snapshot, error) {
	if !validIdentity(id) {
		return nil, ErrDenied
	}
	if e := os.MkdirAll(parent, 0700); e != nil {
		return nil, e
	}
	dir, e := os.MkdirTemp(parent, "turn-")
	if e != nil {
		return nil, e
	}
	snap := &Snapshot{Identity: id, Dir: dir, Entries: map[string]SnapshotEntry{}, Members: map[string]string{}}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	s.mu.Lock()
	if e = s.register(id); e != nil {
		s.mu.Unlock()
		return nil, e
	}
	owners := append([]string(nil), s.members[id.Group]...)
	s.mu.Unlock()
	if id.User != s.admin {
		owners = []string{id.User}
	}
	owners = append(owners, "public")
	if selected != nil {
		owners = selected
	}
	total := 0
	for _, owner := range owners {
		if owner != id.User && owner != "public" {
			snap.Members[digestID(owner)] = owner
		}
		names, e := s.List(id, owner)
		if e != nil {
			return nil, e
		}
		// A named file narrows this owner's input; otherwise a selected owner
		// supplies their bounded workspace. No contents are read for selection.
		if request != nil {
			var named []string
			for _, name := range names {
				if strings.Contains(*request, name) {
					named = append(named, name)
				}
			}
			if len(named) > 0 {
				names = named
			} else if owner == id.User || owner == "public" {
				if !strings.Contains(*request, "所有文件") && !strings.Contains(*request, "全部文件") && !strings.Contains(*request, "我的文件") && !strings.Contains(*request, "公共资料") {
					names = nil
				}
			}
		}
		for _, name := range names {
			data, e := s.Read(id, FileRef{Owner: owner, Name: name})
			if e != nil {
				return nil, e
			}
			total += len(data)
			if total > MaxFileBytes || len(snap.Entries) >= 1000 {
				return nil, ErrInvalid
			}
			logical := "mine/" + name
			if owner == "public" {
				logical = "public/" + name
			} else if owner != id.User {
				logical = "members/" + digestID(owner) + "/" + name
			}
			target := filepath.Join(dir, filepath.FromSlash(logical))
			if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				return nil, e
			}
			if e = os.WriteFile(target, data, 0600); e != nil {
				return nil, e
			}
			snap.Entries[logical] = SnapshotEntry{FileRef{Owner: owner, Name: name}, sha256.Sum256(data)}
		}
	}
	for hash := range snap.Members {
		if e = os.MkdirAll(filepath.Join(dir, "members", hash), 0700); e != nil {
			return nil, e
		}
	}
	for _, sub := range []string{"mine", "public", ".ccga-tmp"} {
		if e = os.MkdirAll(filepath.Join(dir, sub), 0700); e != nil {
			return nil, e
		}
	}
	ok = true
	return snap, nil
}
func (snap *Snapshot) Close() error { return os.RemoveAll(snap.Dir) }

type Change struct {
	Ref      FileRef
	Data     []byte
	Delete   bool
	Expected *[32]byte
}

func (s *Store) Changes(snap *Snapshot) ([]Change, error) {
	s.mu.Lock()
	admin := snap.Identity.User == s.admin
	s.mu.Unlock()
	r, e := os.OpenRoot(snap.Dir)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	seen := map[string]bool{}
	var changes []Change
	total := 0
	e = fs.WalkDir(r.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		// Internal transport/runtime files are not user document changes.
		if strings.HasPrefix(d.Name(), ".ccga-") || name == ".cc-connect" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return ErrInvalid
		}
		if d.IsDir() {
			return nil
		}
		data, e := readLimited(r, name)
		if e != nil {
			return e
		}
		total += len(data)
		if total > MaxFileBytes || len(seen) >= 1000 {
			return ErrInvalid
		}
		seen[name] = true
		before, existed := snap.Entries[name]
		if existed {
			if sha256.Sum256(data) == before.Hash {
				return nil
			}
			if before.Ref.Owner == "public" && !admin {
				return ErrDenied
			}
			hash := before.Hash
			changes = append(changes, Change{Ref: before.Ref, Data: data, Expected: &hash})
			return nil
		}
		ref := FileRef{Name: name}
		if strings.HasPrefix(name, "mine/") {
			ref.Name = strings.TrimPrefix(name, "mine/")
		}
		if strings.HasPrefix(name, "public/") {
			if !admin {
				return ErrDenied
			}
			ref = FileRef{Owner: "public", Name: strings.TrimPrefix(name, "public/")}
		}
		if strings.HasPrefix(name, "members/") {
			parts := strings.SplitN(strings.TrimPrefix(name, "members/"), "/", 2)
			if !admin || len(parts) != 2 || snap.Members[parts[0]] == "" {
				return ErrDenied
			}
			ref = FileRef{Owner: snap.Members[parts[0]], Name: parts[1]}
		}
		if _, e := cleanName(ref.Name); e != nil {
			return e
		}
		changes = append(changes, Change{Ref: ref, Data: data})
		return nil
	})
	if e != nil {
		return nil, e
	}
	for name, entry := range snap.Entries {
		if seen[name] {
			continue
		}
		if entry.Ref.Owner == "public" && !admin {
			return nil, ErrDenied
		}
		hash := entry.Hash
		changes = append(changes, Change{Ref: entry.Ref, Delete: true, Expected: &hash})
	}
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Ref.Owner+"/"+changes[i].Ref.Name < changes[j].Ref.Owner+"/"+changes[j].Ref.Name
	})
	// Two logical paths must never publish to the same persistent file.
	dest := map[string]bool{}
	for _, c := range changes {
		owner := c.Ref.Owner
		if owner == "" || owner == "mine" {
			owner = snap.Identity.User
		}
		key := owner + "/" + path.Clean(c.Ref.Name)
		if dest[key] {
			return nil, ErrInvalid
		}
		dest[key] = true
	}
	return changes, nil
}
func (s *Store) StageChange(id Identity, c Change) (*Pending, error) {
	if len(c.Data) > MaxFileBytes {
		return nil, ErrInvalid
	}
	return s.stage(id, c.Ref, c.Data, c.Delete, c.Expected)
}
