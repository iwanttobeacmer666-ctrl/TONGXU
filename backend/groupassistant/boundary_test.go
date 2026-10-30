package groupassistant

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestBoundaryPreservesToolsButDeniesHostResources(t *testing.T) {
	w := t.TempDir()
	h := t.TempDir()
	args, e := BoundaryArgs(w, w+"/public", h, nil, []string{"example.com"}, false)
	if e != nil {
		t.Fatal(e)
	}
	all := strings.Join(args, "\n")
	for _, want := range []string{"\":root\"=\"deny\"", "\":minimal\"=\"read\"", "default_permissions=\"cc_member\"", "network_proxy=true", "\"example.com\"=\"allow\""} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %s in %s", want, all)
		}
	}
	for _, forbidden := range []string{"--disable", "shell_tool=false", "danger-full-access", "--sandbox", "\"*\"=\"allow\""} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("capability restriction or bypass: %s", all)
		}
	}
}

func TestBoundaryCLIOverridesPreserveQuotedPathsAndDomainNames(t *testing.T) {
	w := filepath.Join(t.TempDir(), "member.v1 with spaces")
	h := t.TempDir()
	public := filepath.Join(w, "public")
	tool := filepath.Join(t.TempDir(), "tool.v2")
	args, err := BoundaryArgs(w, public, h, []string{tool}, []string{"example.com"}, false)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{}
	for i := 0; i < len(args); i += 2 {
		key, value, ok := strings.Cut(args[i+1], "=")
		if !ok || strings.ContainsAny(key, "\"\\/:") {
			t.Fatalf("CLI dotted keys must not contain quoted path or domain segments: %s", args[i+1])
		}
		var parsed struct{ Value any }
		if _, err := toml.Decode("Value="+value, &parsed); err != nil {
			t.Fatal(err)
		}
		values[key] = parsed.Value
	}
	fs, ok := values["permissions.cc_member.filesystem"].(map[string]any)
	if !ok {
		t.Fatal("filesystem rules must be passed as a TOML table value")
	}
	for path, permission := range map[string]string{":root": "deny", ":minimal": "read", w: "write", public: "read", h: "deny", tool: "read"} {
		if fs[path] != permission {
			t.Fatalf("path %q: got %v, want %s", path, fs[path], permission)
		}
	}
	domains, ok := values["permissions.cc_member.network.domains"].(map[string]any)
	if !ok || domains["example.com"] != "allow" || len(domains) != 1 {
		t.Fatalf("domain name was split or quoted literally: %#v", domains)
	}
	projects, ok := values["projects"].(map[string]any)
	if !ok {
		t.Fatal("project paths must be passed as a TOML table value")
	}
	project, ok := projects[w].(map[string]any)
	if !ok || project["trust_level"] != "untrusted" {
		t.Fatal("workspace trust rule was lost")
	}
}
func TestBoundaryRejectsRelativeToolPathsAndBroadDomains(t *testing.T) {
	w := t.TempDir()
	h := t.TempDir()
	for _, host := range []string{"https://example.com", "example.com:443", "user@example.com", "*.example.com"} {
		if _, e := BoundaryArgs(w, w+"/public", h, nil, []string{host}, false); e == nil {
			t.Fatalf("accepted %s", host)
		}
	}
	if _, e := BoundaryArgs(w, w+"/public", h, []string{"relative"}, nil, false); e == nil {
		t.Fatal("relative tools root accepted")
	}
}

func TestBoundaryRequiresExplicitGlobalInternetGrant(t *testing.T) {
	work, home := t.TempDir(), t.TempDir()
	args, err := BoundaryArgs(work, filepath.Join(work, "public"), home, nil, []string{"*"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, "\n"), "network.domains={\"*\"=\"allow\"}") {
		t.Fatal("explicit network grant lost", args)
	}
}
