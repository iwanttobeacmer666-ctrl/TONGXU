package groupassistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestUpdateDrainPreservesRunningTaskAndFencesAdmission(t *testing.T) {
	started, finish, done := make(chan struct{}), make(chan struct{}), make(chan Response, 1)
	svc, _ := testService(t, func(ctx context.Context, _ *Snapshot, _ string) (string, error) {
		close(started)
		select {
		case <-finish:
			return "finished", nil
		case <-ctx.Done():
			return "cancelled", ctx.Err()
		}
	})
	defer svc.Close()
	probe, err := svc.store.Snapshot(Identity{"group", "wecom:alice"}, svc.cfg.WorkDir)
	if err != nil {
		t.Fatalf("storage prerequisite: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- svc.Handle(context.Background(), memberMessage("alice", "first", "work")) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not start")
	}
	svc.PauseForUpdate(true)
	if svc.UpdateReady() {
		t.Fatal("running task treated as idle")
	}
	if got := svc.Handle(context.Background(), memberMessage("bob", "second", "new work")); got.Text != svc.T(core.MsgGroupBusy) {
		t.Fatal("new task entered", got)
	}
	if _, err := svc.AdmitMedia(context.Background(), memberMessage("bob", "upload", "file"), 1); !errors.Is(err, ErrBusy) {
		t.Fatal("media was admitted", err)
	}
	if got := svc.Handle(context.Background(), memberMessage("alice", "identity", "/whoami")); got.Text == svc.T(core.MsgGroupBusy) {
		t.Fatal("control command blocked")
	}
	close(finish)
	select {
	case got := <-done:
		if got.Text != "finished" {
			t.Fatal("task was cancelled", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("task did not finish")
	}
	if !svc.UpdateReady() {
		t.Fatal("completed work did not drain")
	}
	svc.PauseForUpdate(false)
	if svc.UpdateReady() {
		t.Fatal("resume left update fence active")
	}
	// Use a short command that is normally allowed but blocked during drain.
	if got := svc.Handle(context.Background(), memberMessage("bob", "resumed", "/files")); got.Text == svc.T(core.MsgGroupBusy) {
		t.Fatal("resume failed")
	}
}

func TestUpdateDrainIncludesReservedMediaAndAllowsItsCompletion(t *testing.T) {
	svc, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "uploaded", nil })
	defer svc.Close()
	msg := memberMessage("alice", "reserved", "process file")
	admission, err := svc.AdmitMedia(context.Background(), msg, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer admission.Close()
	svc.PauseForUpdate(true)
	if svc.UpdateReady() {
		t.Fatal("reserved media ignored")
	}
	admission.Downloaded()
	msg.Files = []core.FileAttachment{{FileName: "a.txt", Data: []byte("hello")}}
	if got := svc.Handle(admission.Context, msg); got.Text != "uploaded" {
		t.Fatal("reserved upload interrupted", got)
	}
	if svc.UpdateReady() {
		t.Fatal("reservation cleanup not awaited")
	}
	admission.Close()
	if !svc.UpdateReady() {
		t.Fatal("closed reservation did not drain")
	}
}

func TestUpdateDrainRefusesUnconfirmedCleanup(t *testing.T) {
	svc, _ := testService(t, nilRunner())
	defer svc.Close()
	task, _, err := svc.addTask(context.Background(), Identity{"group", "wecom:alice"})
	if err != nil {
		t.Fatal(err)
	}
	task.cleanupErr = errors.New("synthetic process cleanup uncertainty")
	svc.finishTask(task)
	svc.PauseForUpdate(true)
	if svc.UpdateReady() {
		t.Fatal("uncertain cleanup treated as safe to replace program")
	}
}
