package wecom

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

type testMediaGuard struct {
	admit    func(context.Context, *core.Message, int) (*core.InboundMediaAdmission, error)
	failures atomic.Int32
}

func (g *testMediaGuard) AdmitMedia(ctx context.Context, msg *core.Message, n int) (*core.InboundMediaAdmission, error) {
	return g.admit(ctx, msg, n)
}
func (g *testMediaGuard) MediaFailure(error) string {
	g.failures.Add(1)
	return "attachment failed; resend"
}

func guardedMediaPlatform(t *testing.T, guard *testMediaGuard, handler func(context.Context, core.Platform, *core.Message)) *WSPlatform {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p := &WSPlatform{allowFrom: "*", ctx: ctx, cancel: cancel}
	p.SetInboundMediaGuard(guard, handler)
	t.Cleanup(func() { _ = p.Stop() })
	return p
}

func mediaBody(t *testing.T, urls ...string) wsMsgCallbackBody {
	t.Helper()
	items := []map[string]any{{"msgtype": "text", "text": map[string]string{"content": "compare these files"}}}
	for _, url := range urls {
		items = append(items, map[string]any{"msgtype": "file", "file": map[string]string{"url": url}})
	}
	data, err := json.Marshal(map[string]any{"msgid": "media", "chattype": "group", "chatid": "group", "from": map[string]string{"userid": "alice"}, "msgtype": "mixed", "mixed": map[string]any{"msg_item": items}})
	if err != nil {
		t.Fatal(err)
	}
	var body wsMsgCallbackBody
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestMediaGuardRejectsBeforeHTTPAndCanSkipWhoamiAttachments(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{false: "denied", true: "whoami"}[skip], func(t *testing.T) {
			var httpCalls, deliveries atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { httpCalls.Add(1) }))
			defer srv.Close()
			g := &testMediaGuard{admit: func(_ context.Context, msg *core.Message, n int) (*core.InboundMediaAdmission, error) {
				if n != 1 {
					t.Error("attachment count lost")
				}
				if skip {
					return nil, nil
				}
				return nil, errors.New("denied")
			}}
			p := guardedMediaPlatform(t, g, func(_ context.Context, _ core.Platform, msg *core.Message) {
				if len(msg.Files)+len(msg.Images) != 0 {
					t.Error("short command downloaded attachments")
				}
				deliveries.Add(1)
			})
			body := mediaBody(t, srv.URL)
			p.handleMsgCallback(wsCallbackFrame(t, "req", body))
			_ = p.Stop()
			if httpCalls.Load() != 0 {
				t.Fatal("HTTP started before admission")
			}
			if skip && deliveries.Load() != 1 || !skip && deliveries.Load() != 0 {
				t.Fatal("wrong delivery", deliveries.Load())
			}
		})
	}
}

func TestGuardedMediaAbortsWholeRequestOnFailureOrTotalLimit(t *testing.T) {
	for _, mode := range []string{"http-failure", "length-limit", "chunked-limit"} {
		t.Run(mode, func(t *testing.T) {
			var requests, deliveries atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/second" && mode == "http-failure" {
					http.Error(w, "failure", 500)
					return
				}
				if mode == "chunked-limit" {
					w.(http.Flusher).Flush()
				}
				_, _ = w.Write([]byte(strings.Repeat("x", 5)))
			}))
			defer srv.Close()
			done := make(chan struct{})
			g := &testMediaGuard{admit: func(ctx context.Context, _ *core.Message, _ int) (*core.InboundMediaAdmission, error) {
				return &core.InboundMediaAdmission{Context: ctx, MaxBytes: 8, Downloaded: func() {}, Close: func() { close(done) }}, nil
			}}
			p := guardedMediaPlatform(t, g, func(context.Context, core.Platform, *core.Message) { deliveries.Add(1) })
			body := mediaBody(t, srv.URL+"/first", srv.URL+"/second", srv.URL+"/third")
			p.handleMsgCallback(wsCallbackFrame(t, "req", body))
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not finish")
			}
			if deliveries.Load() != 0 || requests.Load() != 2 || g.failures.Load() != 1 {
				t.Fatal("partial request delivered or download continued", deliveries.Load(), requests.Load(), g.failures.Load())
			}
		})
	}
}

func TestGuardedMediaStopCancelsHTTPAndWaitsForReservation(t *testing.T) {
	entered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer srv.Close()
	var closed, deliveries atomic.Int32
	g := &testMediaGuard{admit: func(ctx context.Context, _ *core.Message, _ int) (*core.InboundMediaAdmission, error) {
		return &core.InboundMediaAdmission{Context: ctx, MaxBytes: 8, Downloaded: func() {}, Close: func() { closed.Add(1) }}, nil
	}}
	p := guardedMediaPlatform(t, g, func(context.Context, core.Platform, *core.Message) { deliveries.Add(1) })
	p.handleMsgCallback(wsCallbackFrame(t, "req", mediaBody(t, srv.URL)))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	stopDone := make(chan struct{})
	go func() { _ = p.Stop(); close(stopDone) }()
	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not cancel download")
	}
	if closed.Load() != 1 || deliveries.Load() != 0 {
		t.Fatal("shutdown did not clean reservation")
	}
}

func TestDownloadBudgetAllowsCBCPaddingButChecksDecryptedBytes(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	plain := []byte("12345678")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	padded := pkcs7PadBlock(plain, aes.BlockSize)
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key[:16]).CryptBlocks(encrypted, padded)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(encrypted) }))
	defer srv.Close()
	data, _, err := downloadWeComWSMedia(context.Background(), srv.URL, base64.StdEncoding.EncodeToString(key), 8)
	if err != nil || !bytes.Equal(data, plain) {
		t.Fatal("padding counted as document bytes", err)
	}
	if _, _, err := downloadWeComWSMedia(context.Background(), srv.URL, base64.StdEncoding.EncodeToString(key), 7); err == nil {
		t.Fatal("plaintext exceeded budget")
	}
}
