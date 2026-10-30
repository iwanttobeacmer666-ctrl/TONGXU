package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
	"github.com/chenhg5/cc-connect/groupassistant"
)

func TestInstalledLocalDefaultsReadOnlyAudit(t *testing.T) {
	path := os.Getenv("CC_GROUP_LOCAL_DEFAULTS_AUDIT")
	if path == "" {
		t.Skip("explicit read-only local configuration audit")
	}
	cfg, err := readConfig(path)
	if err != nil {
		t.Fatal("group configuration audit failed", err)
	}
	user, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	personal := os.Getenv("CODEX_HOME")
	if personal == "" {
		personal = filepath.Join(user, ".codex")
	}
	runner := &groupassistant.NativeRunner{ToolRoots: append([]string(nil), cfg.ToolRoots...)}
	if err := applyLocalCodexDefaults(cfg, runner, personal, user); err != nil {
		t.Fatal(err)
	}
	t.Logf("read-only audit: model default present=%t, effort default present=%t, added local skill roots=%d; no authentication files read", runner.Model != "", runner.Effort != "", len(runner.ToolRoots)-len(cfg.ToolRoots))
}

func TestInstalledNativeLocalSkillCatalog(t *testing.T) {
	command := os.Getenv("CC_GROUP_MXC_TEST_COMMAND")
	if command == "" {
		t.Skip("explicit installed CLI metadata check")
	}
	home, work := t.TempDir(), t.TempDir()
	// An empty dedicated home cannot import personal plugins/MCP/auth config.
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("cli_auth_credentials_store='file'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	agent, err := core.CreateAgent("codex", map[string]any{"cmd": command, "backend": "app_server", "work_dir": work, "codex_home": home})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Stop()
	provider, ok := agent.(core.SkillCatalogProvider)
	if !ok {
		t.Fatal("native skill catalog unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	skills, err := provider.ListSkills(ctx)
	if err != nil {
		t.Fatal("installed native skill metadata failed", err)
	}
	local := 0
	user, _ := os.UserHomeDir()
	root := filepath.Join(user, ".agents", "skills")
	for _, skill := range skills {
		if strings.HasPrefix(strings.ToLower(skill.Source), strings.ToLower(root)+string(filepath.Separator)) {
			local++
		}
	}
	if local == 0 {
		t.Fatal("native catalog did not discover user-local skills")
	}
	t.Logf("native catalog: %d user-local skills discovered with an empty dedicated home; no thread, model turn, login or personal config", local)
}

func TestLocalDefaultsImportOnlyScalarsAndReadOnlySkills(t *testing.T) {
	base := t.TempDir()
	user := filepath.Join(base, "user")
	personal := filepath.Join(user, ".codex")
	dedicated := filepath.Join(base, "group-home")
	skills := filepath.Join(user, ".agents", "skills")
	for _, dir := range []string{personal, dedicated, skills} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture := "model='local-model'\nmodel_reasoning_effort='high'\nprofile='selected'\n[profiles.selected]\nmodel='profile-model'\n[mcp_servers.private]\nurl='https://must-not-import.invalid'\n[plugins.private]\nenabled=true\n[model_providers.custom]\nenv_key='DO_NOT_IMPORT_KEY'\n"
	path := filepath.Join(personal, "config.toml")
	if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := settings{Config: groupassistant.Config{CodexHome: dedicated, WorkDir: filepath.Join(base, "work"), StateDir: filepath.Join(base, "state"), SoftwareDir: filepath.Join(base, "software")}, DataDir: filepath.Join(base, "files")}
	runner := &groupassistant.NativeRunner{}
	if err := applyLocalCodexDefaults(cfg, runner, personal, user); err != nil {
		t.Fatal(err)
	}
	if runner.Model != "profile-model" || runner.Effort != "high" || len(runner.ToolRoots) != 1 || runner.ToolRoots[0] != skills {
		t.Fatal("local defaults not projected", runner)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != fixture {
		t.Fatal("personal configuration changed")
	}
	if _, err := os.Stat(filepath.Join(dedicated, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("personal configuration copied")
	}
	if err := os.WriteFile(filepath.Join(dedicated, "config.toml"), []byte("model='dedicated-model'\nmodel_reasoning_effort='low'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner = &groupassistant.NativeRunner{}
	if err := applyLocalCodexDefaults(cfg, runner, personal, user); err != nil || runner.Model != "" || runner.Effort != "" {
		t.Fatal("dedicated defaults overwritten", err, runner)
	}
	// New read grants must not expose any protected member/state/auth root.
	cfg.StateDir = skills
	if err := applyLocalCodexDefaults(cfg, &groupassistant.NativeRunner{}, personal, user); err == nil {
		t.Fatal("protected root granted as local skills")
	}
}

func TestLocalDefaultsCustomProviderMissingAndMalformed(t *testing.T) {
	base := t.TempDir()
	personal, dedicated := filepath.Join(base, "personal"), filepath.Join(base, "dedicated")
	for _, dir := range []string{personal, dedicated} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := settings{Config: groupassistant.Config{CodexHome: dedicated}}
	path := filepath.Join(personal, "config.toml")
	for _, fixture := range []string{"model='private-provider-model'\nmodel_provider='custom'\n", "model='private-provider-model'\nprofile='custom'\n[profiles.custom]\nmodel_provider='custom'\n", "model='private-model'\nopenai_base_url='https://private.invalid'\n", "model='private-model'\n[model_providers.openai]\nbase_url='https://private.invalid'\n"} {
		if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
			t.Fatal(err)
		}
		runner := &groupassistant.NativeRunner{}
		if err := applyLocalCodexDefaults(cfg, runner, personal, base); err != nil || runner.Model != "" || runner.Effort != "" {
			t.Fatal("custom model crossed providers", err)
		}
	}
	if err := os.WriteFile(path, []byte("secret='SYNTHETIC_PRIVATE_VALUE'\nbroken=["), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := readLocalModelDefaults(path)
	if err == nil || strings.Contains(err.Error(), "SYNTHETIC_PRIVATE_VALUE") {
		t.Fatal("parser leaked source", err)
	}
	if _, err := readLocalModelDefaults(filepath.Join(base, "missing.toml")); err != nil {
		t.Fatal("missing optional defaults rejected", err)
	}
}
