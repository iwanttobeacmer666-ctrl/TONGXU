package groupassistant

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeWorkspaceCopiesWithoutChangingOriginals(t *testing.T) {
	s := newTestStore(t)
	a := Identity{"g", "alice"}
	b := Identity{"g", "bob"}
	owner := Identity{"g", "owner"}
	if _, e := s.Put(a, FileRef{Name: "main.py"}, []byte("before")); e != nil {
		t.Fatal(e)
	}
	s.Put(b, FileRef{Name: "secret.txt"}, []byte("bob"))
	s.Put(owner, FileRef{Owner: "public", Name: "template.txt"}, []byte("public"))
	snap, e := s.Snapshot(a, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	if _, e = os.Stat(filepath.Join(snap.Dir, "members")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("member sees other owners")
	}
	os.WriteFile(filepath.Join(snap.Dir, "mine", "main.py"), []byte("after"), 0600)
	os.WriteFile(filepath.Join(snap.Dir, "mine", "result.txt"), []byte("new"), 0600)
	changes, e := s.Changes(snap)
	if e != nil {
		t.Fatal(e)
	}
	if len(changes) != 2 {
		t.Fatalf("changes: %v", changes)
	}
	var pending *Pending
	for _, c := range changes {
		p, e := s.StageChange(a, c)
		if e != nil {
			t.Fatal(e)
		}
		if p != nil {
			pending = p
		}
	}
	if pending == nil {
		t.Fatal("overwriting original did not require approval")
	}
	if got, _ := s.Read(a, FileRef{Name: "main.py"}); string(got) != "before" {
		t.Fatal("native write bypassed confirmation")
	}
	if got, _ := s.Read(a, FileRef{Name: "result.txt"}); string(got) != "new" {
		t.Fatal("new output not published")
	}
	if e = s.Confirm(b, pending.Token); !errors.Is(e, ErrDenied) {
		t.Fatal("other person approved native output")
	}
	if e = s.Confirm(a, pending.Token); e != nil {
		t.Fatal(e)
	}
}
func TestNativeWorkspaceCannotPublishPublicEdits(t *testing.T) {
	s := newTestStore(t)
	owner := Identity{"g", "owner"}
	a := Identity{"g", "alice"}
	s.Put(owner, FileRef{Owner: "public", Name: "template.txt"}, []byte("public"))
	snap, e := s.Snapshot(a, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	os.WriteFile(filepath.Join(snap.Dir, "public", "template.txt"), []byte("bad"), 0600)
	if _, e = s.Changes(snap); !errors.Is(e, ErrDenied) {
		t.Fatalf("public edit published: %v", e)
	}
	if got, _ := s.Read(a, FileRef{Owner: "public", Name: "template.txt"}); string(got) != "public" {
		t.Fatal("original changed")
	}
}
func TestOwnerCanCreateFilesForOtherMembers(t *testing.T) {
	s := newTestStore(t)
	a := Identity{"g", "alice"}
	owner := Identity{"g", "owner"}
	s.Put(a, FileRef{Name: "a.txt"}, []byte("a"))
	snap, e := s.Snapshot(owner, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	name := filepath.Join(snap.Dir, "members", digestID("alice"), "new.txt")
	if e = os.WriteFile(name, []byte("admin output"), 0600); e != nil {
		t.Fatal(e)
	}
	changes, e := s.Changes(snap)
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range changes {
		if _, e = s.StageChange(owner, c); e != nil {
			t.Fatal(e)
		}
	}
	if got, e := s.Read(a, FileRef{Name: "new.txt"}); e != nil || string(got) != "admin output" {
		t.Fatalf("%q %v", got, e)
	}
}
func TestPersistentMembershipAndCrossGroupSeparation(t *testing.T) {
	dir := t.TempDir()
	s, e := NewStore(dir, "owner")
	if e != nil {
		t.Fatal(e)
	}
	s.Put(Identity{"g", "alice"}, FileRef{Name: ".gitignore"}, []byte("build/"))
	s.Close()
	s, e = NewStore(dir, "owner")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	snap, e := s.Snapshot(Identity{"g", "owner"}, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	if snap.Members[digestID("alice")] != "alice" {
		t.Fatal("membership lost after restart")
	}
	if names, e := s.List(Identity{"other", "alice"}, "mine"); e != nil || len(names) != 0 {
		t.Fatalf("cross group files leaked: %v %v", names, e)
	}
}

func TestUnchangedUserCCConnectDirectoryIsNotDeleted(t *testing.T) {
	s := newTestStore(t)
	id := Identity{"g", "alice"}
	if p, e := s.Put(id, FileRef{Name: ".cc-connect/report.txt"}, []byte("keep")); e != nil || p != nil {
		t.Fatal(p, e)
	}
	snap, e := s.Snapshot(id, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	changes, e := s.Changes(snap)
	if e != nil || len(changes) != 0 {
		t.Fatalf("unchanged user files interpreted as deleted: %+v %v", changes, e)
	}
}
