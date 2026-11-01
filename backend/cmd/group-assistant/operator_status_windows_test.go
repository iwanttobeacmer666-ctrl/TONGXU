package main

import (
	"context"
	"os"
	"path/filepath"
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

func TestDesktopStatusDoesNotRequireValidConfiguration(t *testing.T) {
	for _, state := range []string{"missing", "invalid", "owner-changed"} {
		for _, online := range []bool{false, true} {
			t.Run(state+map[bool]string{false: "/offline", true: "/online"}[online], func(t *testing.T) {
				m, _ := extendedManager(t)
				if !online {
					m.service = nil
				}
				m.path = filepath.Join(t.TempDir(), "operator-setup.json")
				var data []byte
				if state == "invalid" {
					data = []byte(`{"Owner":`)
				} else if state == "owner-changed" {
					data = []byte(`{"Owner":"changed","Groups":[{"ID":"group","Enabled":true,"Members":[]}]}`)
				}
				if data != nil {
					if err := os.WriteFile(m.path, data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				status := m.handle(context.Background(), manageRequest{Action: "status"})
				if !status.Success || status.Online != online || status.Status == nil || status.CleanupReady || len(status.Groups) != 0 || status.Version != "" {
					t.Fatalf("state query incorrectly depends on config: %+v", status)
				}
				if status.Owner != "owner" || online && status.Status.Service != "running" || !online && status.Status.Service != "offline" {
					t.Fatalf("wrong process state: %+v", status)
				}
				if stopped := m.handle(context.Background(), manageRequest{Action: "service-stop", Version: "untrusted"}); stopped.Success || m.stopping {
					t.Fatal("configuration checks bypassed for service-stop", stopped)
				}
				if data == nil {
					if _, err := os.Stat(m.path); !os.IsNotExist(err) {
						t.Fatal("status created settings", err)
					}
				} else if after, err := os.ReadFile(m.path); err != nil || string(after) != string(data) {
					t.Fatal("status changed settings", err)
				}
			})
		}
	}
}
