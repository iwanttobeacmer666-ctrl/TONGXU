package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhg5/cc-connect/groupassistant"
)

func TestConfigurationRejectsResourcesExposedThroughToolRoots(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(base, "personal"))
	good := settings{Config: groupassistant.Config{Owner: "owner", Groups: []string{"group"}, WorkDir: filepath.Join(base, "turns"), CodexHome: filepath.Join(base, "group-home"), CodexCommand: filepath.Join(base, "codex.exe"), WindowsSandbox: "mxc"}, DataDir: filepath.Join(base, "data")}
	for _, name := range []string{"valid", "parent-tool", "data-tool", "work-tool", "credential-tool", "personal-home", "overlap", "broad-domain", "duplicate-group", "invalid-owner", "duplicate-member", "unknown-member-group", "owner-in-members"} {
		t.Run(name, func(t *testing.T) {
			cfg := good
			switch name {
			case "parent-tool":
				cfg.ToolRoots = []string{base}
			case "data-tool":
				cfg.ToolRoots = []string{cfg.DataDir}
			case "work-tool":
				cfg.ToolRoots = []string{cfg.WorkDir}
			case "credential-tool":
				cfg.ToolRoots = []string{cfg.CodexHome}
			case "personal-home":
				cfg.CodexHome = filepath.Join(base, "personal", "nested")
			case "overlap":
				cfg.WorkDir = filepath.Join(cfg.DataDir, "turns")
			case "broad-domain":
				cfg.AllowedHosts = []string{"*.example.com"}
			case "duplicate-group":
				cfg.Groups = []string{"group", "group"}
			case "invalid-owner":
				cfg.Owner = "a\nb"
			case "duplicate-member":
				cfg.Members = map[string][]string{"group": {"alice", "alice"}}
			case "unknown-member-group":
				cfg.Members = map[string][]string{"other": {"alice"}}
			case "owner-in-members":
				cfg.Members = map[string][]string{"group": {"owner"}}
			}
			data, e := json.Marshal(cfg)
			if e != nil {
				t.Fatal(e)
			}
			configFile := filepath.Join(t.TempDir(), "config.json")
			if e = os.WriteFile(configFile, data, 0600); e != nil {
				t.Fatal(e)
			}
			parsed, e := readConfig(configFile)
			if name == "valid" {
				if e != nil || parsed.BoundaryVerified {
					t.Fatalf("valid disabled config rejected: %+v %v", parsed, e)
				}
			} else if e == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}

func TestAutomaticConfigurationProtectsSoftwareAndPackageTools(t *testing.T) {
	base := t.TempDir()
	tools := filepath.Join(base, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(tools, "python.exe")
	if err := os.WriteFile(command, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	good := settings{Config: groupassistant.Config{Owner: "owner", Groups: []string{"group"}, WorkDir: filepath.Join(base, "tmp"), CodexHome: filepath.Join(base, "home"), CodexCommand: filepath.Join(base, "codex.exe"), WindowsSandbox: "mxc", AutomaticRoutine: true, SoftwareDir: filepath.Join(base, "software"), ToolRoots: []string{tools}, PythonCommand: command}, DataDir: filepath.Join(base, "files")}
	for _, scenario := range []string{"valid", "missing-software", "software-data", "software-tool", "package-outside", "missing-command", "state-data", "state-tool", "state-software", "invalid-timezone"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := good
			switch scenario {
			case "missing-software":
				cfg.SoftwareDir = ""
			case "software-data":
				cfg.SoftwareDir = cfg.DataDir
			case "software-tool":
				cfg.SoftwareDir = tools
			case "package-outside":
				cfg.PythonCommand = filepath.Join(base, "codex.exe")
			case "missing-command":
				cfg.PythonCommand = filepath.Join(tools, "absent.exe")
			case "state-data":
				cfg.StateDir = cfg.DataDir
			case "state-tool":
				cfg.StateDir = tools
			case "state-software":
				cfg.StateDir = cfg.SoftwareDir
			case "invalid-timezone":
				cfg.Timezone = "Unknown/Timezone"
			}
			data, _ := json.Marshal(cfg)
			name := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(name, data, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := readConfig(name)
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}
