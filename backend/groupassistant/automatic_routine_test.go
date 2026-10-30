package groupassistant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestRoutineActionsNeedNoOwnerButKeepPublicationAuthority(t *testing.T) {
	for _, scenario := range []string{"explicit", "assistant", "quote", "history", "delete", "command-delete"} {
		t.Run(scenario, func(t *testing.T) {
			svc, store := testService(t, func(ctx context.Context, snap *Snapshot, prompt string) (string, error) {
				event := core.Event{ToolName: "Bash", ToolInputRaw: map[string]any{"command": "python hello.py", "cwd": snap.Dir}}
				if allowed, err := snap.Approve(ctx, event); err != nil || !allowed {
					t.Fatal("routine requires owner", allowed, err)
				}
				event.ToolInputRaw["additionalPermissions"] = map[string]any{"network": true}
				if allowed, _ := snap.Approve(ctx, event); allowed {
					t.Fatal("automatic approval expanded boundary")
				}
				if len(snap.DynamicTools) != 2 || snap.DynamicHandler == nil || len(snap.ReadOnlyRoots) != 1 {
					t.Fatal("controlled tools unavailable")
				}
				name := filepath.Join(snap.Dir, "mine", "hello.py")
				if scenario == "delete" {
					return "done", os.Remove(name)
				}
				return "done", os.WriteFile(name, []byte("after"), 0600)
			})
			svc.cfg.AutomaticRoutine = true
			svc.cfg.SoftwareDir = t.TempDir()
			id := Identity{"group", "wecom:alice"}
			putTestFile(t, store, id, "hello.py", "before")
			text := "整理文件"
			switch scenario {
			case "explicit":
				text = "修改 hello.py，输出‘你好’"
			case "delete":
				text = "删除 hello.py"
			case "command-delete":
				text = "/delete hello.py"
			case "quote":
				text = "解释这句话：删除 hello.py"
			case "history":
				svc.history["group"] = []string{"alice: 修改 hello.py"}
			}
			msg := memberMessage("alice", "routine", text)
			msg.ExtraContent = "修改 hello.py"
			response := svc.Handle(context.Background(), msg)
			data, err := store.Read(id, FileRef{Name: "hello.py"})
			switch scenario {
			case "explicit":
				if err != nil || string(data) != "after" || strings.Contains(response.Text, "/confirm ") {
					t.Fatal(response.Text, string(data), err)
				}
			case "delete", "command-delete":
				if err == nil {
					t.Fatal("explicit deletion not applied", response.Text)
				}
			default:
				if err != nil || string(data) != "before" || !strings.Contains(response.Text, "/confirm ") {
					t.Fatal("unrequested change applied", response.Text, string(data), err)
				}
			}
		})
	}
}

func TestRoutineSoftwareApprovalStillUsesOwnerMessageRoute(t *testing.T) {
	requests := make(chan string, 1)
	svc, _ := testService(t, func(ctx context.Context, snap *Snapshot, _ string) (string, error) {
		_, err := snap.DynamicHandler(ctx, "workspace_software", jsonArgs(map[string]string{"action": "install", "ecosystem": "python", "package": "fixture==1.0", "id": ""}))
		if err != ErrDenied {
			return "", err
		}
		return "software rejected", nil
	})
	svc.cfg.AutomaticRoutine = true
	svc.cfg.SoftwareDir = t.TempDir()
	svc.cfg.ToolRoots = []string{t.TempDir()}
	svc.cfg.PythonCommand = filepath.Join(svc.cfg.ToolRoots[0], "python.exe")
	svc.SetApprovalNotifier(func(_ context.Context, msg *core.Message, text string) error {
		if msg.UserID != "alice" || msg.ChannelID != "group" {
			t.Error("approval lost original actor/group")
		}
		requests <- text
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan Response, 1)
	go func() { done <- svc.Handle(ctx, memberMessage("alice", "install", "install fixture")) }()
	var notice string
	select {
	case notice = <-requests:
	case response := <-done:
		t.Fatal("software did not wait for owner", response.Text)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var code string
	fields := strings.Fields(notice)
	for i, field := range fields {
		if field == "/approve" && i+1 < len(fields) {
			code = fields[i+1]
		}
	}
	if code == "" {
		t.Fatal("missing owner instructions", notice)
	}
	if got := svc.Handle(ctx, memberMessage("alice", "self-approve", "/approve "+code)); got.Text != svc.T(core.MsgGroupDenied) {
		t.Fatal("member approved installation", got.Text)
	}
	if got := svc.Handle(ctx, memberMessage("owner", "reject", "/reject "+code)); got.Text != svc.T(core.MsgGroupApprovalRecorded) {
		t.Fatal(got.Text)
	}
	select {
	case response := <-done:
		if response.Text != "software rejected" {
			t.Fatal(response.Text)
		}
	case <-ctx.Done():
		t.Fatal("owner message could not resume task", ctx.Err())
	}
}

func TestRoutineDeleteDoesNotGrantOtherMemberAccess(t *testing.T) {
	svc, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	svc.cfg.AutomaticRoutine = true
	bob := Identity{"group", "wecom:bob"}
	putTestFile(t, store, bob, "secret.txt", "keep")
	public := FileRef{Owner: "public", Name: "shared.txt"}
	if pending, err := store.Put(Identity{"group", "wecom:owner"}, public, []byte("shared")); err != nil || pending != nil {
		t.Fatal(pending, err)
	}
	if result := svc.Handle(context.Background(), memberMessage("alice", "delete-public", "/delete public/shared.txt")); result.Text != svc.T(core.MsgGroupDenied) {
		t.Fatal("automatic deletion granted public write", result.Text)
	}
	if data, err := store.Read(bob, public); err != nil || string(data) != "shared" {
		t.Fatal(string(data), err)
	}
	svc.Handle(context.Background(), memberMessage("alice", "delete-other", "/delete bob/secret.txt"))
	if data, err := store.Read(bob, FileRef{Name: "secret.txt"}); err != nil || string(data) != "keep" {
		t.Fatal(string(data), err)
	}
}
