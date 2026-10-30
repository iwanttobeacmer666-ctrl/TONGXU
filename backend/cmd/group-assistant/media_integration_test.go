package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
	"github.com/chenhg5/cc-connect/groupassistant"
	"github.com/gorilla/websocket"
)

type mediaIntegrationRunner struct{ calls atomic.Int32 }

func (r *mediaIntegrationRunner) Run(context.Context, *groupassistant.Snapshot, string) (string, error) {
	r.calls.Add(1)
	return "media delivered", nil
}

// Real platform + real service, with local fake transport/model boundaries.
func TestMediaDownloadRevocationAndSuccessfulHandoff(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blocked" {
			close(entered)
			<-r.Context().Done()
			close(canceled)
			return
		}
		w.Header().Set("Content-Disposition", "attachment; filename=sample.txt")
		_, _ = w.Write([]byte("sample"))
	}))
	defer media.Close()
	var writeMu sync.Mutex
	connected := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var frame struct {
				Cmd     string
				Headers map[string]string
			}
			if err := conn.ReadJSON(&frame); err != nil {
				return
			}
			if frame.Cmd == "aibot_subscribe" || frame.Cmd == "ping" {
				writeMu.Lock()
				err = conn.WriteJSON(map[string]any{"headers": frame.Headers, "errcode": 0})
				writeMu.Unlock()
				if err != nil {
					return
				}
				if frame.Cmd == "aibot_subscribe" {
					connected <- conn
				}
			}
		}
	}))
	defer wsServer.Close()
	store, err := groupassistant.NewStore(filepath.Join(t.TempDir(), "files"), "wecom:owner")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner := &mediaIntegrationRunner{}
	service, err := groupassistant.NewService(groupassistant.Config{Owner: "owner", Groups: []string{"group"}, Members: map[string][]string{"group": {"alice"}}, WorkDir: t.TempDir(), Language: "zh"}, store, runner)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	p, err := core.CreatePlatform("wecom", map[string]any{"mode": "websocket", "bot_id": "bot", "bot_secret": "local-fixture", "allow_from": "*", "ws_url": "ws" + wsServer.URL[4:]})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan groupassistant.Response, 1)
	handle := func(ctx context.Context, _ core.Platform, msg *core.Message) { results <- service.Handle(ctx, msg) }
	p.(core.InboundMediaConfigurer).SetInboundMediaGuard(service, handle)
	if err := p.Start(func(platform core.Platform, msg *core.Message) { handle(context.Background(), platform, msg) }); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	var conn *websocket.Conn
	select {
	case conn = <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("fake platform not connected")
	}
	send := func(id, url string) {
		t.Helper()
		writeMu.Lock()
		err := conn.WriteJSON(map[string]any{"cmd": "aibot_msg_callback", "headers": map[string]string{"req_id": id}, "body": map[string]any{
			"msgid": id, "chattype": "group", "chatid": "group", "from": map[string]string{"userid": "alice"},
			"msgtype": "file", "text": map[string]string{"content": "read this file"}, "file": map[string]string{"url": url},
		}})
		writeMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	send("blocked", media.URL+"/blocked")
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("attachment HTTP not started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.ApplyMember(ctx, "group", "alice", false, func() error { return nil }); err != nil {
		t.Fatal("revocation failed", err)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("revocation did not cancel HTTP")
	}
	if runner.calls.Load() != 0 {
		t.Fatal("revoked download reached runner")
	}
	files, err := store.List(groupassistant.Identity{Group: "group", User: "wecom:alice"}, "mine")
	if err != nil || len(files) != 0 {
		t.Fatal("partial request persisted attachments", files, err)
	}
	if err := service.ApplyMember(ctx, "group", "alice", true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	send("successful", media.URL+"/small")
	select {
	case response := <-results:
		if runner.calls.Load() != 1 || response.Text != "media delivered" {
			t.Fatal("handoff duplicated admission or failed", response, runner.calls.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("successful media did not reach service")
	}
	files, err = store.List(groupassistant.Identity{Group: "group", User: "wecom:alice"}, "mine")
	if err != nil || len(files) != 1 {
		t.Fatal("successful upload missing", files, err)
	}
}
