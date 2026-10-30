package groupassistant

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	_ "github.com/chenhg5/cc-connect/agent/codex"
	"github.com/chenhg5/cc-connect/core"
)

func TestNativeBoundaryRemainsDisabledUntilAccepted(t *testing.T) {
	n := &NativeRunner{Command: filepath.Join(t.TempDir(), "codex.exe"), Home: t.TempDir(), WindowsSandbox: "elevated"}
	if e := n.Ready(); !errors.Is(e, ErrUnavailable) {
		t.Fatal("unverified boundary enabled", e)
	}
	n.Verified = true
	n.WindowsSandbox = "unelevated"
	if e := n.Ready(); !errors.Is(e, ErrUnavailable) {
		t.Fatal("weaker boundary enabled", e)
	}
}
func TestFailedNativeStartupQuarantinesRunner(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("bounded runner currently Windows-only")
	}
	n := &NativeRunner{Command: filepath.Join(t.TempDir(), "missing-codex.exe"), Home: t.TempDir(), WindowsSandbox: "mxc", Verified: true}
	snap := &Snapshot{Dir: t.TempDir(), Identity: Identity{"group", "wecom:alice"}, Approve: func(context.Context, core.Event) (bool, error) { return false, nil }}
	if _, e := n.Run(context.Background(), snap, "synthetic request"); e == nil {
		t.Fatal("missing executable started")
	}
	if !n.unhealthy.Load() || !errors.Is(n.Ready(), ErrUnavailable) {
		t.Fatal("failed native startup did not quarantine")
	}
}

func TestNativeGroupNeverInitializesLegacySystemSandbox(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native runner is Windows-only")
	}
	n := &NativeRunner{Command: filepath.Join(t.TempDir(), "codex.exe"), Home: t.TempDir(), WindowsSandbox: "mxc"}
	if !errors.Is(n.Ready(), ErrUnavailable) {
		t.Fatal("unverified MXC boundary enabled")
	}
	n.Verified = true
	if err := n.Ready(); err != nil {
		t.Fatal("accepted MXC configuration rejected", err)
	}
	n.WindowsSandbox = "elevated"
	if !errors.Is(n.Ready(), ErrUnavailable) {
		t.Fatal("legacy system-changing initialization allowed")
	}
	n.WindowsSandbox = "unknown"
	if !errors.Is(n.Ready(), ErrUnavailable) {
		t.Fatal("unknown sandbox implementation accepted")
	}
}
