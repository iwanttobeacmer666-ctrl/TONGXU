package groupassistant

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestEnrollmentRejectsUnknownBeforeAnyWork(t *testing.T) {
	called := false
	svc, store := testService(t, func(context.Context, *Snapshot, string) (string, error) {
		called = true
		return "ok", nil
	})
	// A historical file owner must not gain enrollment automatically.
	putTestFile(t, store, Identity{"group", "wecom:unknown"}, "old.txt", "private")
	for i, text := range []string{"hello", "/help", "/token", "/model", "/context", "/compact", "/new", "/files", "/read old.txt", "/delete old.txt", "/approve x", "/whoami extra"} {
		got := svc.Handle(context.Background(), memberMessage("unknown", fmt.Sprint(i), text))
		if !strings.Contains(got.Text, svc.T(core.MsgGroupDenied)) || len(got.Files) != 0 || called {
			t.Fatalf("unknown member admitted for %q: %+v", text, got)
		}
	}
	for _, ownerOnly := range []bool{false, true} {
		svc.cfg.OwnerOnly = ownerOnly
		got := svc.Handle(context.Background(), memberMessage("unknown", fmt.Sprint("identity", ownerOnly), "/whoami"))
		if !strings.Contains(got.Text, "unknown") || !strings.Contains(got.Text, "group") || called {
			t.Fatalf("identity discovery blocked or entered work: %+v", got)
		}
	}
	if len(svc.chat.History("group")) != 0 {
		t.Fatal("unregistered traffic entered shared history")
	}
}

func TestEnrollmentIsPerGroupAndMissingFailsClosed(t *testing.T) {
	svc, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "allowed", nil })
	svc.cfg.Groups = []string{"group", "other"}
	msg := memberMessage("alice", "cross-group", "hello")
	msg.ChannelID = "other"
	if got := svc.Handle(context.Background(), msg); got.Text != svc.T(core.MsgGroupDenied) {
		t.Fatal("enrollment leaked across groups", got)
	}
	svc.cfg.Members = nil
	if got := svc.Handle(context.Background(), memberMessage("alice", "missing", "hello")); got.Text != svc.T(core.MsgGroupDenied) {
		t.Fatal("missing enrollment admitted member", got)
	}
	if got := svc.Handle(context.Background(), memberMessage("owner", "owner", "hello")); got.Text != "allowed" {
		t.Fatal("owner no longer admitted", got)
	}
}

func TestEnrollmentValidation(t *testing.T) {
	for _, users := range [][]string{{"a", "a"}, {"owner"}, {" a"}, {"wecom:a"}, {"bad\nuser"}, {""}, {"YOUR_ID"}} {
		if ValidateMembers("owner", []string{"g"}, map[string][]string{"g": users}) == nil {
			t.Fatal("invalid enrollment accepted", users)
		}
	}
	if ValidateMembers("owner", []string{"g"}, map[string][]string{"other": {"a"}}) == nil {
		t.Fatal("unknown group accepted")
	}
	if ValidateMembers("owner", []string{"g"}, map[string][]string{"g": {}}) != nil {
		t.Fatal("zero members rejected")
	}
}

func TestMoreThanFiveHistoricalMembersSurviveRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "files")
	store, err := NewStore(dir, "wecom:owner")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		putTestFile(t, store, Identity{"g", fmt.Sprint("wecom:user", i)}, "file.txt", "retained")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewStore(dir, "wecom:owner")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if len(store.members["g"]) != 8 {
		t.Fatal("member records lost")
	}
	data, err := store.Read(Identity{"g", "wecom:user7"}, FileRef{Name: "file.txt"})
	if err != nil || string(data) != "retained" {
		t.Fatal("file lost", err)
	}
}
