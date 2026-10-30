package groupassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestNativeMemberDeletionUsesControlOnlySession(t *testing.T) {
	command := os.Getenv("CC_GROUP_MXC_TEST_COMMAND")
	if command == "" {
		t.Skip("opt into installed native control handshake; no inference or real authentication")
	}
	work, home := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(work, ".ccga-tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	n := &NativeRunner{Command: command, Home: home, WindowsSandbox: "mxc", Verified: true, Admin: "wecom:owner"}
	opts, err := n.agentOptions(&Snapshot{Dir: work, Identity: Identity{"group", "wecom:alice"}})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := core.CreateAgent("codex", opts)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	session, err := agent.StartSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	thread := session.CurrentSessionID()
	if !validThreadID(thread) {
		t.Fatal("invalid created thread")
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	if err = n.DeleteThreads(ctx, work, []string{thread}); err != nil {
		t.Fatal("native metadata deletion failed", err)
	}
	if err = n.DeleteThreads(ctx, work, []string{thread}); err != nil {
		t.Fatal("retry of already deleted thread failed", err)
	}
	// Materialize a real native rollout using a local synthetic Responses
	// endpoint, then verify deletion and retry make no further model request.
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(event string, value any) {
			raw, _ := json.Marshal(value)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
		}
		emit("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": "delete-fixture"}})
		emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": "synthetic deletion fixture"}}}})
		emit("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"id": "delete-fixture", "usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}})
	}))
	defer server.Close()
	config := "model=\"gpt-5.3-codex\"\nmodel_provider=\"cc_synthetic\"\nmodel_providers.cc_synthetic={name=\"Synthetic deletion\",base_url=" + strconv.Quote(server.URL) + ",wire_api=\"responses\",requires_openai_auth=false}\n[features]\nsystem_proxy_fallback=false\n"
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NO_PROXY", "127.0.0.1,localhost,::1")
	if err = os.Mkdir(filepath.Join(work, "mine"), 0700); err != nil {
		t.Fatal(err)
	}
	var recorded Conversation
	snap := &Snapshot{Dir: work, Identity: Identity{"group", "wecom:alice"}, Approve: func(context.Context, core.Event) (bool, error) { return false, nil }, Report: func(v Conversation) error { recorded = v; return nil }}
	if _, err = n.Run(ctx, snap, "Run the local synthetic deletion fixture."); err != nil {
		t.Fatal(err)
	}
	if recorded.ThreadID == "" || calls.Load() == 0 {
		t.Fatal("materialized fixture did not run")
	}
	thread = recorded.ThreadID
	persisted := false
	if err = filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(info.Name(), "-"+thread+".jsonl") {
			persisted = true
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !persisted {
		t.Fatal("synthetic native thread did not persist a rollout")
	}
	before := calls.Load()
	if err = n.DeleteThreads(ctx, work, []string{thread}); err != nil {
		t.Fatal("persisted native deletion", err)
	}
	if err = n.DeleteThreads(ctx, work, []string{thread}); err != nil {
		t.Fatal("persisted deletion retry", err)
	}
	if calls.Load() != before {
		t.Fatal("deletion triggered inference")
	}
	if err = filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.Contains(info.Name(), thread) {
			t.Error("deleted native transcript remains")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("control-only test unexpectedly created authentication material")
	}
}
