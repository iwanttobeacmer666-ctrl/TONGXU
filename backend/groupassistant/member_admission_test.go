package groupassistant

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestMemberTaskAdmissionIsAtomicAcrossGroupsAndOwnerExempt(t *testing.T) {
	svc, _ := testService(t, nilRunner())
	var wg sync.WaitGroup
	accepted := make(chan *groupTask, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, _, err := svc.addTask(context.Background(), Identity{"group", "wecom:alice"})
			if err == nil {
				accepted <- task
			} else if !errors.Is(err, errMemberBusy) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(accepted) != 1 {
		t.Fatalf("accepted %d simultaneous tasks from one member", len(accepted))
	}
	if _, _, err := svc.addTask(context.Background(), Identity{"other", "wecom:alice"}); !errors.Is(err, errMemberBusy) {
		t.Fatal("cross-group submission bypassed limit", err)
	}
	for _, user := range []string{"bob", "owner", "owner"} {
		task, _, err := svc.addTask(context.Background(), Identity{"group", "wecom:" + user})
		if err != nil {
			t.Fatal(user, err)
		}
		defer svc.finishTask(task)
	}
	svc.finishTask(<-accepted)
	task, _, err := svc.addTask(context.Background(), Identity{"other", "wecom:alice"})
	if err != nil {
		t.Fatal("completed task did not free member", err)
	}
	svc.finishTask(task)
}

func TestBusyMemberCannotUploadOrResetButCanConfirmAndQuery(t *testing.T) {
	entered := make(chan struct{}, 1)
	svc, store := testService(t, func(ctx context.Context, _ *Snapshot, _ string) (string, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	})
	actor := Identity{"group", "wecom:alice"}
	putTestFile(t, store, actor, "own.txt", "old")
	pending, err := store.Put(actor, FileRef{Name: "own.txt"}, []byte("confirmed"))
	if err != nil || pending == nil {
		t.Fatal(pending, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan Response, 1)
	go func() { done <- svc.Handle(ctx, memberMessage("alice", "first", "work")) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, command := range []string{"another task", "/compact", "/new"} {
		msg := memberMessage("alice", command, command)
		if command == "another task" {
			msg.Files = []core.FileAttachment{{FileName: "rejected.txt", Data: []byte("must not save")}}
		}
		if got := svc.Handle(ctx, msg); !strings.Contains(got.Text, svc.T(core.MsgGroupMemberBusy)) {
			t.Fatal(command, got)
		}
	}
	for _, command := range []string{"/help", "/files", "/read own.txt", "/context", "/whoami", "/confirm " + pending.Token} {
		if got := svc.Handle(ctx, memberMessage("alice", command, command)); got.Text == svc.T(core.MsgGroupMemberBusy) || got.Text == svc.T(core.MsgGroupDenied) {
			t.Fatal("short command blocked", command, got)
		}
	}
	for _, command := range []string{"/tasks", "/approve fake", "/read member:bob/private.txt", "/delete public/shared.txt"} {
		if got := svc.Handle(ctx, memberMessage("alice", command, command)); got.Text != svc.T(core.MsgGroupDenied) {
			t.Fatal("member gained privileged command", command, got)
		}
	}
	names, err := store.List(actor, "mine")
	if err != nil || len(names) != 1 || strings.Contains(strings.Join(names, "\n"), "rejected") {
		t.Fatal("rejected task saved an upload", names, err)
	}
	if data, err := store.Read(actor, FileRef{Name: "own.txt"}); err != nil || string(data) != "confirmed" {
		t.Fatal("confirmation could not proceed during work", string(data), err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled task did not finish")
	}
	if len(svc.slots) != 0 {
		t.Fatal("canceled task retained global slot")
	}
	svc.runner = nilRunner()
	if got := svc.Handle(context.Background(), memberMessage("alice", "next", "next task")); got.Text == svc.T(core.MsgGroupMemberBusy) {
		t.Fatal("cancellation retained member slot")
	}
}

func TestDisableQueuedMemberPreservesOtherUsersAndReleasesSlot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{}, 1)
	svc, _ := testService(t, func(ctx context.Context, _ *Snapshot, _ string) (string, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	})
	ownerDone := make(chan Response, 1)
	go func() { ownerDone <- svc.Handle(ctx, memberMessage("owner", "owner-work", "work")) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	done := make(chan Response, 1)
	go func() { done <- svc.Handle(ctx, memberMessage("alice", "queued", "work")) }()
	for {
		svc.tasksMu.Lock()
		count := len(svc.tasks)
		svc.tasksMu.Unlock()
		if count == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("member did not queue")
		case <-time.After(time.Millisecond):
		}
	}
	if err := svc.ApplyMember(ctx, "group", "alice", false, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("queued task not canceled")
	}
	if len(svc.slots) != 1 {
		t.Fatal("queued task retained slot or owner was canceled")
	}
	select {
	case <-entered:
		t.Fatal("revoked queued task executed")
	default:
	}
	if err := svc.ApplyMember(ctx, "group", "alice", true, func() error { return nil }); err != nil {
		t.Fatal("cleanly canceled member cannot be re-enabled", err)
	}
	cancel()
	select {
	case <-ownerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("owner task stuck")
	}
}

func TestMemberLimitPreservesGlobalFiveTaskCapacity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{}, 1)
	svc, _ := testService(t, func(ctx context.Context, _ *Snapshot, _ string) (string, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	})
	svc.cfg.Members["group"] = []string{"alice", "bob", "charlie", "dave"}
	done := make(chan Response, 5)
	go func() { done <- svc.Handle(ctx, memberMessage("owner", "owner", "work")) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, user := range svc.cfg.Members["group"] {
		go func(user string) { done <- svc.Handle(ctx, memberMessage(user, user, "work")) }(user)
	}
	for len(svc.slots) != 5 {
		select {
		case <-ctx.Done():
			t.Fatal("five distinct users could not queue")
		case <-time.After(time.Millisecond):
		}
	}
	if got := svc.Handle(ctx, memberMessage("owner", "sixth", "work")); got.Text != svc.T(core.MsgGroupBusy) {
		t.Fatal("owner bypassed global capacity", got)
	}
	if got := svc.Handle(ctx, memberMessage("alice", "help-while-full", "/help")); got.Text == svc.T(core.MsgGroupBusy) {
		t.Fatal("full queue blocked short command")
	}
	cancel()
	for i := 0; i < 5; i++ {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("canceled task stuck")
		}
	}
	if len(svc.slots) != 0 {
		t.Fatal("global slots leaked")
	}
}
