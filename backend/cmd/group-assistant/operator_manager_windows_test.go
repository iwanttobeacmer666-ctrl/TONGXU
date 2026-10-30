package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
	"github.com/chenhg5/cc-connect/groupassistant"
)

func operatorFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operator-setup.json")
	data := []byte(`{"Version":1,"Owner":"owner","BotID":"fixture","ProtectedSecret":"PRIVATE-FIXTURE","Paths":{"StateDir":"KEEP-PATH"},"Groups":[{"ID":"group","Enabled":true,"Members":[{"ID":"alice","Enabled":true}]},{"ID":"other","Enabled":true,"Members":[{"ID":"alice","Enabled":true}]}]}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

type managementRunner struct{}

func (managementRunner) Run(context.Context, *groupassistant.Snapshot, string) (string, error) {
	return "ran", nil
}

func TestOperatorPipeAppliesToLiveServiceWithoutRestart(t *testing.T) {
	path := operatorFixture(t)
	store, err := groupassistant.NewStore(t.TempDir(), "wecom:owner")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc, err := groupassistant.NewService(groupassistant.Config{Owner: "owner", Groups: []string{"group", "other"}, Members: map[string][]string{"group": {"alice"}, "other": {"alice"}}, WorkDir: t.TempDir()}, store, managementRunner{})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	manager := &operatorManager{path: path, owner: "owner", service: svc}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stop, err := startOperatorPipe(ctx, path, manager)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	list, err := operatorRemote(ctx, path, manageRequest{Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	added, err := operatorRemote(ctx, path, manageRequest{Action: "add", Version: list.Version, Group: "group", User: "bob"})
	if err != nil || !added.Success || !added.Online || !added.Applied {
		t.Fatal(added, err)
	}
	msg := func(mid string) *core.Message {
		return &core.Message{UserID: "bob", MessageID: mid, ChannelID: "group", ChatName: "group", Content: "hello"}
	}
	if got := svc.Handle(ctx, msg("enabled")); got.Text != "ran" {
		t.Fatal("new member not live", got)
	}
	disabled, err := operatorRemote(ctx, path, manageRequest{Action: "disable", Version: added.Version, Group: "group", User: "bob"})
	if err != nil || !disabled.Success || !disabled.CleanupReady {
		t.Fatal(disabled, err)
	}
	if got := svc.Handle(ctx, msg("disabled")); got.Text == "ran" {
		t.Fatal("disabled member still ran")
	}
	unknown, err := operatorRemote(ctx, path, manageRequest{Action: "stop", Group: "group", Task: "missing"})
	if err != nil || unknown.Success {
		t.Fatal("unknown stop reported success", unknown, err)
	}
}

func TestOperatorDetectsLegacyInstanceAndRefusesOffline(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestOperatorLegacyHelper$")
	child.Env = append(os.Environ(), "CC_OPERATOR_TEST_HOLD=1")
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	ready := make(chan bool, 1)
	go func() { scanner := bufio.NewScanner(output); ready <- scanner.Scan() }()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("child failed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child not ready")
	}
	if err := operatorOfflineSafe(); err == nil {
		t.Fatal("running instance treated as offline")
	}
}

func TestOperatorLegacyHelper(t *testing.T) {
	if os.Getenv("CC_OPERATOR_TEST_HOLD") != "1" {
		return
	}
	fmt.Println("ready")
	time.Sleep(10 * time.Second)
}

func TestOperatorManagerVersionPrivacyAndPersistence(t *testing.T) {
	if req, err := decodeManage(append([]byte{0xef, 0xbb, 0xbf}, []byte(`{"Action":"list"}`)...)); err != nil || req.Action != "list" {
		t.Fatal("UTF-8 stdin preamble rejected", err)
	}
	path := operatorFixture(t)
	m := operatorManager{path: path}
	list := m.handle(context.Background(), manageRequest{Action: "list"})
	if !list.Success || list.Online {
		t.Fatal(list)
	}
	encoded, _ := json.Marshal(list)
	if strings.Contains(string(encoded), "PRIVATE-FIXTURE") || strings.Contains(string(encoded), "KEEP-PATH") {
		t.Fatal("private fields exposed")
	}
	changed := m.handle(context.Background(), manageRequest{Action: "disable", Version: list.Version, Group: "group", User: "alice"})
	if !changed.Success || !changed.Applied || changed.Online {
		t.Fatal(changed)
	}
	if changed.Groups[0].Members[0].Enabled || !changed.Groups[1].Members[0].Enabled {
		t.Fatal("cross group update")
	}
	if got := m.handle(context.Background(), manageRequest{Action: "add", Version: list.Version, Group: "group", User: "bob"}); got.Success {
		t.Fatal("stale update accepted")
	}
	if got := m.handle(context.Background(), manageRequest{Action: "add", Version: changed.Version, Group: "group", User: "owner"}); got.Success {
		t.Fatal("owner change accepted")
	}
	raw, _, _, groups, err := readOperator(path)
	if err != nil || groups[0].Members[0].Enabled || string(raw["ProtectedSecret"]) != `"PRIVATE-FIXTURE"` {
		t.Fatal("persistence damaged", err)
	}
	for _, bad := range []string{`{"Action":"shell"}`, `{"Action":"list","Secret":"x"}`, `{} {}`} {
		req, err := decodeManage([]byte(bad))
		if err == nil && m.handle(context.Background(), req).Success {
			t.Fatal("unsupported input accepted", bad)
		}
	}
}

func TestOperatorPipeSameUserRoundTripAndSingleInstance(t *testing.T) {
	path := operatorFixture(t)
	m := &operatorManager{path: path}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	closePipe, err := startOperatorPipe(ctx, path, m)
	if err != nil {
		t.Fatal(err)
	}
	defer closePipe()
	if second, err := startOperatorPipe(ctx, path, m); err == nil {
		second()
		t.Fatal("second pipe instance accepted")
	}
	list, err := operatorRemote(ctx, path, manageRequest{Action: "list"})
	if err != nil || !list.Success {
		t.Fatal(list, err)
	}
	changed, err := operatorRemote(ctx, path, manageRequest{Action: "add", Group: "group", User: "bob", Version: list.Version})
	if err != nil || !changed.Success {
		t.Fatal(changed, err)
	}
}

func TestOperatorLocksRejectConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator.lock")
	release, err := operatorLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if second, err := operatorLock(path); err == nil {
		second()
		t.Fatal("concurrent writer acquired lock")
	}
}
