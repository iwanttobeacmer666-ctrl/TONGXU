package codex

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

type controlReplyWriter struct {
	s     *appServerSession
	reply func(string, map[string]any) any
}

func (w *controlReplyWriter) Close() error { return nil }
func (w *controlReplyWriter) Write(p []byte) (int, error) {
	var request struct {
		ID     any
		Method string
		Params map[string]any
	}
	if err := json.Unmarshal(p, &request); err != nil {
		return 0, err
	}
	result, _ := json.Marshal(w.reply(request.Method, request.Params))
	w.s.handleResponse(rpcResponseEnvelope{ID: request.ID, Result: result})
	return len(p), nil
}

func TestNativeControlCatalogPagingAndQuotaNullPrivacy(t *testing.T) {
	s := &appServerSession{ctx: context.Background()}
	pages := 0
	s.stdin = &controlReplyWriter{s: s, reply: func(method string, params map[string]any) any {
		switch method {
		case "model/list":
			pages++
			if params["includeHidden"] != false {
				t.Fatal(params)
			}
			if pages == 1 {
				return map[string]any{"data": []any{map[string]any{"id": "a", "model": "model-a", "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "future-effort"}}}}, "nextCursor": "next"}
			}
			if params["cursor"] != "next" {
				t.Fatal(params)
			}
			return map[string]any{"data": []any{map[string]any{"model": "hidden", "hidden": true}, map[string]any{"model": "model-b"}}, "nextCursor": nil}
		case "account/rateLimits/read":
			return map[string]any{"accountId": "PRIVATE", "email": "PRIVATE", "rateLimitsByLimitId": map[string]any{"codex": map[string]any{"limitId": "codex", "primary": map[string]any{"usedPercent": nil, "resetsAt": nil}, "secondary": map[string]any{"usedPercent": 75, "windowDurationMins": 300, "resetsAt": 1800000000}}}}
		}
		t.Fatal(method)
		return nil
	}}
	models, err := s.ListNativeModels(context.Background())
	if err != nil || len(models) != 2 || models[0].SupportedReasoningEfforts[0].ReasoningEffort != "future-effort" {
		t.Fatal(models, err)
	}
	quota, err := s.ReadNativeQuota(context.Background())
	if err != nil || len(quota) != 1 || len(quota[0].Windows) != 2 || quota[0].Windows[0].UsedPercent != nil || *quota[0].Windows[1].UsedPercent != 75 {
		t.Fatal(quota, err)
	}
	data, _ := json.Marshal(quota)
	if strings.Contains(string(data), "PRIVATE") {
		t.Fatal("account identity escaped quota projection")
	}
}

func TestResumeWorkspaceAndChoiceMustBeConfirmed(t *testing.T) {
	work := t.TempDir()
	old := t.TempDir()
	effort := "ultra"
	s := &appServerSession{permissionProfile: "member", workDir: work, model: "model-a", effort: effort}
	params := s.threadRequestParams()
	if params["cwd"] != work || params["config"].(map[string]any)["model_reasoning_effort"] != effort {
		t.Fatal(params)
	}
	for _, test := range []struct {
		cwd   string
		roots []string
		want  bool
	}{{work, []string{work}, true}, {old, []string{work}, false}, {work, []string{old}, false}, {work, []string{work, old}, false}, {work, nil, false}, {filepath.Join(work, "missing"), []string{work}, false}} {
		if err := s.verifyThreadWorkspace(test.cwd, test.roots); (err == nil) != test.want {
			t.Fatal(test, err)
		}
	}
	if err := s.verifyThreadChoice("model-a", &effort); err != nil {
		t.Fatal(err)
	}
	if s.verifyThreadChoice("other", &effort) == nil || s.verifyThreadChoice("model-a", nil) == nil {
		t.Fatal("selection silently changed")
	}
	future := "futureEffort"
	s.applyThreadRuntimeState(work, "model-a", &future)
	if s.GetReasoningEffort() != future {
		t.Fatal("native effort rewritten", s.GetReasoningEffort())
	}
}

func TestNativeCompactReportsCompletionOnlyOnCompletedItem(t *testing.T) {
	s := &appServerSession{events: make(chan core.Event, 8), currentTurn: "turn"}
	s.threadID.Store("thread")
	s.handleNotification("item/started", notificationProbe(t, map[string]any{"threadId": "thread", "turnId": "turn", "item": map[string]any{"type": "contextCompaction"}}))
	select {
	case e := <-s.events:
		t.Fatal("start marked complete", e)
	default:
	}
	s.handleNotification("item/completed", notificationProbe(t, map[string]any{"threadId": "thread", "turnId": "turn", "item": map[string]any{"type": "contextCompaction"}}))
	if e := <-s.events; e.ToolName != "ContextCompaction" {
		t.Fatal(e)
	}
	s.handleNotification("turn/completed", notificationProbe(t, map[string]any{"threadId": "thread", "turn": map[string]any{"id": "turn", "status": "interrupted"}}))
	if e := <-s.events; e.Type != core.EventError {
		t.Fatal("interrupted compaction reported success", e)
	}
}

func TestNativeProtocolFailuresUseCodesNotMessageKeywords(t *testing.T) {
	for _, test := range []struct {
		raw  string
		kind core.AgentFailureKind
	}{{`"usageLimitExceeded"`, core.AgentQuota}, {`"unauthorized"`, core.AgentLogin}, {`"serverOverloaded"`, core.AgentBusy}, {`{"httpConnectionFailed":{"httpStatusCode":503}}`, core.AgentBusy}, {`{"responseStreamDisconnected":{"httpStatusCode":null}}`, core.AgentConnection}, {`"sandboxError"`, core.AgentBoundary}, {`null`, core.AgentFailed}} {
		err := nativeProtocolFailure(json.RawMessage(test.raw), errors.New("busy login quota SECRET"))
		var typed *core.AgentFailure
		if !errors.As(err, &typed) || typed.Kind != test.kind || typed.BeforeSubmission {
			t.Fatal(test, err)
		}
	}
}

func TestNativeStrictCompletionDoesNotTrustIdleOrRetryWithoutCleanup(t *testing.T) {
	s := &appServerSession{approvalPolicy: "untrusted", events: make(chan core.Event, 8), currentTurn: "turn"}
	s.threadID.Store("thread")
	s.handleNotification("thread/status/changed", notificationProbe(t, map[string]any{"threadId": "thread", "status": map[string]any{"type": "idle"}}))
	select {
	case event := <-s.events:
		t.Fatal("idle alone completed task", event)
	default:
	}
	for _, failCleanup := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		session := &appServerSession{ctx: ctx, cancel: cancel, events: make(chan core.Event, 1)}
		if failCleanup {
			session.closeBoundary = func() error { return errors.New("cleanup failed") }
		}
		err := session.beforeSubmissionFailure(connectionFailure(errors.New("transport gone")))
		var failure *core.AgentFailure
		if !errors.As(err, &failure) || failure.BeforeSubmission == failCleanup || failCleanup && failure.Kind != core.AgentBoundary {
			t.Fatal(failCleanup, err)
		}
	}
}
