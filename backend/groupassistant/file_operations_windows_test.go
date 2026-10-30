package groupassistant

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestFileOperationWindowsPartialFailureReportsAndRetriesRemainingFiles(t *testing.T) {
	s, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	id := Identity{"group", "wecom:alice"}
	putTestFile(t, store, id, "locked.txt", "locked")
	putTestFile(t, store, id, "removable.txt", "removable")
	p := previewFiles(t, s, id.User, FileClearDocuments, nil, 0)
	file := filepath.Join(store.root.Name(), filepath.FromSlash(memberBase(id)), "locked.txt")
	name, err := windows.UTF16PtrFromString(file)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if h != windows.InvalidHandle {
			windows.CloseHandle(h)
		}
	}()
	result, err := confirmFiles(s, p)
	if err == nil || result.Complete || result.Deleted != 1 || result.Failed != 1 || result.Remaining != 1 {
		t.Fatal(result, err)
	}
	if len(result.Items) != 2 || result.Items[0].Name != "locked.txt" || result.Items[0].Status != "failed" || result.Items[1].Status != "deleted" {
		t.Fatal(result.Items)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("locked file incorrectly reported", err)
	}
	if err := windows.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
	h = windows.InvalidHandle
	p = previewFiles(t, s, id.User, FileClearDocuments, nil, 0)
	if result, err := confirmFiles(s, p); err != nil || !result.Complete || result.Deleted != 1 {
		t.Fatal(result, err)
	}
}

func TestFileOperationWindowsRejectsJunctionsWithoutFollowingTargets(t *testing.T) {
	s, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	id := Identity{"group", "wecom:alice"}
	putTestFile(t, store, id, "seed.txt", "durable")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	junction := func(alias, target string) {
		t.Helper()
		shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		command := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path $env:CC_FILE_TEST_ALIAS -Target $env:CC_FILE_TEST_TARGET | Out-Null")
		command.Env = append(os.Environ(), "CC_FILE_TEST_ALIAS="+alias, "CC_FILE_TEST_TARGET="+target)
		command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("fixture junction creation failed: %v: %s", err, output)
		}
	}
	junction(filepath.Join(s.cfg.WorkDir, "link"), outside)
	view, err := s.TemporaryFiles(context.Background(), store.admin, 0, 100)
	if err != nil || len(view.Items) != 1 || !view.Items[0].Blocked {
		t.Fatal(view, err)
	}
	if _, err := s.PreviewFileOperation(context.Background(), store.admin, "", "", FileClearTemporary, "v1", nil, 0); !errors.Is(err, ErrDenied) {
		t.Fatal("temporary junction followed", err)
	}
	aliceRoot := filepath.Join(store.root.Name(), filepath.FromSlash(memberBase(id)))
	junction(filepath.Join(aliceRoot, "link"), outside)
	if _, err := s.PreviewFileOperation(context.Background(), store.admin, id.Group, id.User, FileClearDocuments, "v1", nil, 0); !errors.Is(err, ErrDenied) {
		t.Fatal("document junction followed", err)
	}
	bobRoot := filepath.Join(store.root.Name(), filepath.FromSlash(memberBase(Identity{"group", "wecom:bob"})))
	junction(bobRoot, aliceRoot)
	if _, err := s.AdminDocuments(store.admin, "group", "wecom:bob", "document-read", "seed.txt", "", "", 0, 0); err == nil {
		t.Fatal("scope root junction crossed ownership")
	}
	if data, err := os.ReadFile(filepath.Join(outside, "keep.txt")); err != nil || string(data) != "outside" {
		t.Fatal("junction target changed", err)
	}
}
