package groupassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

// Real native tools, but only a local synthetic model and in-memory downloads.
func TestNativeRoutineOfflineAndControlledTools(t *testing.T) {
	command := os.Getenv("CC_GROUP_MXC_TEST_COMMAND")
	if command == "" {
		t.Skip("opt into installed native sandbox")
	}
	t.Setenv("NO_PROXY", "127.0.0.1,localhost,::1")
	work, home := t.TempDir(), t.TempDir()
	for _, name := range []string{"mine", ".ccga-tmp"} {
		if err := os.Mkdir(filepath.Join(work, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	host, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	// Prove the listener is reachable before executing the isolated command.
	baseline, err := net.DialTimeout("tcp", host.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	baseline.Close()
	accepted, err := host.Accept()
	if err != nil {
		t.Fatal(err)
	}
	accepted.Close()
	var httpCalls, approvals, nativeActions, modelCalls atomic.Int32
	broker := NewOperationApprovals("wecom:owner")
	codes := make(chan string, 1)
	snap := &Snapshot{Dir: work, Identity: Identity{"group", "wecom:alice"}}
	cfg := Config{WorkDir: filepath.Dir(work), SoftwareDir: t.TempDir(), CodexHome: home, AllowedHosts: []string{"*"}}
	controls, err := NewControlTools(cfg, snap, func(ctx context.Context, summary string) (bool, error) {
		approvals.Add(1)
		if httpCalls.Load() != 1 || !strings.Contains(summary, "script.py") {
			return false, fmt.Errorf("download preceded approval")
		}
		return broker.Wait(ctx, snap.Identity, func(code string) error { codes <- code; return nil })
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controls.Close()
	controls.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		httpCalls.Add(1)
		body, kind := "%PDF-1.7\nfixture", "application/pdf"
		if strings.HasSuffix(r.URL.Path, ".py") {
			body = "print('fixture')"
			kind = "text/plain"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{kind}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	protected := filepath.Join(controls.softwarePath, "dependency.txt")
	if err := os.WriteFile(protected, []byte("readonly"), 0600); err != nil {
		t.Fatal(err)
	}
	snap.DynamicTools = controlToolSpecs()
	snap.DynamicHandler = controls.Handle
	snap.ReadOnlyRoots = []string{controls.softwarePath}
	snap.Approve = func(_ context.Context, e core.Event) (bool, error) {
		_, _, err := ReviewableNativeAction(e, work)
		nativeActions.Add(1)
		return err == nil, err
	}
	port := host.Addr().(*net.TCPAddr).Port
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	probe := "$ErrorActionPreference='Stop'; if([IO.File]::ReadAllText(" + quote(protected) + ") -ne 'readonly'){throw 'managed read failed'}; $denied=$false;try{[IO.File]::Delete(" + quote(protected) + ")}catch{$denied=$true};if(-not $denied){throw 'managed delete escaped'}; $socket=[Net.Sockets.TcpClient]::new();$connected=$false;try{$pending=$socket.ConnectAsync('127.0.0.1'," + strconv.Itoa(port) + ");$null=$pending.Wait(2500);$connected=$socket.Connected}catch{}finally{$socket.Dispose()};if($connected){throw 'host network escaped'};[IO.File]::WriteAllText('mine\\routine.txt','offline-and-readonly')"
	var toolResults atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		var input struct{ Input []map[string]any }
		_ = json.Unmarshal(body, &input)
		for _, item := range input.Input {
			if item["type"] == "function_call_output" {
				out := fmt.Sprint(item["output"])
				if strings.Contains(out, "Saved in working copy:") {
					toolResults.Add(1)
				}
				t.Logf("tool result: %s", clip(out, 1200))
			}
		}
		count := modelCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(event string, value any) {
			data, _ := json.Marshal(value)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		}
		emit("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": fmt.Sprint("routine-", count)}})
		var item map[string]any
		switch count {
		case 1, 2:
			name, purpose := "report.pdf", "document"
			if count == 2 {
				name = "script.py"
				purpose = "software"
			}
			raw, _ := json.Marshal(map[string]string{"url": "https://example.test/" + name, "target": name, "purpose": purpose})
			item = map[string]any{"type": "function_call", "name": "workspace_fetch", "call_id": fmt.Sprint("fetch-", count), "arguments": string(raw)}
		case 3:
			raw, _ := json.Marshal(map[string]any{"cmd": probe, "workdir": work, "login": false, "yield_time_ms": 10000})
			item = map[string]any{"type": "function_call", "name": "exec_command", "call_id": "routine-command", "arguments": string(raw)}
		default:
			item = map[string]any{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": "routine finished"}}}
		}
		emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": item})
		emit("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprint("routine-", count), "usage": map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}}})
	}))
	defer server.Close()
	config := "model_provider=\"cc_synthetic\"\nmodel_providers.cc_synthetic={name=\"Synthetic routine test\",base_url=" + strconv.Quote(server.URL) + ",wire_api=\"responses\",requires_openai_auth=false}\n[features]\nsystem_proxy_fallback=false\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &NativeRunner{Command: command, Home: home, WindowsSandbox: "mxc", Verified: true, AutomaticRoutine: true, Admin: "wecom:owner", Hosts: []string{"*"}}
	if shell, err := exec.LookPath("pwsh.exe"); err == nil {
		runner.ToolRoots = []string{filepath.Dir(shell)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := runner.Run(ctx, snap, "Execute the synthetic routine tools."); done <- err }()
	select {
	case code := <-codes:
		if httpCalls.Load() != 1 {
			t.Fatal("software fetched before owner approved")
		}
		if broker.Resolve(snap.Identity, code, true) == nil {
			t.Fatal("member approved software")
		}
		if err := broker.Resolve(Identity{"group", "wecom:owner"}, code, true); err != nil {
			t.Fatal(err)
		}
		if broker.Resolve(Identity{"group", "wecom:owner"}, code, true) == nil {
			t.Fatal("software approval replayed")
		}
	case err := <-done:
		t.Fatal("native turn ended before controlled approval", err)
	case <-ctx.Done():
		t.Fatal("no controlled approval", ctx.Err())
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if data, err := os.ReadFile(filepath.Join(work, "mine", "routine.txt")); err != nil || string(data) != "offline-and-readonly" {
		t.Fatal("routine network/file probe failed", string(data), err)
	}
	if data, err := os.ReadFile(protected); err != nil || string(data) != "readonly" {
		t.Fatal("managed dependency changed", string(data), err)
	}
	if approvals.Load() != 1 || httpCalls.Load() != 2 || nativeActions.Load() == 0 || toolResults.Load() < 2 {
		t.Fatal("tool flow incomplete", approvals.Load(), httpCalls.Load(), nativeActions.Load(), toolResults.Load())
	}
}

func TestNativeManagedInstallerUsesBoundedCommand(t *testing.T) {
	command := os.Getenv("CC_GROUP_MXC_TEST_COMMAND")
	if command == "" {
		t.Skip("opt into installed native sandbox")
	}
	c := testControls(t, func(context.Context, string) (bool, error) { return true, nil })
	c.cfg.CodexCommand = command
	if err := os.Mkdir(c.cfg.CodexHome, 0700); err != nil {
		t.Fatal(err)
	}
	shell, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Fatal(err)
	}
	c.cfg.ToolRoots = []string{filepath.Dir(shell)}
	stage := t.TempDir()
	if err := os.Mkdir(filepath.Join(stage, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Exercise the actual installer process wrapper without any package request.
	err = c.runInstaller(ctx, stage, []string{shell, "-NoProfile", "-NonInteractive", "-Command", "[IO.File]::WriteAllText('packages/fixture.txt','bounded installer')"})
	if err != nil {
		t.Fatal("native installer sandbox wrapper failed", err)
	}
	if data, err := os.ReadFile(filepath.Join(stage, "packages", "fixture.txt")); err != nil || string(data) != "bounded installer" {
		t.Fatal(string(data), err)
	}
}
