package groupassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func previewFiles(t *testing.T, s *Service, user, kind string, names []string, limit int) FileOperationView {
	t.Helper()
	group := "group"
	if kind == FileClearTemporary {
		group, user = "", ""
	}
	p, err := s.PreviewFileOperation(context.Background(), s.store.admin, group, user, kind, "v1", names, limit)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func reviewFiles(t *testing.T, s *Service, p FileOperationView) FileOperationView {
	t.Helper()
	for !p.ReviewComplete {
		var err error
		p, err = s.FileOperationPage(s.store.admin, p.Group, p.User, p.Kind, p.Version, p.Token, p.NextOffset, 100)
		if err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func confirmFiles(s *Service, p FileOperationView) (FileOperationView, error) {
	return s.ConfirmFileOperation(context.Background(), s.store.admin, p.Group, p.User, p.Kind, p.Version, p.Token)
}

func TestSelectedDeletionIsScopedBoundAndSingleUse(t *testing.T) {
	s, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	alice, bob := Identity{"group", "wecom:alice"}, Identity{"group", "wecom:bob"}
	for _, name := range []string{"one.txt", "sub/two.txt", "keep.txt"} {
		putTestFile(t, store, alice, name, name)
	}
	putTestFile(t, store, bob, "one.txt", "bob")
	p := previewFiles(t, s, alice.User, FileDeleteSelected, []string{"one.txt", "sub/two.txt"}, 1)
	if p.Files != 2 || p.ReviewComplete || p.Consumed {
		t.Fatal(p)
	}
	if _, err := confirmFiles(s, p); !errors.Is(err, ErrDenied) {
		t.Fatal("unreviewed manifest accepted", err)
	}
	for _, scope := range []struct{ owner, group, user, kind, version string }{
		{"wecom:bob", p.Group, p.User, p.Kind, p.Version},
		{s.store.admin, "other", p.User, p.Kind, p.Version},
		{s.store.admin, p.Group, bob.User, p.Kind, p.Version},
		{s.store.admin, p.Group, p.User, FileClearDocuments, p.Version},
		{s.store.admin, p.Group, p.User, p.Kind, "v2"},
	} {
		if _, err := s.ConfirmFileOperation(context.Background(), scope.owner, scope.group, scope.user, scope.kind, scope.version, p.Token); !errors.Is(err, ErrDenied) {
			t.Fatal("token scope bypass", scope, err)
		}
	}
	p = reviewFiles(t, s, p)
	result, err := confirmFiles(s, p)
	if err != nil || !result.Complete || result.Deleted != 2 || !result.Consumed {
		t.Fatal(result, err)
	}
	if _, err := confirmFiles(s, p); !errors.Is(err, ErrDenied) {
		t.Fatal("token replay", err)
	}
	for _, id := range []Identity{alice, bob} {
		name := "keep.txt"
		if id == bob {
			name = "one.txt"
		}
		if _, err := store.Read(id, FileRef{Name: name}); err != nil {
			t.Fatal("unselected file lost", err)
		}
	}
	if !s.actorEnabled(alice) {
		t.Fatal("document deletion removed membership")
	}
}

func TestDocumentClearIncludesSubdirectoriesButKeepsOtherScopesAndSessions(t *testing.T) {
	s, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	alice := Identity{"group", "wecom:alice"}
	putTestFile(t, store, alice, "sub/nested/private.txt", "private")
	owner := Identity{"group", store.admin}
	if _, err := store.Put(owner, FileRef{Owner: "public", Name: "public.txt"}, []byte("public")); err != nil {
		t.Fatal(err)
	}
	if err := s.chat.SaveConversation(alice, Conversation{Identity: alice, ThreadID: "keep-thread"}); err != nil {
		t.Fatal(err)
	}
	s.cfg.SoftwareDir = t.TempDir()
	dependency := filepath.Join(s.cfg.SoftwareDir, "keep.txt")
	if err := os.WriteFile(dependency, []byte("dependency"), 0600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(store.root.Name(), filepath.FromSlash(memberBase(alice)))
	if err := os.MkdirAll(filepath.Join(base, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	p := reviewFiles(t, s, previewFiles(t, s, alice.User, FileClearDocuments, nil, 1))
	if p.Files != 1 || p.Directories != 3 || p.Bytes != 7 {
		t.Fatal(p)
	}
	if result, err := confirmFiles(s, p); err != nil || !result.Complete {
		t.Fatal(result, err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatal("scope root not preserved and emptied", entries, err)
	}
	if _, err := store.Read(owner, FileRef{Owner: "public", Name: "public.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dependency); err != nil {
		t.Fatal(err)
	}
	if len(s.chat.threadIDs(alice)) != 1 || !s.actorEnabled(alice) {
		t.Fatal("clear changed conversation/membership")
	}
}

func TestClearRejectsChangedManifestAndConsumesStaleConfirmation(t *testing.T) {
	for _, change := range []string{"content", "new-file", "new-directory"} {
		t.Run(change, func(t *testing.T) {
			s, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
			id := Identity{"group", "wecom:alice"}
			putTestFile(t, store, id, "a.txt", "before")
			p := previewFiles(t, s, id.User, FileClearDocuments, nil, 0)
			base := filepath.Join(store.root.Name(), filepath.FromSlash(memberBase(id)))
			switch change {
			case "content":
				if err := os.WriteFile(filepath.Join(base, "a.txt"), []byte("AFTER!"), 0600); err != nil {
					t.Fatal(err)
				}
			case "new-file":
				putTestFile(t, store, id, "new.txt", "new")
			case "new-directory":
				if err := os.Mkdir(filepath.Join(base, "new"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			result, err := confirmFiles(s, p)
			if !errors.Is(err, ErrChanged) || result.Complete || !result.Consumed || result.Deleted != 0 {
				t.Fatal(result, err)
			}
			if _, err := store.Read(id, FileRef{Name: "a.txt"}); err != nil {
				t.Fatal("preflight deleted a file", err)
			}
			if _, err := confirmFiles(s, p); !errors.Is(err, ErrDenied) {
				t.Fatal("stale token replayed", err)
			}
		})
	}
}

func TestFileOperationExpirySelectionValidationAndPagination(t *testing.T) {
	s, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	id := Identity{"group", "wecom:alice"}
	for _, name := range []string{"a", "b", "c"} {
		putTestFile(t, store, id, name, "x")
	}
	for _, names := range [][]string{nil, {"../outside"}, {"a", "a"}, {"a", "A"}, {"missing"}, {"a/../b"}, {"C:/outside"}} {
		if _, err := s.PreviewFileOperation(context.Background(), store.admin, id.Group, id.User, FileDeleteSelected, "v1", names, 0); err == nil {
			t.Fatal("invalid selection accepted", names)
		}
	}
	p := previewFiles(t, s, id.User, FileClearDocuments, nil, 1)
	if _, err := s.FileOperationPage(store.admin, p.Group, p.User, p.Kind, p.Version, p.Token, 2, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("skipped manifest page accepted", err)
	}
	s.accessMu.Lock()
	s.filePlans[p.Token].view.Expires = time.Now().Add(-time.Second)
	s.accessMu.Unlock()
	if _, err := confirmFiles(s, p); !errors.Is(err, ErrDenied) {
		t.Fatal("expired token accepted", err)
	}
	if _, err := store.Read(id, FileRef{Name: "a"}); err != nil {
		t.Fatal(err)
	}
}

func TestTemporaryClearRequiresGlobalIdleAndPreservesDurableData(t *testing.T) {
	s, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "ok", nil })
	putTestFile(t, store, Identity{"group", "wecom:alice"}, "keep.txt", "durable")
	dir := filepath.Join(s.cfg.WorkDir, "turn-unattributed", "mine")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "residual.txt"), []byte("temporary"), 0600); err != nil {
		t.Fatal(err)
	}
	p := previewFiles(t, s, "", FileClearTemporary, nil, 0)
	if p.Files != 1 || p.Directories != 2 {
		t.Fatal(p)
	}
	reservation, err := s.AdmitMedia(context.Background(), memberMessage("alice", "downloading", "hi"), 1)
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.TemporaryFiles(context.Background(), store.admin, 0, 50)
	if err != nil || !view.Busy {
		t.Fatal(view, err)
	}
	if _, err := confirmFiles(s, p); !errors.Is(err, ErrBusy) {
		t.Fatal("clear during media admission", err)
	}
	if _, err := s.PreviewFileOperation(context.Background(), store.admin, "", "", FileClearTemporary, "v1", nil, 0); !errors.Is(err, ErrBusy) {
		t.Fatal("preview during media admission", err)
	}
	reservation.Close()
	result, err := confirmFiles(s, p)
	if err != nil || !result.Complete || result.Deleted != 3 {
		t.Fatal(result, err)
	}
	entries, err := os.ReadDir(s.cfg.WorkDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary root lost/not empty", entries, err)
	}
	if _, err := store.Read(Identity{"group", "wecom:alice"}, FileRef{Name: "keep.txt"}); err != nil {
		t.Fatal("durable data deleted", err)
	}
}

func TestTemporaryClearRejectsQueuedRunningAndControlWork(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { close(started); <-release; return "ok", nil })
	done := make(chan Response, 1)
	go func() { done <- s.Handle(context.Background(), memberMessage("alice", "running", "hi")) }()
	<-started
	p, err := s.PreviewFileOperation(context.Background(), s.store.admin, "", "", FileClearTemporary, "v1", nil, 0)
	if !errors.Is(err, ErrBusy) {
		close(release)
		t.Fatal("running task clear allowed", p, err)
	}
	queued, _, err := s.addTask(context.Background(), Identity{"group", "wecom:bob"})
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	<-done
	if _, err := s.PreviewFileOperation(context.Background(), s.store.admin, "", "", FileClearTemporary, "v1", nil, 0); !errors.Is(err, ErrBusy) {
		t.Fatal("queued task clear allowed", err)
	}
	s.finishTask(queued)
	s.accessMu.RLock() // same lock held by native control queries
	_, err = s.PreviewFileOperation(context.Background(), s.store.admin, "", "", FileClearTemporary, "v1", nil, 0)
	s.accessMu.RUnlock()
	if !errors.Is(err, ErrBusy) {
		t.Fatal("control operation clear allowed", err)
	}
}

type stalledFileContext struct {
	context.Context
	once             sync.Once
	entered, release chan struct{}
}

func (c *stalledFileContext) Err() error {
	c.once.Do(func() { close(c.entered); <-c.release })
	return c.Context.Err()
}

func TestTemporaryClearFencesNewRealTaskAdmissionUntilDeletionFinishes(t *testing.T) {
	started := make(chan struct{})
	s, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { close(started); return "ok", nil })
	if err := os.WriteFile(filepath.Join(s.cfg.WorkDir, "residual"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	p := previewFiles(t, s, "", FileClearTemporary, nil, 0)
	ctx := &stalledFileContext{Context: context.Background(), entered: make(chan struct{}), release: make(chan struct{})}
	cleared := make(chan error, 1)
	go func() {
		_, err := s.ConfirmFileOperation(ctx, s.store.admin, "", "", p.Kind, p.Version, p.Token)
		cleared <- err
	}()
	<-ctx.entered
	if s.accessMu.TryRLock() {
		s.accessMu.RUnlock()
		close(ctx.release)
		t.Fatal("clear did not fence admission")
	}
	done := make(chan Response, 1)
	go func() { done <- s.Handle(context.Background(), memberMessage("alice", "after-clear", "hi")) }()
	select {
	case <-started:
		close(ctx.release)
		t.Fatal("worker entered during clear")
	default:
	}
	close(ctx.release)
	if err := <-cleared; err != nil {
		t.Fatal(err)
	}
	if result := <-done; result.Text != "ok" {
		t.Fatal("task failed after clear", result)
	}
}

func TestTemporaryClearRejectsProtectedRootAndLinks(t *testing.T) {
	s, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	for _, work := range []string{store.root.Name(), filepath.Dir(store.root.Name()), filepath.VolumeName(store.root.Name()) + string(filepath.Separator)} {
		s.cfg.WorkDir = work
		if _, err := s.PreviewFileOperation(context.Background(), store.admin, "", "", FileClearTemporary, "v1", nil, 0); !errors.Is(err, ErrDenied) {
			t.Fatal("unsafe temporary root allowed", work, err)
		}
	}
	s.cfg.WorkDir = t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.cfg.WorkDir, "link")); err != nil {
		t.Skip("symlink creation unavailable:", err)
	}
	if _, err := s.PreviewFileOperation(context.Background(), store.admin, "", "", FileClearTemporary, "v1", nil, 0); !errors.Is(err, ErrDenied) {
		t.Fatal("link followed in clear preview", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestAdministratorSingleDeleteBindsUserAndAcceptsLargeFiles(t *testing.T) {
	s, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	id := Identity{"group", "wecom:alice"}
	putTestFile(t, store, id, "large", "seed")
	file := filepath.Join(store.root.Name(), filepath.FromSlash(memberBase(id)), "large")
	f, err := os.OpenFile(file, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	p, err := s.AdminDocuments(store.admin, id.Group, id.User, "document-delete-preview", "large", "", "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdminDocuments(store.admin, id.Group, "public", "document-delete", "", p.Pending.Token, "", 0, 0); !errors.Is(err, ErrDenied) {
		t.Fatal("single-file token crossed scope", err)
	}
	if _, err := s.AdminDocuments(store.admin, id.Group, id.User, "document-delete", "", p.Pending.Token, "", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("large file not deleted", err)
	}
}

func TestFilePageBoundsEscapedPathsAndRejectsInvalidArguments(t *testing.T) {
	items := make([]FileOutcome, 100)
	for i := range items {
		items[i].Name = strings.Repeat("\x01", 1000)
	}
	page, next, err := filePage(items, 0, 100)
	if err != nil || next == 0 || next >= len(items) || len(page) != next {
		t.Fatal(len(page), next, err)
	}
	for _, args := range [][2]int{{-1, 1}, {101, 1}, {0, -1}, {0, 101}} {
		if _, _, err := filePage(items, args[0], args[1]); !errors.Is(err, ErrInvalid) {
			t.Fatal(args, err)
		}
	}
}

type unhealthyFileRunner struct{ runnerFunc }

func (unhealthyFileRunner) Ready() error { return ErrUnavailable }

func TestTemporaryClearRefusesUnhealthyRunnerAndRootReplacement(t *testing.T) {
	s, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	file := filepath.Join(s.cfg.WorkDir, "residual")
	if err := os.WriteFile(file, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	p := previewFiles(t, s, "", FileClearTemporary, nil, 0)
	old := s.runner
	s.runner = unhealthyFileRunner{}
	if _, err := confirmFiles(s, p); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unhealthy runner allowed cleanup", err)
	}
	s.runner = old
	oldRoot := s.cfg.WorkDir + "-old"
	if err := os.Rename(s.cfg.WorkDir, oldRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(oldRoot) })
	if err := os.MkdirAll(s.cfg.WorkDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := confirmFiles(s, p); !errors.Is(err, ErrChanged) || result.Complete {
		t.Fatal("replaced root accepted", result, err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
}

type lateFileContext struct {
	context.Context
	original, added string
	once            sync.Once
	err             error
}

func (c *lateFileContext) Err() error {
	if _, err := os.Stat(c.original); os.IsNotExist(err) {
		c.once.Do(func() { c.err = os.WriteFile(c.added, []byte("new data"), 0600) })
	}
	return errors.Join(c.Context.Err(), c.err)
}

func TestTemporaryClearDoesNotClaimCompleteWithLateUnpreviewedFile(t *testing.T) {
	s, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	original, added := filepath.Join(s.cfg.WorkDir, "original"), filepath.Join(s.cfg.WorkDir, "new")
	if err := os.WriteFile(original, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	p := previewFiles(t, s, "", FileClearTemporary, nil, 0)
	ctx := &lateFileContext{Context: context.Background(), original: original, added: added}
	result, err := s.ConfirmFileOperation(ctx, s.store.admin, "", "", p.Kind, p.Version, p.Token)
	if err == nil || result.Complete || result.Remaining != 1 || result.Deleted != 1 || len(result.Items) != 2 {
		t.Fatal(result, err)
	}
	if data, err := os.ReadFile(added); err != nil || string(data) != "new data" {
		t.Fatal("unpreviewed data deleted", err)
	}
}

func TestFileOperationCompletedResultRetentionDoesNotBlockFurtherClears(t *testing.T) {
	s, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	for i := 0; i < 12; i++ {
		p := previewFiles(t, s, "", FileClearTemporary, nil, 0)
		if result, err := confirmFiles(s, p); err != nil || !result.Complete {
			t.Fatal(i, result, err)
		}
	}
}
