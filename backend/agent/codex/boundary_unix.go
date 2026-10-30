//go:build unix

package codex

import (
	"fmt"
	"os/exec"
)

func startBoundedProcess(cmd *exec.Cmd) (func() error, error) {
	return nil, fmt.Errorf("bounded native agent lifecycle currently requires Windows Job Objects; refusing a process-group fallback")
}
