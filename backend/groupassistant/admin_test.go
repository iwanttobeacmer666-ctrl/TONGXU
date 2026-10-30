package groupassistant

import (
	"context"
	"errors"
	"github.com/chenhg5/cc-connect/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdminScopedFilesAndFreshMember(t *testing.T) {
	svc, store := testService(t, func(_ context.Context, snap *Snapshot, _ string) (string, error) {
		if len(snap.Entries) != 0 {
			t.Fatal("chat copied member files")
		}
		return "chat", nil
	})
	for _, user := range []string{"alice", "bob"} {
		putTestFile(t, store, Identity{"group", "wecom:" + user}, "large.bin", strings.Repeat("x", 5<<20))
	}
	if got := svc.Handle(context.Background(), memberMessage("owner", "chat", "hello")); got.Text != "chat" {
		t.Fatal(got)
	}
	svc.cfg.Members["group"] = append(svc.cfg.Members["group"], "fresh")
	svc.runner = runnerFunc(func(_ context.Context, snap *Snapshot, _ string) (string, error) {
		if snap.Members[digestID("wecom:fresh")] != "wecom:fresh" {
			t.Fatal("fresh mapping absent")
		}
		return "created", os.WriteFile(filepath.Join(snap.Dir, "members", digestID("wecom:fresh"), "first.txt"), []byte("first"), 0600)
	})
	got := svc.Handle(context.Background(), memberMessage("owner", "newfile", "给 fresh 创建 first.txt"))
	if !strings.Contains(got.Text, "created") {
		t.Fatal(got)
	}
	if data, err := store.Read(Identity{"group", "wecom:fresh"}, FileRef{Name: "first.txt"}); err != nil || string(data) != "first" {
		t.Fatal(string(data), err)
	}
	got = svc.Handle(context.Background(), memberMessage("owner", "read", "/read member:fresh/first.txt"))
	if len(got.Files) != 1 {
		t.Fatal(got)
	}
	if got := svc.Handle(context.Background(), memberMessage("alice", "deny", "/read member:fresh/first.txt")); got.Text != svc.T(core.MsgGroupDenied) {
		t.Fatal(got)
	}
	if ref := parseRef("alice/own.txt"); ref.Owner != "" || ref.Name != "alice/own.txt" {
		t.Fatal("old syntax changed", ref)
	}
}

func TestAdminStopWaitsForCleanupAndDiscardsOutput(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	svc, store := testService(t, func(ctx context.Context, snap *Snapshot, _ string) (string, error) {
		if err := os.WriteFile(filepath.Join(snap.Dir, "mine", "unpublished.txt"), []byte("secret"), 0600); err != nil {
			return "", err
		}
		close(entered)
		<-ctx.Done()
		<-release
		return "", ctx.Err()
	})
	done := make(chan Response, 1)
	go func() { done <- svc.Handle(context.Background(), memberMessage("alice", "work", "private body")) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	listing := svc.Handle(context.Background(), memberMessage("owner", "list", "/tasks"))
	if strings.Contains(listing.Text, "private body") || !strings.Contains(listing.Text, "alice") {
		t.Fatal(listing)
	}
	svc.tasksMu.Lock()
	var code string
	for key := range svc.tasks {
		code = key
	}
	svc.tasksMu.Unlock()
	other := Identity{"other", "wecom:owner"}
	if got := svc.taskCommand(context.Background(), other, "/stop", code); got.Text != svc.T(core.MsgGroupDenied) {
		t.Fatal("cross group stop", got)
	}
	if got := svc.taskCommand(context.Background(), Identity{"group", "wecom:alice"}, "/stop", code); got.Text != svc.T(core.MsgGroupDenied) {
		t.Fatal("member stop", got)
	}
	stopped := make(chan Response, 1)
	go func() { stopped <- svc.Handle(context.Background(), memberMessage("owner", "stop", "/stop "+code)) }()
	select {
	case <-stopped:
		t.Fatal("reported stop before cleanup")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case got := <-stopped:
		if got.Text != svc.T(core.MsgGroupTaskStopped) {
			t.Fatal(got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop timeout")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("work stuck")
	}
	if _, err := store.Read(Identity{"group", "wecom:alice"}, FileRef{Name: "unpublished.txt"}); err == nil {
		t.Fatal("canceled output published")
	}
	if !onlyCancellation(errors.Join(context.Canceled, nil)) || onlyCancellation(errors.Join(context.Canceled, errors.New("cleanup failed"))) {
		t.Fatal("cleanup error hidden")
	}
}

func TestAdminStopQueuedAndPublicationFence(t *testing.T) {
	svc, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	task, ctx, err := svc.addTask(context.Background(), Identity{"group", "wecom:alice"})
	if err != nil {
		t.Fatal(err)
	}
	go func() { <-ctx.Done(); svc.finishTask(task) }()
	got := svc.taskCommand(context.Background(), Identity{"group", "wecom:owner"}, "/stop", task.code)
	if got.Text != svc.T(core.MsgGroupTaskStopped) {
		t.Fatal(got)
	}
	task, _, err = svc.addTask(context.Background(), Identity{"group", "wecom:alice"})
	if err != nil {
		t.Fatal(err)
	}
	task.state = "publishing"
	got = svc.taskCommand(context.Background(), Identity{"group", "wecom:owner"}, "/stop", task.code)
	if got.Text != svc.T(core.MsgGroupTaskPublishing) || task.stopping {
		t.Fatal(got)
	}
	svc.finishTask(task)
}

func TestModelSelectionOnlyInvalidatesCurrentGroup(t *testing.T) {
	svc, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	svc.runner = &chatRunner{inspection: chatFixture()}
	id := Identity{"group", "wecom:owner"}
	svc.controlCommand(context.Background(), id, "/model", "")
	otherKey := pickerKey(Identity{"other", "wecom:owner"}, "/thinking")
	svc.pickers[otherKey] = modelPicker{Group: "other", At: time.Now()}
	svc.controlCommand(context.Background(), id, "/model", "1")
	if _, ok := svc.pickers[otherKey]; !ok {
		t.Fatal("other group menu removed")
	}
}

func TestAdminStopDoesNotHideCleanupFailure(t *testing.T) {
	entered := make(chan struct{})
	svc, _ := testService(t, func(ctx context.Context, _ *Snapshot, _ string) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", errors.Join(ctx.Err(), errors.New("cleanup failed"))
	})
	done := make(chan Response, 1)
	go func() { done <- svc.Handle(context.Background(), memberMessage("alice", "failed-cleanup", "run")) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	svc.tasksMu.Lock()
	var code string
	for key := range svc.tasks {
		code = key
	}
	svc.tasksMu.Unlock()
	got := svc.taskCommand(context.Background(), Identity{"group", "wecom:owner"}, "/stop", code)
	if got.Text != svc.T(core.MsgGroupTaskUncertain) {
		t.Fatal("false stop success", got)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("work stuck")
	}
}
