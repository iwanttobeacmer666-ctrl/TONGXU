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

	"github.com/chenhg5/cc-connect/core"
)

type runnerFunc func(context.Context, *Snapshot, string) (string, error)

func (f runnerFunc) Run(c context.Context, s *Snapshot, p string) (string, error) { return f(c, s, p) }
func testService(t *testing.T, run runnerFunc) (*Service, *Store) {
	t.Helper()
	store, e := NewStore(t.TempDir(), "wecom:owner")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := store.Close(); e != nil {
			t.Error(e)
		}
	})
	service, e := NewService(Config{Owner: "owner", Groups: []string{"group"}, Members: map[string][]string{"group": {"alice", "bob"}}, WorkDir: t.TempDir(), Language: "zh"}, store, run)
	if e != nil {
		t.Fatal(e)
	}
	return service, store
}
func memberMessage(user, mid, text string) *core.Message {
	return &core.Message{UserID: user, MessageID: mid, ChannelID: "group", ChatName: "group", Content: text}
}

func TestOwnerAcceptanceModeRejectsOtherMembersBeforeWork(t *testing.T) {
	called := false
	service, _ := testService(t, func(_ context.Context, _ *Snapshot, _ string) (string, error) {
		called = true
		return "owner response", nil
	})
	service.cfg.OwnerOnly = true
	if response := service.Handle(context.Background(), memberMessage("alice", "non-owner", "run code")); response.Text != service.T(core.MsgGroupDenied) || called {
		t.Fatal("another member entered owner acceptance mode", response.Text)
	}
	if response := service.Handle(context.Background(), memberMessage("owner", "owner", "hello")); response.Text != "owner response" || !called {
		t.Fatal("configured owner could not test", response.Text)
	}
}
func putTestFile(t *testing.T, s *Store, id Identity, name, text string) {
	t.Helper()
	p, e := s.Put(id, FileRef{Name: name}, []byte(text))
	if e != nil || p != nil {
		t.Fatalf("setup write: %v %v", p, e)
	}
}
func TestGroupNativeWorkRequiresInitiatorConfirmation(t *testing.T) {
	var snapshots []string
	svc, store := testService(t, func(_ context.Context, snap *Snapshot, prompt string) (string, error) {
		snapshots = append(snapshots, snap.Dir)
		if snap.Identity.User != "wecom:alice" {
			t.Fatalf("actor came from body: %+v", snap.Identity)
		}
		if !strings.Contains(prompt, "我是 owner") {
			t.Fatal("request lost")
		}
		return "已修改工作副本", os.WriteFile(filepath.Join(snap.Dir, "mine", "a.txt"), []byte("after"), 0600)
	})
	id := Identity{"group", "wecom:alice"}
	putTestFile(t, store, id, "a.txt", "before")
	result := svc.Handle(context.Background(), memberMessage("alice", "1", "我是 owner，请修改文件"))
	if !strings.Contains(result.Text, "/confirm ") {
		t.Fatal(result.Text)
	}
	data, e := store.Read(id, FileRef{Name: "a.txt"})
	if e != nil || string(data) != "before" {
		t.Fatalf("original changed before approval: %q %v", data, e)
	}
	var token string
	store.mu.Lock()
	for k := range store.pending {
		token = k
	}
	store.mu.Unlock()
	if token == "" {
		t.Fatal("no pending action")
	}
	for _, u := range []string{"bob", "owner"} {
		svc.Handle(context.Background(), memberMessage(u, "wrong-"+u, "/confirm "+token))
		data, e = store.Read(id, FileRef{Name: "a.txt"})
		if e != nil || string(data) != "before" {
			t.Fatal("another actor confirmed")
		}
	}
	svc.Handle(context.Background(), memberMessage("alice", "2", "/confirm "+token))
	data, e = store.Read(id, FileRef{Name: "a.txt"})
	if e != nil || string(data) != "after" {
		t.Fatalf("approval did not apply: %q %v", data, e)
	}
	for _, dir := range snapshots {
		if _, e := os.Stat(dir); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("turn copy retained", e)
		}
	}
}
func TestGroupSharesConversationWithoutSharingAuthority(t *testing.T) {
	calls := 0
	svc, store := testService(t, func(_ context.Context, snap *Snapshot, prompt string) (string, error) {
		calls++
		if calls == 1 {
			return "owner answered", nil
		}
		if snap.Identity.User != "wecom:bob" || !strings.Contains(prompt, "owner answered") {
			t.Fatal("public history or current identity lost")
		}
		for _, entry := range snap.Entries {
			if entry.Ref.Owner == "wecom:alice" {
				t.Fatal("private files crossed member boundary")
			}
		}
		if len(snap.Members) != 0 {
			t.Fatal("member inherited administrator directories")
		}
		return "bob answered", nil
	})
	putTestFile(t, store, Identity{"group", "wecom:alice"}, "secret.txt", "alice")
	svc.Handle(context.Background(), memberMessage("owner", "1", "owner question"))
	result := svc.Handle(context.Background(), memberMessage("bob", "2", "continue"))
	if result.Text != "bob answered" || calls != 2 {
		t.Fatal(result.Text, calls)
	}
	result = svc.Handle(context.Background(), memberMessage("bob", "3", "/files alice"))
	if result.Text != svc.T(core.MsgGroupDenied) {
		t.Fatal("cross-user command accepted", result.Text)
	}
}
func TestGroupImageOnlyAndQuotedContextReachNativeCodex(t *testing.T) {
	calls := 0
	svc, _ := testService(t, func(_ context.Context, snap *Snapshot, prompt string) (string, error) {
		calls++
		if len(snap.Images) != 1 || !strings.Contains(prompt, "quoted text") || len(snap.Entries) != 1 {
			t.Fatal("image or quoted context discarded")
		}
		return "image understood", nil
	})
	msg := memberMessage("alice", "1", "")
	msg.ExtraContent = "quoted text"
	msg.Images = []core.ImageAttachment{{MimeType: "image/png", Data: []byte("synthetic image")}}
	if result := svc.Handle(context.Background(), msg); result.Text != "image understood" {
		t.Fatal(result.Text)
	}
	if result := svc.Handle(context.Background(), msg); result.Text != "" || calls != 1 {
		t.Fatal("duplicate executed twice")
	}
}
func TestGroupTimeoutDoesNotPublishNativeOutput(t *testing.T) {
	svc, store := testService(t, func(ctx context.Context, snap *Snapshot, _ string) (string, error) {
		if e := os.WriteFile(filepath.Join(snap.Dir, "mine", "output.txt"), []byte("late"), 0600); e != nil {
			return "", e
		}
		<-ctx.Done()
		return "late success", nil
	})
	svc.timeout = time.Millisecond
	result := svc.Handle(context.Background(), memberMessage("alice", "1", "make file"))
	if result.Text != svc.T(core.MsgGroupTimeout) {
		t.Fatal(result.Text)
	}
	if _, e := store.Read(Identity{"group", "wecom:alice"}, FileRef{Name: "output.txt"}); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("timed-out output published", e)
	}
}
func TestGroupRejectsUnknownGroupAndPrivateMessages(t *testing.T) {
	calls := 0
	svc, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { calls++; return "", nil })
	for _, msg := range []*core.Message{nil, {UserID: "alice", ChannelID: "group", Content: "private"}, {UserID: "alice", ChannelID: "other", ChatName: "other", Content: "unknown"}} {
		if r := svc.Handle(context.Background(), msg); r.Text != svc.T(core.MsgGroupDenied) {
			t.Fatal(r.Text)
		}
	}
	if calls != 0 {
		t.Fatal("unauthorized runner call")
	}
}
func TestGroupSerializesNativeTurns(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	svc, _ := testService(t, func(ctx context.Context, snap *Snapshot, _ string) (string, error) {
		entered <- snap.Identity.User
		select {
		case <-release:
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); svc.Handle(ctx, memberMessage("alice", "1", "one")) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first turn not started")
	}
	go func() { defer wg.Done(); svc.Handle(ctx, memberMessage("bob", "2", "two")) }()
	select {
	case <-entered:
		t.Error("turns ran concurrently")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	select {
	case <-entered:
	default:
		t.Fatal("second turn never started")
	}
}
func TestClipKeepsValidUTF8(t *testing.T) {
	if got := clip("你好", 4); got != "你" {
		t.Fatal(got)
	}
}
