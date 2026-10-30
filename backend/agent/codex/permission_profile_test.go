package codex

import (
	"context"
	"encoding/json"
	"github.com/chenhg5/cc-connect/core"
	"strings"
	"testing"
	"time"
)

func TestOwnerReviewedPolicyCannotBeDowngradedAndMustBeConfirmed(t *testing.T) {
	for _, mode := range []string{"suggest", "auto-edit", "full-auto", "yolo"} {
		s := &appServerSession{mode: mode, permissionProfile: "member", approvalPolicy: "untrusted"}
		params := s.threadRequestParams()
		if params["approvalPolicy"] != "untrusted" || params["approvalsReviewer"] != "user" || params["permissions"] != "member" {
			t.Fatal("execution approval downgraded", mode, params)
		}
		if _, exists := params["sandbox"]; exists {
			t.Fatal("owner approval expanded sandbox", params)
		}
		for _, actual := range []json.RawMessage{nil, json.RawMessage("\"never\""), json.RawMessage("{}")} {
			if err := s.verifyApprovalPolicy(actual, "user"); err == nil {
				t.Fatal("unconfirmed approval policy accepted")
			}
		}
		if err := s.verifyApprovalPolicy(json.RawMessage("\"untrusted\""), "auto_review"); err == nil {
			t.Fatal("automatic reviewer replaced owner")
		}
		if err := s.verifyApprovalPolicy(json.RawMessage("\"untrusted\""), "user"); err != nil {
			t.Fatal(err)
		}
	}
	a := &Agent{permissionProfile: "member", approvalPolicy: "untrusted", backend: "app_server", mode: "full-auto"}
	a.SetMode("suggest")
	if a.WorkspaceAgentOptions()["approval_policy"] != "untrusted" {
		t.Fatal("workspace clone dropped owner execution review")
	}
}

func TestOwnerReviewedProcessDoesNotInheritPersonalCredentials(t *testing.T) {
	env := ownerReviewedBaseEnvironment([]string{
		"Path=tools", "SystemRoot=windows", "TEMP=task-temp", "NO_PROXY=localhost", "SystemDrive=C:",
		"OPENAI_API_KEY=synthetic", "CC_GROUP_BOT_SECRET=synthetic",
		"AZURE_OPENAI_CREDENTIAL=synthetic", "HTTPS_PROXY=synthetic",
		"CODEX_HOME=personal-home", "NODE_OPTIONS=synthetic",
	})
	if len(env) != 5 || env[0] != "Path=tools" || env[1] != "SystemRoot=windows" || env[2] != "TEMP=task-temp" || env[3] != "NO_PROXY=localhost" || env[4] != "SystemDrive=C:" {
		t.Fatal("unexpected host environment inherited", env)
	}
}

func TestBoundedCloseWaitsForReadersBeforeClosingEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &appServerSession{ctx: ctx, cancel: cancel, events: make(chan core.Event, 2), closeBoundary: func() error { return nil }}
	s.wg.Add(1)
	if e := s.Close(); e == nil {
		t.Fatal("incomplete bounded shutdown reported success")
	}
	select {
	case _, ok := <-s.events:
		if !ok {
			t.Fatal("events closed while reader can still emit")
		}
	default:
	}
	s.emit(core.Event{Type: core.EventText, Content: "late reader"})
	s.wg.Done()
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-s.events:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("events not closed after readers finished")
		}
	}
}

func TestPermissionProfileMustBeConfirmedByServer(t *testing.T) {
	s := &appServerSession{permissionProfile: "member"}
	for _, actual := range []*struct{ ID string }{nil, {ID: ":workspace"}, {ID: "other"}} {
		if e := s.verifyActiveProfile(actual); e == nil {
			t.Fatalf("unconfirmed boundary accepted: %v", actual)
		}
	}
	if e := s.verifyActiveProfile(&struct{ ID string }{ID: "member"}); e != nil {
		t.Fatal(e)
	}
	s.permissionProfile = ""
	if e := s.verifyActiveProfile(nil); e != nil {
		t.Fatal("legacy server without profile metadata rejected")
	}
}

func TestPermissionProfileKeepsNativeBoundaryAcrossModes(t *testing.T) {
	for _, mode := range []string{"suggest", "auto-edit", "full-auto", "yolo"} {
		s := &appServerSession{mode: mode, permissionProfile: "member-alice"}
		p := s.threadRequestParams()
		if p["permissions"] != "member-alice" {
			t.Fatalf("%s: profile lost: %v", mode, p)
		}
		if _, ok := p["sandbox"]; ok {
			t.Fatalf("%s: legacy sandbox overrides profile: %v", mode, p)
		}
	}
	s := &appServerSession{mode: "full-auto"}
	p := s.threadRequestParams()
	if p["sandbox"] != "workspace-write" {
		t.Fatalf("legacy behavior changed: %v", p)
	}
	if _, ok := p["permissions"]; ok {
		t.Fatal("unexpected named profile")
	}
}
func TestPermissionProfileRefusesExecFallback(t *testing.T) {
	_, err := New(map[string]any{"backend": "exec", "permission_profile": "member"})
	if err == nil || !strings.Contains(err.Error(), "requires backend=app_server") {
		t.Fatalf("got %v", err)
	}
}
func TestPermissionProfileCannotBeRemovedByModeSwitch(t *testing.T) {
	a := &Agent{permissionProfile: "member", backend: "app_server", mode: "full-auto"}
	a.SetMode("yolo")
	if a.GetMode() != "full-auto" {
		t.Fatal("accepted bypass mode")
	}
	opts := a.WorkspaceAgentOptions()
	if opts["permission_profile"] != "member" {
		t.Fatalf("workspace clone dropped boundary: %v", opts)
	}
}
