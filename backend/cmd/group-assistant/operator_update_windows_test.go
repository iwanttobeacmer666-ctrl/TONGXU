package main

import (
	"context"
	"testing"
)

func TestUpdateStopRequiresPauseIdleDeliveryAndCurrentVersion(t *testing.T) {
	m, _ := extendedManager(t)
	ctx := context.Background()
	m.shutdown = func() { t.Fatal("must not stop before pipe ACK") }
	deliveryDone := false
	m.sealUpdate = func() bool { return deliveryDone }
	version := m.handle(ctx, manageRequest{Action: "list"}).Version
	send := func(action, v string) manageReply { return m.handle(ctx, manageRequest{Action: action, Version: v}) }
	if send("update-pause", "stale").Success {
		t.Fatal("stale pause accepted")
	}
	if send("update-stop", version).Success {
		t.Fatal("stop without pause accepted")
	}
	if !send("update-pause", version).Success {
		t.Fatal("pause rejected")
	}
	if got := m.handle(ctx, manageRequest{Action: "status"}); got.Status == nil || !got.Status.UpdatePaused {
		t.Fatal("new desktop cannot discover paused admission", got)
	}
	if got := send("update-stop", version); got.Success || got.Code != "busy" {
		t.Fatal("delivery interrupted", got)
	}
	if !send("update-resume", version).Success || m.service.UpdateReady() {
		t.Fatal("resume failed")
	}
	deliveryDone = true
	send("update-pause", version)
	if got := send("update-stop", version); !got.Success || got.CleanupReady || !m.stopping {
		t.Fatal("idle update stop rejected")
	}
	if send("update-resume", version).Success {
		t.Fatal("stopping service resumed")
	}
}
