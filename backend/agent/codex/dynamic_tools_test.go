package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestDynamicToolsRequireTrustedBoundedHandler(t *testing.T) {
	spec := core.DynamicTool{Type: "function", Name: "workspace_fetch", InputSchema: map[string]any{"type": "object"}}
	handler := core.DynamicToolHandler(func(context.Context, string, json.RawMessage) (string, error) { return "ok", nil })
	for _, scenario := range []string{"valid", "no-profile", "legacy", "no-handler", "duplicate", "wrong-type"} {
		t.Run(scenario, func(t *testing.T) {
			opts := map[string]any{"dynamic_tools": []core.DynamicTool{spec}, "dynamic_tool_handler": handler}
			backend, profile := "app_server", "cc_member"
			switch scenario {
			case "no-profile":
				profile = ""
			case "legacy":
				backend = "exec"
			case "no-handler":
				delete(opts, "dynamic_tool_handler")
			case "duplicate":
				opts["dynamic_tools"] = []core.DynamicTool{spec, spec}
			case "wrong-type":
				bad := spec
				bad.Type = "custom"
				opts["dynamic_tools"] = []core.DynamicTool{bad}
			}
			got, err := parseDynamicTools(opts, backend, profile)
			if scenario == "valid" {
				if err != nil || len(got.specs) != 1 {
					t.Fatal(got, err)
				}
			} else if err == nil {
				t.Fatal("unsafe registration accepted")
			}
		})
	}
}

func TestDynamicToolCallsAreSessionBoundSingleUseAndAsync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdin := &lockedWriteCloser{}
	started, release := make(chan struct{}, 1), make(chan struct{})
	count := 0
	s := &appServerSession{ctx: ctx, stdin: stdin, currentTurn: "turn"}
	s.threadID.Store("thread")
	s.tools = appServerTools{specs: []core.DynamicTool{{Type: "function", Name: "workspace_fetch", InputSchema: map[string]any{"type": "object"}}}, handler: func(_ context.Context, name string, args json.RawMessage) (string, error) {
		count++
		started <- struct{}{}
		<-release
		return "trusted result", nil
	}}
	if _, ok := s.threadRequestParams()["dynamicTools"]; !ok {
		t.Fatal("tool schemas not sent")
	}
	request := func(id, thread, turn, tool, call string, namespace any) {
		raw, _ := json.Marshal(map[string]any{"threadId": thread, "turnId": turn, "tool": tool, "callId": call, "namespace": namespace, "arguments": map[string]any{}})
		s.handleDynamicToolCall(json.RawMessage(`"`+id+`"`), raw)
	}
	request("good", "thread", "turn", "workspace_fetch", "once", nil)
	<-started
	// These replies must proceed while the owner callback is still waiting.
	request("replay", "thread", "turn", "workspace_fetch", "once", nil)
	request("child", "child", "turn", "workspace_fetch", "child", nil)
	request("stale", "thread", "old", "workspace_fetch", "old", nil)
	request("unknown", "thread", "turn", "unknown", "other", nil)
	request("namespace", "thread", "turn", "workspace_fetch", "ns", "foreign")
	if strings.Count(stdin.String(), `"success":false`) != 5 {
		t.Fatal(stdin.String())
	}
	close(release)
	s.wg.Wait()
	if count != 1 || !strings.Contains(stdin.String(), `"success":true`) {
		t.Fatal(count, stdin.String())
	}
}
