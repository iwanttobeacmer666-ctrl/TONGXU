package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

//go:noinline
func growOperatorPipeStack(depth int) byte {
	var scratch [4096]byte
	scratch[0] = byte(depth)
	if depth > 0 {
		scratch[1] = growOperatorPipeStack(depth - 1)
	}
	runtime.KeepAlive(&scratch)
	return scratch[0] ^ scratch[1]
}

type operatorPipeStackContext struct{ context.Context }

func (operatorPipeStackContext) Err() error {
	growOperatorPipeStack(128)
	return context.Canceled
}

func TestOperatorFilePipeCancellationStackHelper(t *testing.T) {
	if os.Getenv("CC_OPERATOR_STACK_HELPER") != "1" {
		return
	}
	name, err := windows.UTF16PtrFromString(pipeName(filepath.Join(t.TempDir(), "stack-lifetime")))
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED, windows.PIPE_TYPE_MESSAGE|windows.PIPE_READMODE_MESSAGE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 1024, 1024, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	_, err = pipeIO(operatorPipeStackContext{context.Background()}, h, true, false, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("pending connection did not cancel", err)
	}
}

func TestOperatorFilePipeCancellationKeepsKernelBuffersStableAcrossStackGrowth(t *testing.T) {
	// Bound the regression in a synthetic child: the old code can wait forever
	// when the kernel retains an OVERLAPPED address on a moved Go stack.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestOperatorFilePipeCancellationStackHelper$", "-test.v")
	command.Env = append(os.Environ(), "CC_OPERATOR_STACK_HELPER=1")
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("pending pipe hung or failed after stack growth: %v: %s", err, output)
	}
}
