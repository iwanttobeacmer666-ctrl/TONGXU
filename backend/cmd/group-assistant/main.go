package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/chenhg5/cc-connect/agent/codex"
	"github.com/chenhg5/cc-connect/core"
	"github.com/chenhg5/cc-connect/groupassistant"
	_ "github.com/chenhg5/cc-connect/platform/wecom"
)

type settings struct {
	groupassistant.Config
	DataDir string
}

func readConfig(name string) (settings, error) {
	var cfg settings
	f, e := os.Open(name)
	if e != nil {
		return cfg, e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 64<<10))
	d.DisallowUnknownFields()
	if e = d.Decode(&cfg); e != nil {
		return cfg, e
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return cfg, fmt.Errorf("configuration must contain one JSON object")
	}
	if cfg.WindowsSandbox != "mxc" {
		return cfg, fmt.Errorf("group execution requires explicit MXC; legacy system-changing sandbox setup is not allowed")
	}
	if cfg.StateDir == "" {
		cfg.StateDir = filepath.Join(filepath.Dir(cfg.DataDir), "state")
	}
	if cfg.Timezone == "" {
		cfg.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return cfg, fmt.Errorf("invalid display timezone")
	}
	for _, p := range []string{cfg.DataDir, cfg.WorkDir, cfg.CodexHome, cfg.CodexCommand} {
		if !filepath.IsAbs(p) {
			return cfg, fmt.Errorf("configuration paths must be absolute")
		}
	}
	// Resolve existing links before comparing grants. A tool-directory alias
	// must not expose the persistent store through a second spelling.
	paths := []*string{&cfg.DataDir, &cfg.WorkDir, &cfg.CodexHome, &cfg.CodexCommand, &cfg.StateDir}
	for i := range cfg.ToolRoots {
		paths = append(paths, &cfg.ToolRoots[i])
	}
	if cfg.AutomaticRoutine {
		paths = append(paths, &cfg.SoftwareDir)
		for _, p := range []*string{&cfg.PythonCommand, &cfg.NodeCommand, &cfg.NpmCLI} {
			if *p != "" {
				paths = append(paths, p)
			}
		}
	}
	for _, p := range paths {
		resolved, e := resolveCandidate(*p)
		if e != nil {
			return cfg, e
		}
		*p = resolved
	}
	if strings.TrimSpace(cfg.Owner) == "" || len(cfg.Owner) > 250 || strings.ContainsAny(cfg.Owner, "\x00\r\n") || strings.HasPrefix(cfg.Owner, "YOUR_") || len(cfg.Groups) > 16 {
		return cfg, fmt.Errorf("configure actual owner and group IDs")
	}
	seenGroups := map[string]bool{}
	for _, g := range cfg.Groups {
		if strings.TrimSpace(g) == "" || len(g) > 256 || strings.ContainsAny(g, "\x00\r\n") || seenGroups[g] || strings.HasPrefix(g, "YOUR_") {
			return cfg, fmt.Errorf("configure actual group IDs")
		}
		seenGroups[g] = true
	}
	if err := groupassistant.ValidateMembers(cfg.Owner, cfg.Groups, cfg.Members); err != nil {
		return cfg, fmt.Errorf("invalid per-group member enrollment")
	}
	for _, pair := range [][2]string{{cfg.DataDir, cfg.WorkDir}, {cfg.DataDir, cfg.CodexHome}, {cfg.WorkDir, cfg.CodexHome}} {
		if overlaps(pair[0], pair[1]) {
			return cfg, fmt.Errorf("data, workspace, and Codex home directories must be separate")
		}
	}
	protected := []string{cfg.DataDir, cfg.WorkDir, cfg.CodexHome}
	for _, path := range protected {
		if overlaps(path, cfg.StateDir) {
			return cfg, fmt.Errorf("chat state must be separate from files, workspaces and credentials")
		}
	}
	protected = append(protected, cfg.StateDir)
	if cfg.AutomaticRoutine {
		for _, p := range protected {
			if overlaps(p, cfg.SoftwareDir) {
				return cfg, fmt.Errorf("managed software must be separate from files, temporary work and credentials")
			}
		}
		protected = append(protected, cfg.SoftwareDir)
		for _, command := range []string{cfg.PythonCommand, cfg.NodeCommand, cfg.NpmCLI} {
			if command == "" {
				continue
			}
			inside := false
			for _, tool := range cfg.ToolRoots {
				rel, err := filepath.Rel(tool, command)
				inside = inside || err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
			}
			if !inside {
				return cfg, fmt.Errorf("package tools must be within configured read-only tool roots")
			}
			if info, err := os.Stat(command); err != nil || !info.Mode().IsRegular() {
				return cfg, fmt.Errorf("configured package tool is unavailable")
			}
		}
	}
	for _, tool := range cfg.ToolRoots {
		for _, p := range protected {
			if overlaps(tool, p) {
				return cfg, fmt.Errorf("tool roots must not overlap member data, workspaces or Codex credentials")
			}
		}
	}
	if _, e := groupassistant.BoundaryArgs(cfg.WorkDir, filepath.Join(cfg.WorkDir, "public"), cfg.CodexHome, cfg.ToolRoots, cfg.AllowedHosts, false); e != nil {
		return cfg, fmt.Errorf("invalid boundary configuration: %w", e)
	}
	personalHome := os.Getenv("CODEX_HOME")
	if personalHome == "" {
		if h, e := os.UserHomeDir(); e == nil {
			personalHome = filepath.Join(h, ".codex")
		}
	}
	if personalHome != "" {
		personalHome, e = resolveCandidate(personalHome)
		if e != nil {
			return cfg, e
		}
	}
	if personalHome != "" && overlaps(personalHome, cfg.CodexHome) {
		return cfg, fmt.Errorf("use a separate Codex home for the group, outside the personal Codex home")
	}
	return cfg, nil
}

