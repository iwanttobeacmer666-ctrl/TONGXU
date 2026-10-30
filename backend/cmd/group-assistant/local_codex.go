package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/chenhg5/cc-connect/groupassistant"
)

// Decode only non-secret defaults. Provider, authentication, plugins, MCP,
// permissions, environment and hook configuration never reach the worker.
type localModelDefaults struct {
	Model     string `toml:"model"`
	Effort    string `toml:"model_reasoning_effort"`
	Provider  string `toml:"model_provider"`
	Endpoint  string `toml:"openai_base_url"`
	Profile   string `toml:"profile"`
	Providers map[string]struct {
		Endpoint string `toml:"base_url"`
	} `toml:"model_providers"`
	Profiles map[string]struct {
		Model    string `toml:"model"`
		Effort   string `toml:"model_reasoning_effort"`
		Provider string `toml:"model_provider"`
		Endpoint string `toml:"openai_base_url"`
	} `toml:"profiles"`
}

func readLocalModelDefaults(path string) (value localModelDefaults, err error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return value, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return value, fmt.Errorf("local Codex configuration is unavailable, linked or too large; no configuration imported")
	}
	f, err := os.Open(path)
	if err != nil {
		return value, fmt.Errorf("cannot read local Codex model defaults")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return value, fmt.Errorf("cannot read bounded local Codex model defaults")
	}
	if _, err := toml.Decode(string(data), &value); err != nil {
		// TOML parser errors can contain source excerpts, including secrets.
		return localModelDefaults{}, fmt.Errorf("local Codex configuration cannot be parsed; no configuration imported")
	}
	if value.Profile != "" {
		p, ok := value.Profiles[value.Profile]
		if !ok {
			return localModelDefaults{}, fmt.Errorf("selected local Codex profile is missing")
		}
		if p.Model != "" {
			value.Model = p.Model
		}
		if p.Effort != "" {
			value.Effort = p.Effort
		}
		if p.Provider != "" {
			value.Provider = p.Provider
		}
		if p.Endpoint != "" {
			value.Endpoint = p.Endpoint
		}
	}
	for _, item := range []string{value.Model, value.Effort} {
		if len(item) > 250 || item != strings.TrimSpace(item) || strings.ContainsAny(item, "\x00\r\n\t") {
			return localModelDefaults{}, fmt.Errorf("invalid local Codex model defaults")
		}
	}
	return value, nil
}

func (d localModelDefaults) usesDefaultProvider() bool {
	return (d.Provider == "" || d.Provider == "openai") && d.Endpoint == "" && d.Providers["openai"].Endpoint == ""
}

func applyLocalCodexDefaults(cfg settings, runner *groupassistant.NativeRunner, personalHome, userHome string) error {
	personal, err := readLocalModelDefaults(filepath.Join(personalHome, "config.toml"))
	if err != nil {
		return err
	}
	dedicated, err := readLocalModelDefaults(filepath.Join(cfg.CodexHome, "config.toml"))
	if err != nil {
		return err
	}
	// A model name alone cannot reproduce a custom provider. Never silently
	// copy its endpoint/credential setup, nor send that model to another provider.
	if personal.usesDefaultProvider() && dedicated.usesDefaultProvider() {
		if dedicated.Model == "" {
			runner.Model = personal.Model
		}
		if dedicated.Model == "" && dedicated.Effort == "" {
			runner.Effort = personal.Effort
		}
	} else {
		slog.Info("custom Codex provider configuration is not shared; dedicated Codex defaults retained")
	}
	// Native Codex discovers these user-local skills independently of CODEX_HOME.
	// Grant their helpers read access, not access to the personal Codex home.
	skills := filepath.Join(userHome, ".agents", "skills")
	info, err := os.Lstat(skills)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("local skill directory must be an existing unlinked directory")
	}
	skills, err = resolveCandidate(skills)
	if err != nil {
		return fmt.Errorf("cannot resolve local skill directory")
	}
	for _, protected := range []string{cfg.DataDir, cfg.WorkDir, cfg.StateDir, cfg.SoftwareDir, cfg.CodexHome, personalHome} {
		if protected != "" && overlaps(skills, protected) {
			return fmt.Errorf("local skills overlap protected data or credentials")
		}
	}
	for _, existing := range runner.ToolRoots {
		if strings.EqualFold(existing, skills) {
			return nil
		}
	}
	runner.ToolRoots = append(runner.ToolRoots, skills)
	return nil
}
