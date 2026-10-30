package groupassistant

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, e := NewStore(t.TempDir(), "owner")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestOwnershipAndPublicReadOnly(t *testing.T) {
	s := newTestStore(t)
	a := Identity{"g", "alice"}
	b := Identity{"g", "bob"}
	admin := Identity{"g", "owner"}
	if _, e := s.Put(a, FileRef{Name: "notes.txt"}, []byte("alice")); e != nil {
		t.Fatal(e)
	}
	for _, id := range []Identity{b, {"another-group", "bob"}} {
		if _, e := s.Read(id, FileRef{Owner: "alice", Name: "notes.txt"}); !errors.Is(e, ErrDenied) {
			t.Fatalf("cross-user read: %v", e)
		}
		if _, e := s.Put(id, FileRef{Owner: "alice", Name: "notes.txt"}, []byte("bad")); !errors.Is(e, ErrDenied) {
			t.Fatalf("cross-user write: %v", e)
		}
	}
	if got, e := s.Read(admin, FileRef{Owner: "alice", Name: "notes.txt"}); e != nil || string(got) != "alice" {
		t.Fatalf("%q %v", got, e)
	}
	if _, e := s.Put(admin, FileRef{Owner: "public", Name: "template.txt"}, []byte("public")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Read(b, FileRef{Owner: "public", Name: "template.txt"}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Put(b, FileRef{Owner: "public", Name: "template.txt"}, []byte("bad")); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
}
func TestConfirmBoundToActorGroupAndVersion(t *testing.T) {
	s := newTestStore(t)
	a := Identity{"g", "alice"}
	ref := FileRef{Name: "x.txt"}
	s.Put(a, ref, []byte("before"))
	p, e := s.Put(a, ref, []byte("after"))
	if e != nil || p == nil {
		t.Fatalf("%v %v", p, e)
	}
	if data, _ := s.Read(a, ref); string(data) != "before" {
		t.Fatal("write happened before confirmation")
	}
	for _, wrong := range []Identity{{"g", "bob"}, {"other", "alice"}, {"g", "owner"}} {
		if e = s.Confirm(wrong, p.Token); !errors.Is(e, ErrDenied) {
			t.Fatalf("wrong actor approved: %v", e)
		}
	}
	if e = s.Confirm(a, p.Token); e != nil {
		t.Fatal(e)
	}
	if e = s.Confirm(a, p.Token); !errors.Is(e, ErrDenied) {
		t.Fatal("replay accepted")
	}
	p, _ = s.Delete(a, ref)
	s.mu.Lock()
	r, n, _ := s.scoped(a, ref, true)
	r.WriteFile(n, []byte("changed elsewhere"), 0600)
	r.Close()
	s.mu.Unlock()
	if e = s.Confirm(a, p.Token); !errors.Is(e, ErrChanged) {
		t.Fatalf("stale approval: %v", e)
	}
	if _, e = s.Read(a, ref); e != nil {
		t.Fatal("stale approval deleted file")
	}
}
func TestConfirmationExpiryAndOwnerAlsoConfirms(t *testing.T) {
	s := newTestStore(t)
	id := Identity{"g", "owner"}
	ref := FileRef{Name: "a.txt"}
	s.Put(id, ref, []byte("x"))
	p, e := s.Delete(id, ref)
	if e != nil || p == nil {
		t.Fatal("owner skipped confirmation")
	}
	s.now = func() time.Time { return p.Expires.Add(time.Second) }
	if e = s.Confirm(id, p.Token); !errors.Is(e, ErrDenied) {
		t.Fatalf("expired approval: %v", e)
	}
	if _, e = s.Read(id, ref); e != nil {
		t.Fatal(e)
	}
}
func TestRejectTraversalAndWindowsSpecialPaths(t *testing.T) {
	s := newTestStore(t)
	id := Identity{"g", "alice"}
	for _, name := range []string{"../bob/x", "a/../x", "/etc/passwd", "C:\\secret", "a:b", "a\\b", ".ccga-internal", "a//b", "a/", "a.", "a/\x00b"} {
		if _, e := s.Put(id, FileRef{Name: name}, []byte("x")); e == nil {
			t.Errorf("accepted %q", name)
		}
	}
	for _, id := range []Identity{{"", "alice"}, {"g", ""}, {"g\nx", "alice"}} {
		if _, e := s.List(id, "mine"); !errors.Is(e, ErrDenied) {
			t.Fatalf("missing identity: %v", e)
		}
	}
}
func TestSymlinkCannotEscapePersonalRoot(t *testing.T) {
	s := newTestStore(t)
	id := Identity{"g", "alice"}
	s.Put(id, FileRef{Name: "normal.txt"}, []byte("ok"))
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("secret"), 0600)
	dir := filepath.Join(s.root.Name(), "groups", digestID("g"), "users", digestID("alice"))
	if e := os.Symlink(outside, filepath.Join(dir, "link.txt")); e != nil {
		t.Skipf("symlink creation unavailable: %v", e)
	}
	if _, e := s.Read(id, FileRef{Name: "link.txt"}); e == nil {
		t.Fatal("symlink escaped root")
	}
}
func TestDeleteRequiresFreshExplicitConfirmation(t *testing.T) {
	s := newTestStore(t)
	id := Identity{"g", "alice"}
	ref := FileRef{Name: "x.txt"}
	s.Put(id, ref, []byte("x"))
	p, _ := s.Delete(id, ref)
	if _, e := s.Read(id, ref); e != nil {
		t.Fatal(e)
	}
	if e := s.Cancel(id, p.Token); e != nil {
		t.Fatal(e)
	}
	if e := s.Confirm(id, p.Token); !errors.Is(e, ErrDenied) {
		t.Fatal("cancelled request executed")
	}
	p, _ = s.Delete(id, ref)
	if e := s.Confirm(id, p.Token); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Read(id, ref); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}
