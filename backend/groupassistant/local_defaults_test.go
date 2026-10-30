package groupassistant

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestLocalModelDefaultsYieldToGroupAndSkillGrantIsReadOnly(t *testing.T) {
	base := t.TempDir()
	skills := filepath.Join(base, "skills")
	runner := &NativeRunner{Command: filepath.Join(base, "codex.exe"), Home: filepath.Join(base, "home"), Model: "local-model", Effort: "high", ToolRoots: []string{skills}}
	for _, test := range []struct {
		settings      GroupSettings
		model, effort string
	}{
		{GroupSettings{}, "local-model", "high"},
		{GroupSettings{"group-model", "low"}, "group-model", "low"},
		{GroupSettings{"group-model", ""}, "group-model", ""},
	} {
		snap := &Snapshot{Dir: filepath.Join(base, "work"), Settings: test.settings}
		opts, err := runner.agentOptions(snap)
		if err != nil || opts["model"] != test.model || opts["reasoning_effort"] != test.effort {
			t.Fatal(opts, err)
		}
	}
	args, err := BoundaryArgs(filepath.Join(base, "work"), filepath.Join(base, "work", "public"), runner.Home, runner.ToolRoots, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(args); i += 2 {
		key, value, _ := strings.Cut(args[i+1], "=")
		if key != "permissions.cc_member.filesystem" {
			continue
		}
		var parsed struct{ Value map[string]string }
		if _, err := toml.Decode("Value="+value, &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Value[skills] != "read" || parsed.Value[runner.Home] != "deny" || parsed.Value[":root"] != "deny" {
			t.Fatal("skill grant broadened authority", parsed.Value)
		}
		return
	}
	t.Fatal("missing filesystem grant")
}
