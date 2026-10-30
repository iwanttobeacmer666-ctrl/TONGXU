package groupassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestLiveDisableRevokesTasksTokensAndOnlySelectedGroup(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var runs atomic.Int32
	svc, store := testService(t, func(ctx context.Context, snap *Snapshot, _ string) (string, error) {
		runs.Add(1)
		if err := os.WriteFile(filepath.Join(snap.Dir, "mine", "unpublished.txt"), []byte("not published"), 0600); err != nil {
			return "", err
		}
		close(entered)
		<-ctx.Done()
		<-release
		return "", ctx.Err()
	})
	svc.cfg.Groups = append(svc.cfg.Groups, "other")
	svc.cfg.Members["other"] = []string{"alice"}
	actor := Identity{"group", "wecom:alice"}
	putTestFile(t, store, actor, "keep.txt", "old")
	pending, err := store.Put(actor, FileRef{Name: "keep.txt"}, []byte("new"))
	if err != nil || pending == nil {
		t.Fatal(err)
	}
	approvalCode := make(chan string, 1)
	approvalDone := make(chan bool, 1)
	go func() {
		ok, _ := svc.approvals.Wait(context.Background(), actor, func(code string) error { approvalCode <- code; return nil })
		approvalDone <- ok
	}()
	var code string
	select {
	case code = <-approvalCode:
	case <-time.After(3 * time.Second):
		t.Fatal("approval not registered")
	}
	done := make(chan Response, 1)
	go func() { done <- svc.Handle(context.Background(), memberMessage("alice", "running", "run")) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("task not started")
	}
	deadline := time.Now().Add(3 * time.Second)
	applied := make(chan error, 1)
	go func() {
		applied <- svc.ApplyMember(context.Background(), "group", "alice", false, func() error { return nil })
	}()
	for svc.actorEnabled(actor) {
		if time.Now().After(deadline) {
			t.Fatal("admission not revoked")
		}
		time.Sleep(time.Millisecond)
	}
	if got := svc.Handle(context.Background(), memberMessage("alice", "new", "hello")); got.Text != svc.T(core.MsgGroupDenied) {
		t.Fatal(got)
	}
	other := memberMessage("alice", "other", "hello")
	other.ChannelID = "other"
	if _, err := svc.identity(other); err != nil {
		t.Fatal("other group revoked", err)
	}
	if err := store.Confirm(actor, pending.Token); err == nil {
		t.Fatal("old file confirmation survived")
	}
	if err := svc.approvals.Resolve(Identity{"group", "wecom:owner"}, code, true); err == nil {
		t.Fatal("old approval survived")
	}
	select {
	case ok := <-approvalDone:
		if ok {
			t.Fatal("revoked approval allowed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approval stuck")
	}
	if err := svc.ApplyMember(context.Background(), "group", "alice", true, func() error { t.Fatal("enabled before cleanup"); return nil }); err == nil {
		t.Fatal("pending cleanup permitted enable")
	}
	close(release)
	select {
	case err := <-applied:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disable stuck")
	}
	for i := 0; i < 1; i++ {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("task stuck")
		}
	}
	if runs.Load() != 1 {
		t.Fatal("queued task executed", runs.Load())
	}
	if _, err := store.Read(actor, FileRef{Name: "unpublished.txt"}); err == nil {
		t.Fatal("canceled result published")
	}
	if data, err := store.Read(actor, FileRef{Name: "keep.txt"}); err != nil || string(data) != "old" {
		t.Fatal("saved data lost", err)
	}
	if err := svc.ApplyMember(context.Background(), "group", "alice", true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.Confirm(actor, pending.Token); err == nil {
		t.Fatal("confirmation revived")
	}
}

func TestLiveMembershipSaveFailureAndPublicationFence(t *testing.T) {
	svc, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	if err := svc.ApplyMember(context.Background(), "group", "alice", false, func() error { return errors.New("disk failure") }); err == nil || !svc.actorEnabled(Identity{"group", "wecom:alice"}) {
		t.Fatal("failed save changed authority")
	}
	svc.accessMu.RLock()
	persisted := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- svc.ApplyMember(context.Background(), "group", "alice", false, func() error { close(persisted); return nil })
	}()
	select {
	case <-persisted:
		t.Fatal("changed membership while publishing")
	case <-time.After(20 * time.Millisecond):
	}
	svc.accessMu.RUnlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("update stuck")
	}
}
