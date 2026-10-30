package groupassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestNativeContinuityRebindsWorkspaceToolsAndCompacts(t *testing.T) {
	command := os.Getenv("CC_GROUP_MXC_TEST_COMMAND")
	if command == "" {
		t.Skip("opt into installed native sandbox")
	}
	t.Setenv("NO_PROXY", "127.0.0.1,localhost,::1")
	home, first, second, third := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for _, work := range []string{first, second, third} {
		for _, name := range []string{"mine", ".ccga-tmp"} {
			if err := os.Mkdir(filepath.Join(work, name), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	oldFile := filepath.Join(first, "mine", "old.txt")
	if err := os.WriteFile(oldFile, []byte("old fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	var phase, step, modelCalls, compactCalls atomic.Int32
	phase.Store(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		t.Logf("synthetic phase=%d endpoint=%s", phase.Load(), r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/compact") {
			compactCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "compact-fixture", "object": "response.compaction", "created_at": 1800000000, "output": []map[string]any{{"type": "compaction", "id": "compact-item", "encrypted_content": "synthetic-opaque-compaction"}}, "usage": map[string]any{"input_tokens": 100, "output_tokens": 20, "total_tokens": 120}})
			return
		}
		var input struct{ Input []map[string]any }
		_ = json.Unmarshal(body, &input)
		for _, item := range input.Input {
			if item["type"] == "function_call_output" {
				t.Logf("synthetic result: %s", clip(fmt.Sprint(item["output"]), 900))
			}
		}
		count := step.Add(1)
		modelCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(event string, value any) {
			raw, _ := json.Marshal(value)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
		}
		responseID := fmt.Sprintf("continuity-%d-%d", phase.Load(), count)
		emit("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": responseID}})
		var item map[string]any
		if phase.Load() == 1 && count == 1 {
			raw, _ := json.Marshal(map[string]any{"cmd": "[IO.File]::WriteAllText('mine/first.txt','first turn')", "workdir": first, "login": false, "yield_time_ms": 10000})
			item = map[string]any{"type": "function_call", "name": "exec_command", "call_id": "first-command", "arguments": string(raw)}
		} else if phase.Load() == 2 && count == 1 {
			raw, _ := json.Marshal(map[string]string{"url": "https://example.test/", "target": "", "purpose": "preview"})
			item = map[string]any{"type": "function_call", "name": "workspace_fetch", "call_id": "resumed-fetch", "arguments": string(raw)}
		} else if phase.Load() == 2 && count == 2 {
			probe := "$ErrorActionPreference='Stop';$denied=$false;try{$null=[IO.File]::ReadAllText(" + quote(oldFile) + ")}catch{$denied=$true};if(-not $denied){throw 'old workspace escaped'};[IO.File]::WriteAllText('mine/second.txt','new workspace only')"
			raw, _ := json.Marshal(map[string]any{"cmd": probe, "workdir": second, "login": false, "yield_time_ms": 10000})
			item = map[string]any{"type": "function_call", "name": "exec_command", "call_id": "second-command", "arguments": string(raw)}
		} else {
			item = map[string]any{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": "synthetic continuity summary"}}}
		}
		emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": item})
		emit("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"id": responseID, "usage": map[string]any{"input_tokens": 100, "output_tokens": 20, "total_tokens": 120, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}})
	}))
	defer server.Close()
	config := "model=\"gpt-5.3-codex\"\nmodel_provider=\"cc_synthetic\"\nmodel_providers.cc_synthetic={name=\"Synthetic continuity\",base_url=" + strconv.Quote(server.URL) + ",wire_api=\"responses\",requires_openai_auth=false}\n[features]\nsystem_proxy_fallback=false\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &NativeRunner{Command: command, Home: home, WindowsSandbox: "mxc", Verified: true, AutomaticRoutine: true, Admin: "wecom:owner"}
	if shell, err := exec.LookPath("pwsh.exe"); err == nil {
		runner.ToolRoots = []string{filepath.Dir(shell)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	var last Conversation
	identity := Identity{"group", "wecom:alice"}
	makeSnap := func(work, effort string) *Snapshot {
		return &Snapshot{Dir: work, Identity: identity, ResumeID: last.ThreadID, Settings: GroupSettings{"gpt-5.3-codex", effort}, Approve: func(_ context.Context, e core.Event) (bool, error) {
			_, _, err := ReviewableNativeAction(e, work)
			return err == nil, err
		}, Report: func(value Conversation) error { last = value; return nil }}
	}
	firstSnap := makeSnap(first, "medium")
	firstSnap.DynamicTools = controlToolSpecs()
	firstSnap.DynamicHandler = func(context.Context, string, json.RawMessage) (string, error) {
		t.Error("old callback reused")
		return "", ErrDenied
	}
	if _, err := runner.Run(ctx, firstSnap, "Execute the first synthetic turn."); err != nil {
		t.Fatal(err)
	}
	thread := last.ThreadID
	if thread == "" || last.Context == nil || last.Context.UsedTokens <= 0 {
		t.Fatal("native context report missing", last)
	}
	phase.Store(2)
	step.Store(0)
	newCalls := 0
	secondSnap := makeSnap(second, "high")
	secondSnap.DynamicTools = controlToolSpecs()
	secondSnap.DynamicHandler = func(context.Context, string, json.RawMessage) (string, error) {
		newCalls++
		return "fresh actor-bound handler", nil
	}
	if _, err := runner.Run(ctx, secondSnap, "Continue in the new workspace; old paths are not authorized."); err != nil {
		t.Fatal(err)
	}
	if last.ThreadID != thread || last.Effort != "high" || newCalls != 1 {
		t.Fatal("resume changed identity/effort or failed to rebind tool", last, newCalls)
	}
	if data, err := os.ReadFile(filepath.Join(second, "mine", "second.txt")); err != nil || string(data) != "new workspace only" {
		t.Fatal("fresh workspace probe failed", string(data), err)
	}
	if data, err := os.ReadFile(oldFile); err != nil || string(data) != "old fixture" {
		t.Fatal(string(data), err)
	}
	phase.Store(3)
	step.Store(0)
	before := modelCalls.Load() + compactCalls.Load()
	compactSnap := makeSnap(third, "high")
	compactSnap.Compact = true
	if _, err := runner.Run(ctx, compactSnap, ""); err != nil {
		t.Fatal("native compaction failed", err)
	}
	if last.ThreadID != thread || modelCalls.Load()+compactCalls.Load() <= before {
		t.Fatal("compaction did not invoke native model service", last)
	}
}

func TestNativeControlInspectionUsesEphemeralDedicatedServer(t *testing.T) {
	command := os.Getenv("CC_GROUP_MXC_TEST_COMMAND")
	if command == "" {
		t.Skip("opt into installed native sandbox")
	}
	work, home := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(work, ".ccga-tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "No inference allowed", 500) }))
	defer server.Close()
	config := "model=\"gpt-5.3-codex\"\nmodel_provider=\"cc_synthetic\"\nmodel_providers.cc_synthetic={name=\"Synthetic inspection\",base_url=" + strconv.Quote(server.URL) + ",wire_api=\"responses\",requires_openai_auth=false}\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &NativeRunner{Command: command, Home: home, WindowsSandbox: "mxc", Verified: true, AutomaticRoutine: true}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	result, err := runner.Inspect(ctx, &Snapshot{Dir: work, Identity: Identity{"group", "wecom:alice"}, Settings: GroupSettings{"removed-group-selection", "unknown-effort"}}, "models")
	if err != nil || len(result.Models) == 0 || calls.Load() != 0 {
		t.Fatal("inspection invoked inference or failed", len(result.Models), calls.Load(), err)
	}
	if result.Current.Model != "removed-group-selection" {
		t.Fatal("lost displayed group selection", result.Current)
	}
	var rollouts int
	_ = filepath.WalkDir(home, func(name string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasPrefix(entry.Name(), "rollout-") {
			rollouts++
		}
		return nil
	})
	if rollouts != 0 {
		t.Fatal("inspection persisted unwanted conversation", rollouts)
	}
}
