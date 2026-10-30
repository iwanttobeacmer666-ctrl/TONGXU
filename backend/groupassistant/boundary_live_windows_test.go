package groupassistant

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
	"golang.org/x/sys/windows/registry"
)

func TestNativeOwnerApprovalSessionHandshake(t *testing.T) {
	command := os.Getenv("CC_GROUP_MXC_TEST_COMMAND")
	if command == "" {
		t.Skip("set CC_GROUP_MXC_TEST_COMMAND for the installed native handshake")
	}
	work, home := t.TempDir(), t.TempDir()
	temp := filepath.Join(work, ".ccga-tmp")
	if err := os.Mkdir(temp, 0700); err != nil {
		t.Fatal(err)
	}
	args, err := BoundaryArgs(work, filepath.Join(work, "public"), home, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	args = append(args, "-c", "windows.sandbox=\"mxc\"")
	agent, err := core.CreateAgent("codex", map[string]any{
		"cmd": append([]string{command}, args...), "backend": "app_server",
		"mode": "full-auto", "permission_profile": "cc_member", "approval_policy": "untrusted",
		"work_dir": work, "codex_home": home,
		"env": map[string]string{"TEMP": temp, "TMP": temp, "TMPDIR": temp},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := agent.StartSession(ctx, "")
	if err != nil {
		t.Fatal("native policy/reviewer handshake failed:", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal("native process-tree cleanup failed:", err)
	}
	t.Log("Native thread confirms named boundary, untrusted approvals and human reviewer; no turn/model request sent.")
}

// This opt-in acceptance test uses the installed Codex sandbox directly,
// without authenticating, connecting to WeCom, or making a model request.
func TestNativeMXCFileBoundary(t *testing.T) {
	runNativeMXCProbe(t)
}

func runNativeMXCProbe(t *testing.T) {
	t.Helper()
	command := os.Getenv("CC_GROUP_MXC_TEST_COMMAND")
	if command == "" {
		t.Skip("set CC_GROUP_MXC_TEST_COMMAND to opt into the installed MXC sandbox")
	}
	if !filepath.IsAbs(command) {
		t.Fatal("Codex command must be absolute")
	}
	// Verify that the environment key exists on the host before testing denied
	// write-handle acquisition. Neither host nor sandbox writes the registry.
	key, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE)
	if err != nil {
		t.Fatal("cannot inspect existing user environment key:", err)
	}
	key.Close()
	externalRoot := os.Getenv("CC_GROUP_MXC_EXTERNAL_TEMP")
	if !filepath.IsAbs(externalRoot) || strings.EqualFold(filepath.VolumeName(externalRoot), "D:") {
		t.Fatal("set CC_GROUP_MXC_EXTERNAL_TEMP to an existing temporary directory outside D:")
	}
	external, err := os.MkdirTemp(externalRoot, "ccga-mxc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(external); err != nil {
			t.Error("clean temporary external fixture:", err)
		}
	})
	base := t.TempDir()
	work := filepath.Join(base, "member.v1 with spaces")
	home := filepath.Join(base, "isolated-home")
	public := filepath.Join(work, "public")
	tool := filepath.Join(base, "readonly-tool.v2")
	fixtures := map[string]string{
		"Own":      filepath.Join(work, "mine.txt"),
		"Public":   filepath.Join(public, "public.txt"),
		"Foreign":  filepath.Join(base, "other-member", "other.txt"),
		"Store":    filepath.Join(base, "persistent-store", "original.txt"),
		"AuthDir":  filepath.Join(home, "fake-auth.txt"),
		"Tool":     filepath.Join(tool, "tool.txt"),
		"External": filepath.Join(external, "outside-my-share.txt"),
	}
	for _, name := range fixtures {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	temp := filepath.Join(work, ".ccga-tmp")
	if err := os.Mkdir(temp, 0700); err != nil {
		t.Fatal(err)
	}
	probe, err := os.ReadFile(filepath.Join("testdata", "boundary_probe.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	args, err := BoundaryArgs(work, public, home, []string{tool}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	args = append(args, "-c", "windows.sandbox=\"mxc\"", "sandbox",
		"--permission-profile", "cc_member", "-C", work, "--",
		filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
		"-NoProfile", "-NonInteractive", "-Command")
	// Run ordinary inline shell commands; do not change ExecutionPolicy.
	var parameters []string
	for _, key := range []string{"Own", "Public", "Foreign", "Store", "AuthDir", "Tool", "External"} {
		parameters = append(parameters, key+"='"+strings.ReplaceAll(fixtures[key], "'", "''")+"'")
	}
	args = append(args, "$taskProbeArgs=@{"+strings.Join(parameters, ";")+"}; & {\n"+string(probe)+"\n} @taskProbeArgs")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = work
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if key == "CODEX_HOME" || key == "TEMP" || key == "TMP" || key == "TMPDIR" ||
			strings.HasPrefix(key, "CC_GROUP_") || strings.Contains(key, "TOKEN") ||
			strings.Contains(key, "SECRET") || strings.Contains(key, "PASSWORD") || strings.Contains(key, "KEY") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "CODEX_HOME="+home, "TEMP="+temp, "TMP="+temp, "TMPDIR="+temp)
	output, err := cmd.CombinedOutput()
	t.Logf("Native MXC probe:\n%s", output)
	if err != nil {
		t.Fatal("native MXC boundary acceptance failed:", err)
	}
	if !strings.Contains(string(output), "CC_GROUP_BOUNDARY_OK") {
		t.Fatal("probe did not report successful completion")
	}
	for key, name := range fixtures {
		if key == "Own" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil || string(data) != "fixture" {
			t.Fatalf("%s fixture changed through sandbox: %q %v", key, data, err)
		}
	}
	if data, err := os.ReadFile(fixtures["Own"]); err != nil || string(data) != "updated" {
		t.Fatal("authorized write was not visible to host", err)
	}
}
