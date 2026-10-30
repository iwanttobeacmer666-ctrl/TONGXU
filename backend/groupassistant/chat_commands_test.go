package groupassistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

type chatRunner struct {
	run         runnerFunc
	inspection  NativeInspection
	inspectErr  error
	inspections int
}

func (r *chatRunner) Run(ctx context.Context, snap *Snapshot, prompt string) (string, error) {
	if r.run != nil {
		return r.run(ctx, snap, prompt)
	}
	return "finished", nil
}
func (r *chatRunner) Inspect(context.Context, *Snapshot, string) (NativeInspection, error) {
	r.inspections++
	return r.inspection, r.inspectErr
}
func chatFixture() NativeInspection {
	used, minutes, reset := 25, 300, int64(1800000000)
	return NativeInspection{Current: GroupSettings{"model-a", "medium"}, Models: []core.NativeModel{
		{ID: "a", Model: "model-a", DisplayName: "Model A", DefaultReasoningEffort: "medium", SupportedReasoningEfforts: []core.NativeReasoningEffort{{ReasoningEffort: "medium"}, {ReasoningEffort: "ultra"}}},
		{ID: "b", Model: "model-b", DisplayName: "Model B", DefaultReasoningEffort: "low", SupportedReasoningEfforts: []core.NativeReasoningEffort{{ReasoningEffort: "low"}}},
	}, Quota: []core.NativeQuota{{Name: "codex", Windows: []core.NativeQuotaWindow{{UsedPercent: &used, WindowDurationMins: &minutes, ResetsAt: &reset}, {UsedPercent: nil}}}}}
}

func TestChatCommandsOwnerSelectionQuotaAndUnknownNeverInvokeModel(t *testing.T) {
	svc, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) {
		t.Fatal("command entered inference")
		return "", nil
	})
	runner := &chatRunner{inspection: chatFixture()}
	svc.runner = runner
	call := func(user, text string) Response {
		return svc.Handle(context.Background(), memberMessage(user, fmt.Sprint(user, text, runner.inspections), text))
	}
	if got := call("alice", "/help").Text; !strings.Contains(got, "/compact") || !strings.Contains(got, "/model") {
		t.Fatal(got)
	}
	if got := call("alice", "/typo").Text; got != svc.T(core.MsgGroupUnknownCommand) {
		t.Fatal(got)
	}
	if got := call("alice", "/model").Text; !strings.Contains(got, "2. Model B") {
		t.Fatal(got)
	}
	if got := call("alice", "/model 2").Text; got != svc.T(core.MsgGroupDenied) {
		t.Fatal("member changed group model", got)
	}
	call("owner", "/model")
	call("owner", "/model 2")
	if got := svc.chat.Settings("group"); got != (GroupSettings{"model-b", "low"}) {
		t.Fatal(got)
	}
	// The synthetic server now confirms the selected group model.
	runner.inspection.Current = GroupSettings{"model-b", "low"}
	if got := call("owner", "/thinkng").Text; !strings.Contains(got, "1. low") {
		t.Fatal(got)
	}
	call("owner", "/thinking 1")
	if got := call("alice", "/token").Text; !strings.Contains(got, "75%") || !strings.Contains(got, "?") {
		t.Fatal("quota is not native percent/unknown", got)
	}
	if got := call("alice", "/token extra").Text; got != svc.T(core.MsgGroupInvalid) {
		t.Fatal(got)
	}
	call("owner", "/model")
	svc.mu.Lock()
	picker := svc.pickers[pickerKey(Identity{"group", "wecom:owner"}, "/model")]
	picker.At = time.Now().Add(-11 * time.Minute)
	svc.pickers[pickerKey(Identity{"group", "wecom:owner"}, "/model")] = picker
	svc.mu.Unlock()
	if got := call("owner", "/model 1").Text; !strings.Contains(got, "失效") {
		t.Fatal("expired menu changed model", got)
	}
}