func resolveCandidate(name string) (string, error) {
	if !filepath.IsAbs(name) {
		return "", fmt.Errorf("configuration paths must be absolute")
	}
	name = filepath.Clean(name)
	var missing []string
	for {
		_, e := os.Lstat(name)
		if e == nil {
			real, e := filepath.EvalSymlinks(name)
			if e != nil {
				return "", e
			}
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return real, nil
		}
		if !os.IsNotExist(e) {
			return "", e
		}
		parent := filepath.Dir(name)
		if parent == name {
			return "", e
		}
		missing = append(missing, filepath.Base(name))
		name = parent
	}
}
func overlaps(a, b string) bool {
	inside := func(parent, child string) bool {
		r, e := filepath.Rel(parent, child)
		return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
	}
	return inside(a, b) || inside(b, a)
}
func run() error {
	configPath := flag.String("config", "config.group-assistant.json", "Group assistant JSON configuration")
	check := flag.Bool("check", false, "Validate configuration without connecting, executing commands or modifying runtime files")
	nativeCheck := flag.Bool("check-native", false, "Check native permission handshake in an isolated temporary home; no WeCom, login or model turn")
	probe := flag.Bool("probe-wecom", false, "Test WeCom connection and discover IDs without Codex, runtime configuration or workspace access")
	ownerTest := flag.Bool("acceptance-owner-only", false, "Run a controlled acceptance session for the configured owner only; does not enable general group work")
	operatorPath := flag.String("operator-settings", "", "Local operator settings for live membership management")
	manage := flag.Bool("manage-local", false, "Read one bounded local management request from stdin; never connects to WeCom")
	checkLocal := flag.Bool("check-local-client", false, "Check the existing trusted local Windows token boundary; no settings, login or platform access")
	flag.Parse()
	modeCount := 0
	for _, enabled := range []bool{*check, *nativeCheck, *probe, *ownerTest, *manage, *checkLocal} {
		if enabled {
			modeCount++
		}
	}
	if modeCount > 1 {
		return fmt.Errorf("choose one mode")
	}
	if *checkLocal {
		return operatorLocalTrusted()
	}
	if *manage {
		return runManagement(*operatorPath)
	}
	if *operatorPath != "" && !*check && !*probe && !*nativeCheck {
		if !filepath.IsAbs(*operatorPath) {
			return fmt.Errorf("operator path must be absolute")
		}
		release, err := operatorLock(filepath.Join(filepath.Dir(*operatorPath), "service.lock"))
		if err != nil {
			return fmt.Errorf("local service lock unavailable")
		}
		defer release()
	}
	if *probe {
		return runWecomProbe()
	}
	cfg, e := readConfig(*configPath)
	if e != nil {
		return e
	}
	if *ownerTest {
		cfg.OwnerOnly = true
	}
	runner := &groupassistant.NativeRunner{Command: cfg.CodexCommand, Home: cfg.CodexHome, WindowsSandbox: cfg.WindowsSandbox,
		Model: "", ToolRoots: cfg.ToolRoots, Hosts: cfg.AllowedHosts, Verified: cfg.BoundaryVerified || *ownerTest, Admin: "wecom:" + cfg.Owner, AutomaticRoutine: cfg.AutomaticRoutine}
	if _, e = os.Stat(cfg.CodexCommand); e != nil {
		return fmt.Errorf("Codex executable: %w", e)
	}
	if *check {
		fmt.Printf("Configuration validated. BoundaryVerified=%t is an operator flag; live boundary and platform acceptance tests remain required.\n", cfg.BoundaryVerified)
		return nil
	}
	if *nativeCheck {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if e := checkNativeCommand(ctx, cfg.CodexCommand, filepath.Dir(*configPath)); e != nil {
			return fmt.Errorf("native compatibility/permission handshake or cleanup failed; no group connection")
		}
		fmt.Println("Native permission handshake and cleanup confirmed; no group connection or model turn.")
		return nil
	}
	userHome, e := os.UserHomeDir()
	if e != nil {
		return fmt.Errorf("cannot resolve local user home")
	}
	personalHome := os.Getenv("CODEX_HOME")
	if personalHome == "" {
		personalHome = filepath.Join(userHome, ".codex")
	}
	personalHome, e = resolveCandidate(personalHome)
	if e != nil {
		return e
	}
	if e = applyLocalCodexDefaults(cfg, runner, personalHome, userHome); e != nil {
		return e
	}
	if e = runner.Ready(); e != nil {
		return e
	}
	if *ownerTest {
		slog.Info("owner-only acceptance mode; general group work remains disabled")
	}
	botID, secret := os.Getenv("CC_GROUP_BOT_ID"), os.Getenv("CC_GROUP_BOT_SECRET")
	if botID == "" || secret == "" {
		return fmt.Errorf("set CC_GROUP_BOT_ID and CC_GROUP_BOT_SECRET locally")
	}
	store, e := groupassistant.NewStore(cfg.DataDir, "wecom:"+cfg.Owner)
	if e != nil {
		return e
	}
	defer store.Close()
	// Reserve the owner slot before any group members register.
	for _, g := range cfg.Groups {
		if _, e = store.List(groupassistant.Identity{Group: g, User: "wecom:" + cfg.Owner}, "mine"); e != nil {
			return e
		}
	}
	service, e := groupassistant.NewService(cfg.Config, store, runner)
	if e != nil {
		return e
	}
	defer service.Close()
	p, e := core.CreatePlatform("wecom", map[string]any{"mode": "websocket", "bot_id": botID, "bot_secret": secret, "allow_from": "*"})
	if e != nil {
		return e
	}
	var approvalFollowups sync.Map
	service.SetApprovalNotifier(func(ctx context.Context, msg *core.Message, text string) error {
		approvalFollowups.Store(msg, true)
		return p.Send(ctx, msg.ReplyCtx, text)
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *operatorPath != "" {
		_, _, owner, _, err := readOperator(*operatorPath)
		if err != nil || owner != cfg.Owner {
			return fmt.Errorf("operator settings unavailable or owner mismatch")
		}
		manager := &operatorManager{path: *operatorPath, service: service, owner: cfg.Owner, shutdown: stop, started: time.Now().UTC()}
		if reporter, ok := p.(interface{ ConnectionStatus() (string, time.Time) }); ok {
			manager.platform = reporter
		}
		closePipe, err := startOperatorPipe(ctx, *operatorPath, manager)
		if err != nil {
			return fmt.Errorf("local management unavailable")
		}
		defer closePipe()
	}
	var calls sync.WaitGroup
	var callbackMu sync.Mutex
	accepting := true
	handleMessage := func(ctx context.Context, platform core.Platform, msg *core.Message) {
		callbackMu.Lock()
		if !accepting || msg == nil {
			callbackMu.Unlock()
			return
		}
		calls.Add(1)
		callbackMu.Unlock()
		defer calls.Done()
		response := service.Handle(ctx, msg)
		_, needsFollowup := approvalFollowups.LoadAndDelete(msg)
		if response.Text != "" {
			deliver := platform.Reply
			if needsFollowup || response.Followup {
				deliver = platform.Send
			}
			if e := deliver(ctx, msg.ReplyCtx, response.Text); e != nil {
				slog.Warn("group reply failed")
			}
		}
		sender, ok := platform.(core.FileSender)
		if !ok && len(response.Files) > 0 {
			slog.Warn("group platform cannot return generated files")
			return
		}
		for _, f := range response.Files {
			if e := sender.SendFile(ctx, msg.ReplyCtx, core.FileAttachment{FileName: filepath.Base(f.Ref.Name), Data: f.Data}); e != nil {
				slog.Warn("group file delivery failed")
			}
		}
	}
	mediaPlatform, ok := p.(core.InboundMediaConfigurer)
	if !ok {
		return fmt.Errorf("platform cannot enforce pre-download admission")
	}
	mediaPlatform.SetInboundMediaGuard(service, handleMessage)
	if e = p.Start(func(platform core.Platform, msg *core.Message) { handleMessage(ctx, platform, msg) }); e != nil {
		stop()
		callbackMu.Lock()
		accepting = false
		callbackMu.Unlock()
		calls.Wait()
		return e
	}
	<-ctx.Done()
	callbackMu.Lock()
	accepting = false
	callbackMu.Unlock()
	stopErr := p.Stop()
	calls.Wait()
	return stopErr
}
func main() {
	if e := run(); e != nil {
		slog.Error("group assistant stopped", "error", e)
		os.Exit(1)
	}
}
