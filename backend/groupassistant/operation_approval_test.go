package groupassistant

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

type approvalResult struct {
	allowed bool
	err     error
}

func TestOperationApprovalOnlyOwnerInOriginalGroupAndOnlyOnce(t *testing.T) {
	a := NewOperationApprovals("wecom:owner")
	code := make(chan string, 1)
	done := make(chan approvalResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		allowed, err := a.Wait(ctx, Identity{"group", "wecom:alice"}, func(token string) error { code <- token; return nil })
		done <- approvalResult{allowed, err}
	}()
	token := <-code
	for _, id := range []Identity{{"group", "wecom:alice"}, {"group", "wecom:bob"}, {"other", "wecom:owner"}} {
		if err := a.Resolve(id, token, true); !errors.Is(err, ErrDenied) {
			t.Fatal("another member/group approved execution", err)
		}
	}
	if err := a.Resolve(Identity{"group", "wecom:owner"}, token, true); err != nil {
		t.Fatal(err)
	}
	if result := <-done; !result.allowed || result.err != nil {
		t.Fatal(result)
	}
	if err := a.Resolve(Identity{"group", "wecom:owner"}, token, true); !errors.Is(err, ErrDenied) {
		t.Fatal("approval replay accepted", err)
	}
}

func TestOperationApprovalExpiryCancelRejectionAndDeliveryFailure(t *testing.T) {
	for _, scenario := range []string{"expired", "canceled", "rejected", "delivery-failed"} {
		t.Run(scenario, func(t *testing.T) {
			a := NewOperationApprovals("wecom:owner")
			now := time.Now()
			a.now = func() time.Time { return now }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			code := make(chan string, 1)
			done := make(chan approvalResult, 1)
			go func() {
				allowed, err := a.Wait(ctx, Identity{"group", "wecom:alice"}, func(token string) error {
					code <- token
					if scenario == "delivery-failed" {
						return ErrUnavailable
					}
					return nil
				})
				done <- approvalResult{allowed, err}
			}()
			token := <-code
			switch scenario {
			case "expired":
				now = now.Add(6 * time.Minute)
				if err := a.Resolve(Identity{"group", "wecom:owner"}, token, true); !errors.Is(err, ErrChanged) {
					t.Fatal("expired approval accepted", err)
				}
				cancel()
			case "canceled":
				cancel()
			case "rejected":
				if err := a.Resolve(Identity{"group", "wecom:owner"}, token, false); err != nil {
					t.Fatal(err)
				}
			}
			result := <-done
			if result.allowed {
				t.Fatal("unapproved command executed", result)
			}
			if scenario == "rejected" && result.err != nil {
				t.Fatal(result.err)
			}
			if scenario != "rejected" && result.err == nil {
				t.Fatal("failed approval reported success")
			}
			if err := a.Resolve(Identity{"group", "wecom:owner"}, token, true); err == nil {
				t.Fatal("completed/failed request remained approvable")
			}
		})
	}
}

func nativeCommandEvent(cwd string) core.Event {
	return core.Event{ToolName: "Bash", ToolInputRaw: map[string]any{"command": "python task.py", "cwd": cwd, "kind": "command"}}
}

func TestNativeCommandReviewRejectsBoundaryExpansion(t *testing.T) {
	workspace := t.TempDir()
	for _, scenario := range []string{"valid", "local", "outside-cwd", "missing-cwd", "extra-filesystem", "network-escalation", "remote", "stdin", "permission-tool", "missing-command"} {
		t.Run(scenario, func(t *testing.T) {
			event := nativeCommandEvent(workspace)
			switch scenario {
			case "local":
				event.ToolInputRaw["environmentId"] = "local"
			case "outside-cwd":
				event.ToolInputRaw["cwd"] = t.TempDir()
			case "missing-cwd":
				event.ToolInputRaw["cwd"] = filepath.Join(workspace, "missing")
			case "extra-filesystem":
				event.ToolInputRaw["additionalPermissions"] = map[string]any{"fileSystem": map[string]any{"write": []string{t.TempDir()}}}
			case "network-escalation":
				event.ToolInputRaw["networkApprovalContext"] = map[string]any{"host": "example.com"}
			case "remote":
				event.ToolInputRaw["environmentId"] = "other-machine"
			case "stdin":
				event.ToolInputRaw["kind"] = "writeStdin"
			case "permission-tool":
				event.ToolName = "Permissions"
			case "missing-command":
				delete(event.ToolInputRaw, "command")
			}
			command, _, err := ReviewableNativeCommand(event, workspace)
			if scenario == "valid" || scenario == "local" {
				if err != nil || command != "python task.py" {
					t.Fatal(command, err)
				}
			} else if !errors.Is(err, ErrDenied) {
				t.Fatal("boundary expansion was reviewable", scenario, err)
			}
		})
	}
}