func TestChatRestartResumesOnlyActorAndNewPreservesFilesAndPublicHistory(t *testing.T) {
	stateDir := t.TempDir()
	counter := 0
	runner := &chatRunner{}
	runner.run = func(_ context.Context, snap *Snapshot, prompt string) (string, error) {
		counter++
		if snap.ResumeID == "" {
			snap.ResumeID = fmt.Sprint("thread-", counter)
		}
		if !strings.Contains(prompt, snap.Dir) {
			t.Fatal("fresh workspace missing")
		}
		return "public reply", snap.Report(Conversation{Identity: snap.Identity, ThreadID: snap.ResumeID, Model: "model-a", Context: &core.ContextUsage{UsedTokens: 100, ContextWindow: 1000}, ObservedAt: time.Now().UTC()})
	}
	svc, store := testService(t, nilRunner())
	cfg := svc.cfg
	cfg.StateDir = stateDir
	svc, err := NewService(cfg, store, runner)
	if err != nil {
		t.Fatal(err)
	}
	alice, bob := Identity{"group", "wecom:alice"}, Identity{"group", "wecom:bob"}
	svc.Handle(context.Background(), memberMessage("alice", "a1", "hello"))
	svc.Handle(context.Background(), memberMessage("bob", "b1", "hello"))
	a, b := svc.chat.Conversation(alice), svc.chat.Conversation(bob)
	if a.ThreadID == "" || a.ThreadID == b.ThreadID {
		t.Fatal("actors shared a native thread", a, b)
	}
	if err := svc.chat.SetSettings("group", GroupSettings{"model-a", "ultra"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewService(cfg, store, runner)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if got := restarted.chat.Settings("group"); got.Effort != "ultra" || len(restarted.history["group"]) != 4 {
		t.Fatal("restart lost group state", got, restarted.history)
	}
	if got := restarted.Handle(context.Background(), memberMessage("alice", "ctx", "/context")).Text; !strings.Contains(got, "90.0%") {
		t.Fatal(got)
	}
	restarted.Handle(context.Background(), memberMessage("alice", "a2", "continue"))
	if got := restarted.chat.Conversation(alice).ThreadID; got != a.ThreadID {
		t.Fatal("same actor not resumed", got)
	}
	putTestFile(t, store, alice, "keep.txt", "keep")
	restarted.Handle(context.Background(), memberMessage("alice", "new", "/new"))
	if restarted.chat.Conversation(alice).ThreadID != "" || restarted.chat.Conversation(bob).ThreadID != b.ThreadID {
		t.Fatal("new affected another member")
	}
	if data, err := store.Read(alice, FileRef{Name: "keep.txt"}); err != nil || string(data) != "keep" || len(restarted.history["group"]) == 0 || restarted.chat.Settings("group").Effort != "ultra" {
		t.Fatal(string(data), err)
	}
	if err := restarted.chat.SaveConversation(alice, Conversation{Identity: alice, ThreadID: b.ThreadID}); !errors.Is(err, ErrDenied) {
		t.Fatal("thread rebound to another actor", err)
	}
}

func nilRunner() runnerFunc {
	return func(context.Context, *Snapshot, string) (string, error) { return "", nil }
}

func TestChatStateCorruptionFailsClosed(t *testing.T) {
	for _, raw := range []string{`{}`, `{"Version":1,"Settings":{},"Conversations":{},"History":{},"extra":true}`, `{} {}`} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "chat-state.json"), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if state, err := newChatState(dir); err == nil {
			state.Close()
			t.Fatal("invalid state silently reset", raw)
		}
	}
}

func TestNativeFailuresAreClassifiedWithoutLeakingRawError(t *testing.T) {
	svc, _ := testService(t, nilRunner())
	if svc.failure(&core.AgentFailure{Kind: core.AgentBusy}).Text == svc.failure(ErrBusy).Text {
		t.Fatal("server overload described as local queue full")
	}
	if got := svc.controlFailure(context.DeadlineExceeded).Text; got != svc.T(core.MsgGroupQueryTimeout) {
		t.Fatal("metadata query reported a five-minute job timeout", got)
	}
	for _, kind := range []core.AgentFailureKind{core.AgentConnection, core.AgentBusy, core.AgentLogin, core.AgentQuota, core.AgentBoundary, core.AgentFailed} {
		response := svc.failure(&core.AgentFailure{Kind: kind, Cause: errors.New("SECRET arbitrary upstream error")})
		if strings.Contains(response.Text, "SECRET") || response.Text == svc.T(core.MsgGroupFailed) {
			t.Fatal(kind, response.Text)
		}
	}
}

type startTestAgent struct {
	core.Agent
	calls   int
	failure error
}

func (a *startTestAgent) StartSession(context.Context, string) (core.AgentSession, error) {
	a.calls++
	if a.calls == 1 {
		return nil, a.failure
	}
	return nil, nil
}
func TestNativeRetryOnlyBeforeSubmissionAndAfterSafeCleanup(t *testing.T) {
	for _, test := range []struct {
		kind   core.AgentFailureKind
		before bool
		calls  int
	}{{core.AgentConnection, true, 2}, {core.AgentConnection, false, 1}, {core.AgentBoundary, true, 1}, {core.AgentBusy, false, 1}} {
		agent := &startTestAgent{failure: &core.AgentFailure{Kind: test.kind, BeforeSubmission: test.before}}
		_, err := startNativeSession(context.Background(), agent, "")
		if agent.calls != test.calls || (err == nil) != (test.calls == 2) {
			t.Fatal(test, agent.calls, err)
		}
	}
}

func TestCompactQueuesAndModelChangeAffectsOnlyNextStartedTask(t *testing.T) {
	started, release, compactStarted := make(chan GroupSettings, 1), make(chan struct{}), make(chan GroupSettings, 1)
	runner := &chatRunner{inspection: chatFixture()}
	runner.run = func(_ context.Context, snap *Snapshot, _ string) (string, error) {
		if snap.Compact {
			compactStarted <- snap.Settings
			return "", nil
		}
		started <- snap.Settings
		<-release
		return "finished", snap.Report(Conversation{Identity: snap.Identity, ThreadID: "thread-alice", Model: snap.Settings.Model})
	}
	svc, _ := testService(t, nilRunner())
	svc.runner = runner
	if err := svc.chat.SetSettings("group", GroupSettings{"model-a", "medium"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan Response, 1)
	go func() { done <- svc.Handle(ctx, memberMessage("owner", "work", "run")) }()
	select {
	case old := <-started:
		if old.Model != "model-a" {
			t.Fatal(old)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	svc.Handle(ctx, memberMessage("owner", "list", "/model"))
	svc.Handle(ctx, memberMessage("owner", "select", "/model 2"))
	compactDone := make(chan Response, 1)
	go func() { compactDone <- svc.Handle(ctx, memberMessage("owner", "compact", "/compact")) }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for len(svc.slots) < 2 {
		select {
		case <-deadline.C:
			t.Fatal("compact did not queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case <-compactStarted:
		t.Fatal("compact interrupted current work")
	default:
	}
	close(release)
	<-done
	select {
	case settings := <-compactStarted:
		if settings != (GroupSettings{"model-b", "low"}) {
			t.Fatal(settings)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if result := <-compactDone; !strings.Contains(result.Text, "压缩") {
		t.Fatal(result.Text)
	}
}
