package main

import (
	"context"
	"testing"
	"time"
)

func TestDesktopStatusAndStopDoNotClaimCleanupOrExposeConfiguration(t *testing.T) {
	m, _ := extendedManager(t)
	ctx := context.Background()
	m.platform = desktopFixturePlatform{}
	m.started = time.Now().UTC()
	m.shutdown = func() { t.Error("handle must not stop before reply/ACK") }
	status := m.handle(ctx, manageRequest{Action: "status"})
	if !status.Success || status.Owner != "owner" || status.Status == nil || status.Status.Service != "running" || status.Status.Platform != "connected" || status.CleanupReady || len(status.Groups) != 0 {
		t.Fatal(status)
	}
	if got := m.handle(ctx, manageRequest{Action: "service-stop", Version: "old"}); got.Success {
		t.Fatal("stale stop accepted")
	}
	stopped := m.handle(ctx, manageRequest{Action: "service-stop", Version: status.Version})
	if !stopped.Success || stopped.CleanupReady || stopped.Status.Service != "stopping" {
		t.Fatal(stopped)
	}
	if got := m.handle(ctx, manageRequest{Action: "add", Version: status.Version, Group: "group", User: "new"}); got.Success {
		t.Fatal("write during shutdown accepted")
	}
	offline := (&operatorManager{path: m.path}).handle(ctx, manageRequest{Action: "status"})
	if !offline.Success || offline.Online || offline.Status.Service != "offline" {
		t.Fatal(offline)
	}
}