func TestNativePatchApprovalIncludesDiffAndRejectsOutsideMoves(t *testing.T) {
	work := t.TempDir()
	for _, scenario := range []string{"inside-new-file", "outside-path", "outside-move", "expanded-root", "missing-diff", "unknown-kind", "missing-changes"} {
		t.Run(scenario, func(t *testing.T) {
			change := map[string]any{"path": filepath.Join(work, "mine", "new.py"), "diff": "+print('hello')", "kind": map[string]any{"type": "add"}}
			raw := map[string]any{"changes": []any{change}}
			switch scenario {
			case "outside-path":
				change["path"] = filepath.Join(t.TempDir(), "outside")
			case "outside-move":
				change["kind"] = map[string]any{"type": "update", "move_path": filepath.Join(t.TempDir(), "outside")}
			case "expanded-root":
				raw["grantRoot"] = t.TempDir()
			case "missing-diff":
				delete(change, "diff")
			case "unknown-kind":
				change["kind"] = map[string]any{"type": "unknown"}
			case "missing-changes":
				delete(raw, "changes")
			}
			summary, _, err := ReviewableNativeAction(core.Event{ToolName: "Patch", ToolInputRaw: raw}, work)
			if scenario == "inside-new-file" {
				if err != nil || !strings.Contains(summary, "print") {
					t.Fatal(summary, err)
				}
			} else if !errors.Is(err, ErrDenied) {
				t.Fatal("unsafe patch admitted", scenario, err)
			}
		})
	}
}

func TestGroupOwnerCanApproveWhileMemberTaskWaits(t *testing.T) {
	requests := make(chan string, 1)
	executed := false
	svc, _ := testService(t, func(ctx context.Context, snap *Snapshot, _ string) (string, error) {
		approved, err := snap.Approve(ctx, nativeCommandEvent(snap.Dir))
		if err != nil {
			return "", err
		}
		if approved {
			executed = true
		}
		return "finished", nil
	})
	svc.SetApprovalNotifier(func(_ context.Context, _ *core.Message, text string) error { requests <- text; return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan Response, 1)
	go func() { done <- svc.Handle(ctx, memberMessage("alice", "work", "please run the task")) }()
	var request string
	select {
	case request = <-requests:
	case response := <-done:
		t.Fatal("task ended before requesting approval", response.Text)
	case <-ctx.Done():
		t.Fatal("approval notification not delivered", ctx.Err())
	}
	fields := strings.Fields(request)
	var code string
	for i, field := range fields {
		if field == "/approve" && i+1 < len(fields) {
			code = fields[i+1]
		}
	}
	if code == "" {
		t.Fatal("no owner approval instructions", request)
	}
	for _, user := range []string{"alice", "bob"} {
		result := svc.Handle(ctx, memberMessage(user, user, "/approve "+code))
		if result.Text != svc.T(core.MsgGroupDenied) {
			t.Fatal("member approved their own command", result.Text)
		}
	}
	result := svc.Handle(ctx, memberMessage("owner", "owner-approve", "/approve "+code))
	if result.Text != svc.T(core.MsgGroupApprovalRecorded) {
		t.Fatal(result.Text)
	}
	select {
	case response := <-done:
		if response.Text != "finished" || !executed {
			t.Fatal("approved task did not resume", response)
		}
	case <-ctx.Done():
		t.Fatal("approved task did not finish", ctx.Err())
	}
}
