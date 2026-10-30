package groupassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestLiveRosterAddsGroupsAndCanRemainIdle(t *testing.T) {
	s, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "ran", nil })
	ctx := context.Background()
	groups := []string{"group", "new-group"}
	members := map[string][]string{"group": {"alice"}, "new-group": {"bob"}}
	if err := s.ApplyRoster(ctx, groups, members, func() error { return errors.New("disk") }); err == nil {
		t.Fatal("failed save accepted")
	}
	msg := memberMessage("bob", "new", "hi")
	msg.ChannelID = "new-group"
	if _, err := s.identity(msg); err == nil {
		t.Fatal("failed save expanded permissions")
	}
	if err := s.ApplyRoster(ctx, groups, members, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if result := s.Handle(ctx, msg); result.Text != "ran" {
		t.Fatal(result)
	}
	if err := s.ApplyRoster(ctx, nil, nil, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"owner", "bob"} {
		msg.UserID = user
		if _, err := s.identity(msg); err == nil {
			t.Fatal("disabled group admitted", user)
		}
	}
	if err := s.ApplyRoster(ctx, groups, members, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	msg.UserID = "bob"
	msg.MessageID = "restored"
	if result := s.Handle(ctx, msg); result.Text != "ran" {
		t.Fatal("restored group not live", result)
	}
}

type cleaningRunner struct {
	runnerFunc
	deleted []string
	fail    bool
}

func (r *cleaningRunner) DeleteThreads(_ context.Context, _ string, ids []string) error {
	if r.fail {
		return ErrUnavailable
	}
	r.deleted = append(r.deleted, ids...)
	return nil
}

func TestMemberDeleteTracksOldThreadsAndKeepsOtherUsersAndPublic(t *testing.T) {
	ctx := context.Background()
	s, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	s.cfg.SoftwareDir = t.TempDir()
	runner := &cleaningRunner{runnerFunc: func(context.Context, *Snapshot, string) (string, error) { return "", nil }}
	s.runner = runner
	alice := Identity{"group", "wecom:alice"}
	bob := Identity{"group", "wecom:bob"}
	owner := Identity{"group", "wecom:owner"}
	putTestFile(t, store, alice, "own.txt", "private")
	putTestFile(t, store, bob, "keep.txt", "keep")
	if _, err := store.Put(owner, FileRef{Owner: "public", Name: "public.txt"}, []byte("shared")); err != nil {
		t.Fatal(err)
	}
	for _, thread := range []string{"old-thread", "new-thread"} {
		if err := s.chat.SaveConversation(alice, Conversation{Identity: alice, ThreadID: thread}); err != nil {
			t.Fatal(err)
		}
		if err := s.chat.Reset(alice); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.chat.SaveConversation(bob, Conversation{Identity: bob, ThreadID: "old-thread"}); err == nil {
		t.Fatal("old native thread rebound after /new")
	}
	software := filepath.Join(s.cfg.SoftwareDir, digestID(alice.Group), digestID(alice.User))
	if err := os.MkdirAll(software, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(software, "dependency.txt"), []byte("package"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewMemberDelete(ctx, owner.User, []Identity{alice}); err == nil {
		t.Fatal("enabled member deletable")
	}
	if err := s.ApplyMember(ctx, "group", "alice", false, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewMemberDelete(ctx, owner.User, []Identity{alice})
	if err != nil || preview.Files != 2 || preview.Threads != 2 || !preview.PublicPreserved {
		t.Fatal(preview, err)
	}
	pending, err := store.Put(owner, FileRef{Owner: alice.User, Name: "own.txt"}, []byte("changed"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Confirm(owner, pending.Token); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMembers(ctx, owner.User, preview.Token, []Identity{alice}, func(string) error { t.Fatal("stale preview wrote lifecycle"); return nil }, func() error { return nil }); !errors.Is(err, ErrChanged) {
		t.Fatal("changed file accepted", err)
	}
	runner.fail = true
	preview, err = s.PreviewMemberDelete(ctx, owner.User, []Identity{alice})
	if err != nil {
		t.Fatal(err)
	}
	var stages []string
	stage := func(v string) error { stages = append(stages, v); return nil }
	if err := s.DeleteMembers(ctx, owner.User, preview.Token, []Identity{alice}, stage, func() error { t.Fatal("failed native deletion removed enrollment"); return nil }); err == nil {
		t.Fatal("native failure hidden")
	}
	if len(stages) != 2 || stages[1] != "delete_failed" {
		t.Fatal(stages)
	}
	if _, err := os.Stat(filepath.Join(store.root.Name(), filepath.FromSlash(memberBase(alice)), "own.txt")); err != nil {
		t.Fatal("native failure removed documents", err)
	}
	runner.fail = false
	preview, err = s.PreviewMemberDelete(ctx, owner.User, []Identity{alice})
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	if err := s.DeleteMembers(ctx, owner.User, preview.Token, []Identity{alice}, stage, func() error { finished = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !finished || len(runner.deleted) != 2 || len(s.chat.threadIDs(alice)) != 0 {
		t.Fatal("incomplete member cleanup", runner.deleted)
	}
	if _, err := os.Stat(filepath.Join(store.root.Name(), filepath.FromSlash(memberBase(alice)))); !os.IsNotExist(err) {
		t.Fatal("private directory remains", err)
	}
	if _, err := os.Stat(software); !os.IsNotExist(err) {
		t.Fatal("software remains", err)
	}
	if data, err := store.Read(bob, FileRef{Name: "keep.txt"}); err != nil || string(data) != "keep" {
		t.Fatal(err)
	}
	if data, err := store.Read(owner, FileRef{Owner: "public", Name: "public.txt"}); err != nil || string(data) != "shared" {
		t.Fatal("public data changed", err)
	}
}

func TestThreadOwnershipSurvivesRestartAndReset(t *testing.T) {
	dir := t.TempDir()
	state, err := newChatState(dir)
	if err != nil {
		t.Fatal(err)
	}
	actor := Identity{"g", "wecom:a"}
	if err = state.SaveConversation(actor, Conversation{Identity: actor, ThreadID: "thread-a"}); err != nil {
		t.Fatal(err)
	}
	if err = state.Reset(actor); err != nil {
		t.Fatal(err)
	}
	if err = state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = newChatState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if ids := state.threadIDs(actor); len(ids) != 1 || ids[0] != "thread-a" {
		t.Fatal(ids)
	}
}

func TestLiveGroupReenableRestoresPersistedPublicContext(t *testing.T) {
	dir := t.TempDir()
	chat, err := newChatState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = chat.SaveHistory("other", []string{"PUBLIC_HISTORY_SENTINEL"}); err != nil {
		t.Fatal(err)
	}
	chat.Close()
	store, err := NewStore(t.TempDir(), "wecom:owner")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s, err := NewService(Config{Owner: "owner", Groups: []string{"group"}, WorkDir: t.TempDir(), StateDir: dir}, store, runnerFunc(func(_ context.Context, _ *Snapshot, prompt string) (string, error) {
		if !strings.Contains(prompt, "PUBLIC_HISTORY_SENTINEL") {
			t.Fatal("reenabling discarded persisted public context")
		}
		return "ran", nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.ApplyRoster(context.Background(), []string{"group", "other"}, map[string][]string{"other": {"alice"}}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	msg := memberMessage("alice", "reenabled", "hi")
	msg.ChannelID = "other"
	if response := s.Handle(context.Background(), msg); response.Text != "ran" {
		t.Fatal(response)
	}
}

func TestAdminDocumentsPageReadsAndRejectStaleDelete(t *testing.T) {
	s, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	owner := Identity{"group", "wecom:owner"}
	data := []byte(strings.Repeat("x", 70000))
	if _, err := store.Put(owner, FileRef{Owner: "public", Name: "large.txt"}, data); err != nil {
		t.Fatal(err)
	}
	first, err := s.AdminDocuments(owner.User, "group", "public", "document-read", "large.txt", "", "", 0, 0)
	if err != nil || len(first.Data) != 64<<10 || first.Total != len(data) {
		t.Fatal(err, first.Total)
	}
	second, err := s.AdminDocuments(owner.User, "group", "public", "document-read", "large.txt", "", first.SHA256, first.NextOffset, 0)
	if err != nil || len(second.Data) != len(data)-(64<<10) {
		t.Fatal(err)
	}
	if _, err = s.AdminDocuments("wecom:alice", "group", "public", "document-read", "large.txt", "", "", 0, 0); err == nil {
		t.Fatal("ordinary user entered admin API")
	}
	preview, err := s.AdminDocuments(owner.User, "group", "public", "document-delete-preview", "large.txt", "", "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.Put(owner, FileRef{Owner: "public", Name: "large.txt"}, []byte("replacement"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Confirm(owner, pending.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdminDocuments(owner.User, "group", "public", "document-delete", "large.txt", preview.Pending.Token, "", 0, 0); !errors.Is(err, ErrChanged) {
		t.Fatal("stale delete accepted", err)
	}
}

func TestAdminTaskProjectionRedactsCredentialsAndOmitsReasoning(t *testing.T) {
	s, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	task, _, err := s.addTask(context.Background(), Identity{"group", "wecom:alice"})
	if err != nil {
		t.Fatal(err)
	}
	task.observe(core.Event{Type: core.EventThinking, Content: "PRIVATE_REASONING"})
	task.observe(core.Event{Type: core.EventToolUse, ToolName: "Bash", ToolInput: "run --secret=SECRET_VALUE"})
	task.observe(core.Event{Type: core.EventToolResult, ToolName: "Bash", ToolResult: "Bearer ACCESS_VALUE"})
	if _, err := s.AdminTasks("wecom:alice", "", task.code); err == nil {
		t.Fatal("non-owner read task audit")
	}
	items, err := s.AdminTasks("wecom:owner", "", task.code)
	if err != nil || len(items) != 1 || len(items[0].Events) != 3 {
		t.Fatal(items, err)
	}
	for _, event := range items[0].Events {
		if strings.Contains(event.Text, "SECRET_VALUE") || strings.Contains(event.Text, "ACCESS_VALUE") || strings.Contains(event.Text, "PRIVATE_REASONING") {
			t.Fatal("sensitive task event exposed")
		}
	}
	task.mu.Lock()
	task.completed = true
	task.mu.Unlock()
	s.finishTask(task)
	items, err = s.AdminTasks("wecom:owner", "", task.code)
	if err != nil || items[0].State != "completed" || items[0].CanStop {
		t.Fatal(items, err)
	}
}

func TestInstallStagesOnDestinationWithoutDependingOnWorkVolume(t *testing.T) {
	c := testControls(t, func(context.Context, string) (bool, error) { return true, nil })
	c.cfg.WorkDir = filepath.Join(t.TempDir(), "unavailable-work-volume")
	c.runInstall = func(_ context.Context, stage string, _ []string, _ map[string]string) error {
		if filepath.Dir(stage) != c.softwarePath {
			t.Fatal("staging escaped destination volume")
		}
		return os.WriteFile(filepath.Join(stage, "packages", "fixture.py"), []byte("fixture"), 0600)
	}
	if _, err := c.installSoftware(context.Background(), "python", "fixture==1.0"); err != nil {
		t.Fatal(err)
	}
}

func TestFinishedCleanupFailureStillBlocksDisableCompletionAndDeletion(t *testing.T) {
	s, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) {
		return "", errors.New("cleanup status unknown")
	})
	s.Handle(context.Background(), memberMessage("alice", "failed", "hi"))
	if _, _, err := s.addTask(context.Background(), Identity{"group", "wecom:alice"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed cleanup allowed more work", err)
	}
	if err := s.ApplyMember(context.Background(), "group", "alice", false, func() error { return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatal("finished failure was forgotten", err)
	}
	if _, err := s.PreviewMemberDelete(context.Background(), "wecom:owner", []Identity{{"group", "wecom:alice"}}); err == nil {
		t.Fatal("failed cleanup allowed deletion")
	}
}

func TestKnownTurnFailureDoesNotQuarantineConfirmedCleanup(t *testing.T) {
	if cleanupUncertain(&core.AgentFailure{Kind: core.AgentQuota, Cause: errors.New("quota")}) {
		t.Fatal("known turn failure treated as cleanup failure")
	}
	if !cleanupUncertain(errors.Join(&core.AgentFailure{Kind: core.AgentQuota}, &core.AgentFailure{Kind: core.AgentBoundary})) {
		t.Fatal("joined cleanup failure was hidden")
	}
}

func TestMemberDeletionDoesNotIgnoreUnattributedTemporaryCopies(t *testing.T) {
	s, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	if err := s.ApplyMember(context.Background(), "group", "alice", false, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	remnant := filepath.Join(s.cfg.WorkDir, "turn-unattributed")
	if err := os.Mkdir(remnant, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewMemberDelete(context.Background(), "wecom:owner", []Identity{{"group", "wecom:alice"}}); !errors.Is(err, ErrBusy) {
		t.Fatal("unattributed private cache was ignored", err)
	}
	if _, err := os.Stat(remnant); err != nil {
		t.Fatal("unknown cache was guessed and removed", err)
	}
}
