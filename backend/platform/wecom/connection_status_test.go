package wecom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
	"github.com/gorilla/websocket"
)

func TestConnectionStatusRequiresSubscribeAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	received, allow := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var request map[string]any
		if conn.ReadJSON(&request) != nil {
			return
		}
		close(received)
		select {
		case <-allow:
		case <-ctx.Done():
			return
		}
		if conn.WriteJSON(map[string]any{"headers": request["headers"], "errcode": 0}) != nil {
			return
		}
		<-ctx.Done()
	}))
	t.Cleanup(server.Close)
	t.Cleanup(cancel)
	platform, err := newWebSocket(map[string]any{"bot_id": "fixture", "bot_secret": "synthetic", "ws_url": "ws" + strings.TrimPrefix(server.URL, "http")})
	if err != nil {
		t.Fatal(err)
	}
	p := platform.(*WSPlatform)
	if state, _ := p.ConnectionStatus(); state != "stopped" {
		t.Fatal(state)
	}
	if err := p.Start(func(core.Platform, *core.Message) {}); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("no subscription")
	}
	if state, _ := p.ConnectionStatus(); state != "connecting" {
		t.Fatal("socket alone reported authenticated", state)
	}
	close(allow)
	deadline := time.Now().Add(3 * time.Second)
	for {
		state, at := p.ConnectionStatus()
		if state == "connected" {
			if at.IsZero() {
				t.Fatal("missing ack time")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.Stop()
	if state, _ := p.ConnectionStatus(); state != "stopped" {
		t.Fatal(state)
	}
}
