//go:build windows

package codex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Synthetic helper spawns immediately; no model, network or sandbox setup runs.
func TestBoundedProcessHelper(t *testing.T) {
	mode := os.Getenv("CC_BOUNDARY_HELPER")
	if mode == "" {
		return
	}
	if mode == "parent" {
		child := exec.Command(os.Args[0], "-test.run=^TestBoundedProcessHelper$")
		child.Env = append(os.Environ(), "CC_BOUNDARY_HELPER=child")
		child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
		if e := child.Start(); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(2)
		}
		if e := os.WriteFile(os.Getenv("CC_BOUNDARY_CHILD_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0600); e != nil {
			os.Exit(3)
		}
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}
func TestBoundedProcessStopsImmediateDetachedDescendants(t *testing.T) {
	for _, cancelContext := range []bool{false, true} {
		t.Run(fmt.Sprint("cancel=", cancelContext), func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBoundedProcessHelper$")
			cmd.Env = append(os.Environ(), "CC_BOUNDARY_HELPER=parent", "CC_BOUNDARY_CHILD_PID="+pidFile)
			closeJob, e := startBoundedProcess(cmd)
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				if e := closeJob(); e != nil {
					t.Error(e)
				}
				cmd.Wait()
			}()
			var pid int
			deadline := time.Now().Add(6 * time.Second)
			for time.Now().Before(deadline) {
				if b, e := os.ReadFile(pidFile); e == nil {
					pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
					if pid > 0 {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pid == 0 {
				t.Fatal("helper child not started")
			}
			process, e := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
			if e != nil {
				t.Fatal(e)
			}
			defer windows.CloseHandle(process)
			if cancelContext {
				cancel()
			}
			if e = closeJob(); e != nil {
				t.Fatal(e)
			}
			state, e := windows.WaitForSingleObject(process, 1000)
			if e != nil || state != windows.WAIT_OBJECT_0 {
				t.Fatalf("descendant survived: wait=%d error=%v", state, e)
			}
		})
	}
}
