package groupassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

// A local synthetic Responses endpoint drives the real installed native agent.
// No OpenAI/WeCom request, login, dependency download or system setting is used.
func TestNativeOwnerApprovalBeforeEachRealCommand(t *testing.T) {
	runSyntheticOwnerApproval(t, false)
}

func TestNativeOwnerPatchApprovalBeforeFileChange(t *testing.T) {
	runSyntheticOwnerApproval(t, true)
}

func runSyntheticOwnerApproval(t *testing.T, patchMode bool) {
	t.Helper()
	command := os.Getenv("CC_GROUP_MXC_TEST_COMMAND")
	if command == "" {
		t.Skip("opt into installed Codex/MXC with CC_GROUP_MXC_TEST_COMMAND")
	}
	// Only this test process bypasses proxy discovery for its own mock endpoint.
	t.Setenv("NO_PROXY", "127.0.0.1,localhost,::1")
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousLogger)
	work, home := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "mine"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, ".ccga-tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(work, "mine", "approved.txt")
	shellCommand := "cmd.exe /d /c \"echo approved>>mine\\approved.txt\""
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Logf("synthetic endpoint method=%s path=%s upgrade=%q", r.Method, r.URL.Path, r.Header.Get("Upgrade"))
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		var request struct {
			Input []map[string]any
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		for _, item := range request.Input {
			call, _ := item["call_id"].(string)
			if kind, _ := item["type"].(string); kind == "function_call_output" && strings.HasPrefix(call, "command-") {
				output := fmt.Sprint(item["output"])
				if len(output) > 2000 {
					output = output[:2000]
				}
				t.Logf("synthetic command result %s: %s", call, output)
			}
		}
		// Use the canonical exec_command call from upstream core test fixtures.
		// Some native models send tool schemas incrementally instead of "tools".
		count := calls.Add(1)
		t.Logf("synthetic endpoint request=%d path=%s patch=%t", count, r.URL.Path, patchMode)
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(event string, value any) {
			data, _ := json.Marshal(value)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		}
		emit("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": fmt.Sprint("synthetic-", count)}})
		if count <= 2 {
			args := map[string]any{"workdir": work, "cmd": shellCommand, "yield_time_ms": 10000, "login": false}
			raw, _ := json.Marshal(args)
			item := map[string]any{
				"type": "function_call", "name": "exec_command", "call_id": fmt.Sprint("command-", count), "arguments": string(raw),
			}
			if patchMode {
				patch := "*** Begin Patch\n*** Add File: mine/approved.txt\n+approved\n*** End Patch\n"
				if count == 2 {
					patch = "*** Begin Patch\n*** Update File: mine/approved.txt\n@@\n approved\n+approved\n*** End Patch\n"
				}
				item = map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": fmt.Sprint("patch-", count), "input": patch}
			}
			emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": item})
		} else {
			emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{
				"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": "synthetic task finished"}},
			}})
		}
		emit("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{
			"id":    fmt.Sprint("synthetic-", count),
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0, "input_tokens_details": nil, "output_tokens_details": nil},
		}})
	}))
	defer server.Close()
	provider := "{name=\"Synthetic approval test\",base_url=" + strconv.Quote(server.URL) + ",wire_api=\"responses\",requires_openai_auth=false}"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model_provider=\"cc_synthetic\"\nmodel_providers.cc_synthetic="+provider+"\n[features]\nsystem_proxy_fallback=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &NativeRunner{
		Command: command, Home: home, WindowsSandbox: "mxc", Verified: true, Admin: "wecom:owner",
	}
	if shell, err := exec.LookPath("pwsh.exe"); err == nil {
		runner.ToolRoots = []string{filepath.Dir(shell)}
	}
	broker := NewOperationApprovals("wecom:owner")
	codes := make(chan string, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	snapshot := &Snapshot{Dir: work, Identity: Identity{"group", "wecom:alice"}}
	snapshot.Approve = func(ctx context.Context, event core.Event) (bool, error) {
		if _, _, err := ReviewableNativeAction(event, work); err != nil {
			return false, err
		}
		return broker.Wait(ctx, snapshot.Identity, func(code string) error { codes <- code; return nil })
	}
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_, err := runner.Run(ctx, snapshot, "Run the synthetic test commands, then report completion.")
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("synthetic native agent did not stop")
		}
		if t.Failed() {
			diagnostics := os.Getenv("CC_GROUP_MXC_DIAGNOSTIC_DIR")
			if diagnostics != "" {
				if err := os.MkdirAll(diagnostics, 0700); err != nil {
					t.Error(err)
					return
				}
				for _, name := range []string{"logs_2.sqlite", "logs_2.sqlite-wal", "logs_2.sqlite-shm"} {
					data, err := os.ReadFile(filepath.Join(home, name))
					if err == nil {
						if err := os.WriteFile(filepath.Join(diagnostics, name), data, 0600); err != nil {
							t.Error(err)
						}
					}
				}
			}
		}
	})
	for i := 0; i < 2; i++ {
		select {
		case code := <-codes:
			data, err := os.ReadFile(target)
			if i == 0 && !os.IsNotExist(err) {
				t.Fatal("first command executed before owner approval", err)
			}
			if i == 1 && (err != nil || strings.Count(string(data), "approved") != 1) {
				t.Fatal("first approved command did not produce its expected output before the next approval", err)
			}
			if err := broker.Resolve(Identity{"group", "wecom:alice"}, code, true); err == nil {
				t.Fatal("member approved native command")
			}
			if err := broker.Resolve(Identity{"group", "wecom:owner"}, code, true); err != nil {
				t.Fatal(err)
			}
		case err := <-done:
			t.Fatal("native turn ended before both owner approvals:", err)
		case <-ctx.Done():
			t.Fatal("native approval timed out", ctx.Err(), "synthetic endpoint calls", calls.Load())
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || strings.Count(string(data), "approved") != 2 {
		t.Fatalf("approved commands did not both execute: %q %v", data, err)
	}
}
