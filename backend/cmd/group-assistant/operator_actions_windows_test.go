package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
	"github.com/chenhg5/cc-connect/groupassistant"
)

func extendedManager(t *testing.T) (*operatorManager, *groupassistant.Store) {
	t.Helper()
	path := operatorFixture(t)
	store, err := groupassistant.NewStore(t.TempDir(), "wecom:owner")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	svc, err := groupassistant.NewService(groupassistant.Config{Owner: "owner", Groups: []string{"group", "other"}, Members: map[string][]string{"group": {"alice"}, "other": {"alice"}}, WorkDir: t.TempDir(), StateDir: t.TempDir()}, store, managementRunner{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	return &operatorManager{path: path, owner: "owner", service: svc}, store
}

type auditFloodRunner struct{}

func (auditFloodRunner) Run(_ context.Context, snap *groupassistant.Snapshot, _ string) (string, error) {
	for i := 0; i < 100; i++ {
		snap.Progress(core.Event{Type: core.EventToolResult, ToolName: "Bash", ToolResult: strings.Repeat("\x01", 3000)})
	}
	return "ran", nil
}

func TestEscapedAuditAndDocumentRepliesFitTheNativePipeFrame(t *testing.T) {
	m, store := extendedManager(t)
	svc, err := groupassistant.NewService(groupassistant.Config{Owner: "owner", Groups: []string{"group"}, Members: map[string][]string{"group": {"alice"}}, WorkDir: t.TempDir()}, store, auditFloodRunner{})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	m.service = svc
	if result := svc.Handle(context.Background(), &core.Message{ChannelID: "group", ChatName: "group", UserID: "alice", MessageID: "audit", Content: "hi"}); result.Text != "ran" {
		t.Fatal(result)
	}
	listing := m.handle(context.Background(), manageRequest{Action: "tasks"})
	if !listing.Success || len(listing.TaskItems) == 0 {
		t.Fatal(listing)
	}
	detail := m.handle(context.Background(), manageRequest{Action: "task-detail", Task: listing.TaskItems[0].ID})
	encoded, err := json.Marshal(detail)
	if err != nil || !detail.Success || len(encoded) > 262144 || len(detail.Groups) != 0 {
		t.Fatal("audit exceeded local pipe frame", len(encoded), err)
	}
	if _, err = store.Put(groupassistant.Identity{Group: "group", User: "wecom:alice"}, groupassistant.FileRef{Name: "large.txt"}, []byte(strings.Repeat("x", 70000))); err != nil {
		t.Fatal(err)
	}
	read := m.handle(context.Background(), manageRequest{Action: "document-read", Group: "group", User: "alice", Name: "large.txt"})
	encoded, err = json.Marshal(read)
	if err != nil || !read.Success || len(encoded) > 262144 || len(read.Groups) != 0 {
		t.Fatal("document exceeded local pipe frame", len(encoded), err)
	}
}

func TestLocalManagementHotGroupsAndDisabledMemberDeletionAcrossGroups(t *testing.T) {
	m, store := extendedManager(t)
	ctx := context.Background()
	view := m.handle(ctx, manageRequest{Action: "list"})
	apply := func(action, group, user string) manageReply {
		t.Helper()
		result := m.handle(ctx, manageRequest{Action: action, Version: view.Version, Group: group, User: user})
		if !result.Success {
			t.Fatalf("%s: %+v", action, result)
		}
		view = result
		return result
	}
	apply("group-add", "new-group", "")
	apply("add", "new-group", "bob")
	apply("group-disable", "new-group", "")
	apply("group-enable", "new-group", "")
	for _, group := range []string{"group", "other"} {
		if _, err := store.Put(groupassistant.Identity{Group: group, User: "wecom:alice"}, groupassistant.FileRef{Name: "private.txt"}, []byte("private")); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Put(groupassistant.Identity{Group: group, User: "wecom:owner"}, groupassistant.FileRef{Owner: "public", Name: "keep.txt"}, []byte("public")); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.handle(ctx, manageRequest{Action: "delete-preview", Version: view.Version, User: "alice", Scope: "all"}); got.Success {
		t.Fatal("enabled memberships deletable")
	}
	apply("disable", "group", "alice")
	apply("disable", "other", "alice")
	preview := m.handle(ctx, manageRequest{Action: "delete-preview", Version: view.Version, User: "alice", Scope: "all"})
	if !preview.Success || preview.DeletePreview == nil || len(preview.DeletePreview.Actors) != 2 || preview.DeletePreview.Files != 2 {
		t.Fatal(preview)
	}
	deleted := m.handle(ctx, manageRequest{Action: "delete", Version: view.Version, User: "alice", Scope: "all", Confirm: preview.DeletePreview.Token})
	if !deleted.Success {
		t.Fatal(deleted)
	}
	_, _, _, groups, err := readOperator(m.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		for _, user := range group.Members {
			if user.ID == "alice" {
				t.Fatal("deleted enrollment remains")
			}
		}
	}
	for _, group := range []string{"group", "other"} {
		data, err := store.Read(groupassistant.Identity{Group: group, User: "wecom:owner"}, groupassistant.FileRef{Owner: "public", Name: "keep.txt"})
		if err != nil || string(data) != "public" {
			t.Fatal("public data removed", err)
		}
	}
	encoded, err := os.ReadFile(m.path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["ProtectedSecret"]) != `"PRIVATE-FIXTURE"` {
		t.Fatal("shared private settings changed")
	}
}

func TestLocalDocumentManagementKeepsFramesBoundedAndChecksVersion(t *testing.T) {
	m, store := extendedManager(t)
	ctx := context.Background()
	owner := groupassistant.Identity{Group: "group", User: "wecom:owner"}
	if _, err := store.Put(owner, groupassistant.FileRef{Owner: "wecom:alice", Name: "test.txt"}, []byte("test")); err != nil {
		t.Fatal(err)
	}
	view := m.handle(ctx, manageRequest{Action: "list"})
	read := m.handle(ctx, manageRequest{Action: "document-read", Group: "group", User: "alice", Name: "test.txt"})
	if !read.Success || string(read.Documents.Data) != "test" {
		t.Fatal(read)
	}
	preview := m.handle(ctx, manageRequest{Action: "document-delete-preview", Group: "group", User: "alice", Name: "test.txt"})
	if !preview.Success {
		t.Fatal(preview)
	}
	stale := m.handle(ctx, manageRequest{Action: "document-delete", Version: "old", Group: "group", User: "alice", Confirm: preview.Documents.Pending.Token})
	if stale.Success {
		t.Fatal("stale version accepted")
	}
	deleted := m.handle(ctx, manageRequest{Action: "document-delete", Version: view.Version, Group: "group", User: "alice", Confirm: preview.Documents.Pending.Token})
	if !deleted.Success {
		t.Fatal(deleted)
	}
	if read := m.handle(ctx, manageRequest{Action: "document-read", Group: "group", User: "alice", Name: "../outside"}); read.Success {
		t.Fatal("path traversal accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(m.path), "outside")); !os.IsNotExist(err) {
		t.Fatal("unexpected external document")
	}
}

func TestIncompleteLifecycleCannotBeResetByRepeatingDisable(t *testing.T) {
	for _, state := range []string{"disabling", "cleanup_failed", "deleting", "delete_failed"} {
		t.Run(state, func(t *testing.T) {
			m, _ := extendedManager(t)
			raw, _, _, groups, err := readOperator(m.path)
			if err != nil {
				t.Fatal(err)
			}
			groups[0].Members[0].Enabled = false
			groups[0].Members[0].State = state
			raw["Groups"], _ = json.Marshal(groups)
			data, _ := json.Marshal(raw)
			if err = os.WriteFile(m.path, data, 0600); err != nil {
				t.Fatal(err)
			}
			view := m.handle(context.Background(), manageRequest{Action: "list"})
			for _, action := range []string{"disable", "enable"} {
				result := m.handle(context.Background(), manageRequest{Action: action, Version: view.Version, Group: "group", User: "alice"})
				if result.Success || result.Applied {
					t.Fatal("incomplete state bypassed", action, result)
				}
			}
		})
	}
}
