package groupassistant

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// No model, account, or service is used: only disposable sandbox fixtures.
func TestNativeMXCOwnFileDelete(t *testing.T) {
	command := os.Getenv("CC_GROUP_MXC_TEST_COMMAND")
	if command == "" {
		t.Skip("opt into installed native sandbox")
	}
	base := t.TempDir()
	work, home := filepath.Join(base, "work"), filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"mine", "public", ".ccga-tmp"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"mine/delete.txt", "public/keep.txt"} {
		if err := os.WriteFile(filepath.Join(work, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	args, err := BoundaryArgs(work, filepath.Join(work, "public"), home, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	probe := `$ErrorActionPreference='Stop'; try{Remove-Item -LiteralPath 'mine/delete.txt' -ErrorAction Stop}catch{Write-Output ('REMOVE_ITEM_FAILED: '+$_.Exception.Message)}; if(Test-Path -LiteralPath 'mine/delete.txt'){[IO.File]::Delete((Join-Path (Get-Location) 'mine/delete.txt'))}; if(Test-Path -LiteralPath 'mine/delete.txt'){throw 'own file remains'}; $denied=$false;try{[IO.File]::Delete((Join-Path (Get-Location) 'public/keep.txt'))}catch{$denied=$true};if(-not $denied){throw 'public delete escaped'}; Write-Output 'OWN_DELETE_OK'`
	args = append(args, "-c", `windows.sandbox="mxc"`, "sandbox", "--permission-profile", "cc_member", "-C", work, "--", filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-Command", probe)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = work
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if key == "CODEX_HOME" || key == "TEMP" || key == "TMP" || key == "TMPDIR" || strings.HasPrefix(key, "CC_GROUP_") || strings.Contains(key, "TOKEN") || strings.Contains(key, "SECRET") || strings.Contains(key, "PASSWORD") || strings.Contains(key, "KEY") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	tmp := filepath.Join(work, ".ccga-tmp")
	cmd.Env = append(cmd.Env, "CODEX_HOME="+home, "TEMP="+tmp, "TMP="+tmp, "TMPDIR="+tmp)
	out, err := cmd.CombinedOutput()
	t.Logf("delete probe: %s", out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "OWN_DELETE_OK") {
		t.Fatal("missing success marker")
	}
	if _, err := os.Stat(filepath.Join(work, "mine", "delete.txt")); !os.IsNotExist(err) {
		t.Fatalf("own delete not published to host: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(work, "public", "keep.txt")); err != nil || string(b) != "fixture" {
		t.Fatalf("public changed: %q %v", b, err)
	}
}
