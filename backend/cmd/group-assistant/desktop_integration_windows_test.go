package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
	"github.com/chenhg5/cc-connect/groupassistant"
)

// Only compiled into tests. The desktop fixture invokes this same executable
// as its trusted broker; production binaries never contain the fixture mode.
func TestMain(m *testing.M) {
	for _, arg := range os.Args[1:] {
		if arg == "-manage-local" || arg == "-check" || arg == "-check-local-client" {
			if err := run(); err != nil {
				fmt.Fprintln(os.Stderr, "isolated broker rejected request")
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

type desktopFixtureRunner struct{ started chan struct{} }

func (*desktopFixtureRunner) Ready() error { return nil }
func (r *desktopFixtureRunner) Run(ctx context.Context, snap *groupassistant.Snapshot, _ string) (string, error) {
	snap.Progress(core.Event{Type: core.EventToolUse, ToolName: "shell", ToolInput: "python report.py"})
	for i := 0; i < 45; i++ {
		snap.Progress(core.Event{Type: core.EventToolResult, ToolName: "shell", ToolResult: fmt.Sprintf("本地合成步骤 %d\n", i) + strings.Repeat("可公开执行输出，未调用真实模型。\n", 20)})
	}
	snap.Progress(core.Event{Type: core.EventThinking, Content: "PRIVATE_REASONING"})
	snap.Progress(core.Event{Type: core.EventToolResult, ToolName: "shell", ToolResult: "secret=FAKE_SECRET_VALUE"})
	close(r.started)
	<-ctx.Done()
	return "", ctx.Err()
}

type desktopFixturePlatform struct{}

func (desktopFixturePlatform) ConnectionStatus() (string, time.Time) {
	return "connected", time.Now().UTC()
}

func TestDesktopFixtureService(t *testing.T) {
	root := os.Getenv("CC_DESKTOP_FIXTURE_ROOT")
	if root == "" {
		return
	}
	if !strings.Contains(root, "TestDesktopFrontendIsolatedIntegration") {
		t.Fatal("not isolated")
	}
	settingsPath := filepath.Join(root, "runtime", "operator-setup.json")
	_, _, owner, groups, err := readOperator(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := groupassistant.NewStore(filepath.Join(root, "WORK", "files"), "wecom:"+owner)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	put := func(user, name, content string) {
		t.Helper()
		_, err := store.Put(groupassistant.Identity{Group: "fixture-group", User: "wecom:" + owner}, groupassistant.FileRef{Owner: user, Name: name}, []byte(content))
		if err != nil {
			t.Fatal(err)
		}
	}
	put("wecom:alice", "alice.txt", "private alice")
	put("wecom:bob", "bob.txt", "private bob")
	put("public", "public-a.txt", "public")
	put("public", "large.txt", strings.Repeat("隔离测试文档\n", 20000))
	work := filepath.Join(root, "WORK", "tmp")
	if err := os.MkdirAll(filepath.Join(work, "turn-crash", "mine"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "turn-crash", "mine", "remnant.txt"), []byte("unattributed synthetic residue"), 0600); err != nil {
		t.Fatal(err)
	}
	ids, members := activeRoster(groups)
	runner := &desktopFixtureRunner{started: make(chan struct{})}
	svc, err := groupassistant.NewService(groupassistant.Config{Owner: owner, Groups: ids, Members: members, WorkDir: work, StateDir: filepath.Join(root, "WORK", "state")}, store, runner)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	release, err := operatorLock(filepath.Join(root, "runtime", "service.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	manager := &operatorManager{path: settingsPath, owner: owner, service: svc, platform: desktopFixturePlatform{}, shutdown: cancel, started: time.Now().UTC()}
	closePipe, err := startOperatorPipe(ctx, settingsPath, manager)
	if err != nil {
		t.Fatal(err)
	}
	defer closePipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Handle(ctx, &core.Message{UserID: "alice", ChannelID: "fixture-group", ChatName: "fixture-group", MessageID: "synthetic-task", Content: "run synthetic task"})
	}()
	select {
	case <-runner.started:
	case <-time.After(10 * time.Second):
		t.Fatal("fixture task not started")
	}
	fmt.Println("desktop-fixture-ready")
	<-ctx.Done()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("fixture task not cleaned")
	}
}

func TestDesktopFrontendIsolatedIntegration(t *testing.T) {
	gui := os.Getenv("CC_DESKTOP_EXE")
	if gui == "" {
		t.Skip("opt in to installed local desktop verification")
	}
	evidence := os.Getenv("CC_DESKTOP_EVIDENCE_DIR")
	if evidence == "" {
		t.Fatal("evidence directory required")
	}
	root := t.TempDir()
	for _, dir := range []string{"bin", "runtime", "WORK/files", "WORK/tmp", "WORK/software", "WORK/state", "runtime/codex-home"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	project := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, name := range []string{"Desktop-Operator.ps1", "Operator-Setup.ps1", "Operator-Paths.ps1", "Runtime-Paths.ps1"} {
		data, err := os.ReadFile(filepath.Join(project, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bin", "group-assistant.exe")
	from, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	to, err := os.Create(binary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(to, from); err != nil {
		t.Fatal(err)
	}
	if err = to.Close(); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{"DataDir": filepath.Join(root, "WORK", "files"), "WorkDir": filepath.Join(root, "WORK", "tmp"), "SoftwareDir": filepath.Join(root, "WORK", "software"), "StateDir": filepath.Join(root, "WORK", "state"), "CodexHome": filepath.Join(root, "runtime", "codex-home"), "CodexCommand": binary}
	base := map[string]any{"Owner": "desktop-fixture-owner", "Groups": []string{"fixture-group"}, "WindowsSandbox": "mxc", "BoundaryVerified": false, "ToolRoots": []string{}, "AllowedHosts": []string{}}
	for k, v := range paths {
		base[k] = v
	}
	data, _ := json.Marshal(base)
	if err = os.WriteFile(filepath.Join(root, "config.group-assistant.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"Version": 1, "Owner": "desktop-fixture-owner", "OwnerInitialized": true, "BotID": "fixture-bot-id", "ProtectedSecret": "NEVER-DECRYPTED-SYNTHETIC", "Paths": paths, "Groups": []groupRecord{{ID: "fixture-group", Enabled: true, Members: []memberRecord{{ID: "alice", Enabled: true}, {ID: "bob", Enabled: false, State: "disabled"}, {ID: "carol", Enabled: false, State: "cleanup_failed"}, {ID: strings.Repeat("long-user-id-", 14), Enabled: true}}}}}
	fixtureGroups := settings["Groups"].([]groupRecord)
	for i := 0; i < 1000; i++ {
		fixtureGroups[0].Members = append(fixtureGroups[0].Members, memberRecord{ID: fmt.Sprintf("member-%04d", i), Enabled: true})
	}
	settings["Groups"] = fixtureGroups
	data, _ = json.Marshal(settings)
	if err = os.WriteFile(filepath.Join(root, "runtime", "operator-setup.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ".desktop-test-fixture"), []byte("group-assistant-isolated-fixture-v1"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	server := exec.CommandContext(ctx, binary, "-test.run=^TestDesktopFixtureService$", "-test.timeout=160s")
	server.Env = append(os.Environ(), "CC_DESKTOP_FIXTURE_ROOT="+root)
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var serverErrors strings.Builder
	server.Stderr = &serverErrors
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if server.ProcessState == nil {
			server.Process.Kill()
			server.Wait()
		}
	}()
	scanner := bufio.NewScanner(stdout)
	ready := false
	for scanner.Scan() {
		if scanner.Text() == "desktop-fixture-ready" {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("fixture did not start", serverErrors.String())
	}
	go func() {
		for scanner.Scan() {
		}
	}()
	command := exec.CommandContext(ctx, gui, "--project", root, "--self-test", "--evidence", evidence)
	output, err := command.CombinedOutput()
	if err != nil {
		failure, _ := os.ReadFile(filepath.Join(evidence, "failure.txt"))
		t.Fatalf("desktop: %v %s %s", err, output, failure)
	}
	if err = server.Wait(); err != nil {
		t.Fatal("fixture server exit", err, serverErrors.String())
	}
	var alice, bob, public bool
	if err := filepath.WalkDir(filepath.Join(root, "WORK", "files"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch d.Name() {
		case "alice.txt":
			alice = true
		case "bob.txt":
			bob = true
		case "public-a.txt", "large.txt":
			public = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !alice || bob || public {
		t.Fatal("isolated deletion crossed scope or left selected files", alice, bob, public)
	}
	result, err := os.ReadFile(filepath.Join(evidence, "verification.json"))
	if err != nil || !strings.Contains(string(result), `"Success":true`) {
		t.Fatal("desktop evidence missing", err)
	}
}
