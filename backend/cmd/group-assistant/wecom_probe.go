package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/chenhg5/cc-connect/core"
)

// This bootstrap path never creates an agent or opens the persistent store.
// Authenticated group callbacks supply the IDs needed for the later allowlist.
func wecomProbeReply(msg *core.Message) string {
	if msg == nil {
		return ""
	}
	i18n := core.NewI18n(core.LangChinese)
	for _, id := range []string{msg.UserID, msg.ChannelID} {
		if strings.TrimSpace(id) == "" || len(id) > 256 || strings.ContainsAny(id, "\x00\r\n") {
			return i18n.T(core.MsgGroupProbeHelp)
		}
	}
	if msg.ChatName == "" || strings.TrimSpace(msg.Content) != "/whoami" {
		return i18n.T(core.MsgGroupProbeHelp)
	}
	return i18n.Tf(core.MsgGroupProbeIdentity, msg.UserID, msg.ChannelID)
}

func runWecomProbe() error {
	botID, secret := os.Getenv("CC_GROUP_BOT_ID"), os.Getenv("CC_GROUP_BOT_SECRET")
	if strings.TrimSpace(botID) == "" || strings.TrimSpace(secret) == "" {
		return fmt.Errorf("set CC_GROUP_BOT_ID and CC_GROUP_BOT_SECRET locally")
	}
	platform, e := core.CreatePlatform("wecom", map[string]any{"mode": "websocket", "bot_id": botID, "bot_secret": secret, "allow_from": "*"})
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return serveWecomProbe(ctx, platform)
}

func serveWecomProbe(ctx context.Context, platform core.Platform) error {
	var mu sync.Mutex
	var calls sync.WaitGroup
	accepting := true
	stopCalls := func() { mu.Lock(); accepting = false; mu.Unlock(); calls.Wait() }
	if e := platform.Start(func(p core.Platform, msg *core.Message) {
		mu.Lock()
		if !accepting || ctx.Err() != nil || msg == nil {
			mu.Unlock()
			return
		}
		calls.Add(1)
		mu.Unlock()
		defer calls.Done()
		if reply := wecomProbeReply(msg); reply != "" {
			if e := p.Reply(ctx, msg.ReplyCtx, reply); e != nil {
				slog.Warn("wecom connection test reply failed")
			}
		}
	}); e != nil {
		stopCalls()
		return e
	}
	slog.Info(core.NewI18n(core.LangChinese).T(core.MsgGroupProbeStarted))
	<-ctx.Done()
	mu.Lock()
	accepting = false
	mu.Unlock()
	e := platform.Stop()
	stopCalls()
	return e
}
