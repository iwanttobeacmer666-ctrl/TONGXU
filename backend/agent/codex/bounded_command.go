package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/chenhg5/cc-connect/core"
)

// Keep diagnostics bounded and private to the trusted caller. The group-facing
// software broker deliberately replaces these with a generic public error.
type commandDiagnostic struct{ bytes.Buffer }

func (b *commandDiagnostic) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 8192 - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func (a *Agent) RunBoundedCommand(ctx context.Context, workDir string, args []string, env map[string]string) error {
	if runtime.GOOS != "windows" || a.permissionProfile == "" || !filepath.IsAbs(workDir) {
		return fmt.Errorf("bounded command unavailable")
	}
	bin, err := resolveCodexExecutable(a.cmd)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, append(append([]string(nil), a.cliExtraArgs...), args...)...)
	cmd.Dir = workDir
	var diagnostic commandDiagnostic
	cmd.Stderr = &diagnostic
	cmd.Env = ownerReviewedBaseEnvironment(os.Environ())
	cmd.Env = append(cmd.Env, "CODEX_HOME="+a.codexHome)
	for key, value := range env {
		cmd.Env = core.MergeEnv(cmd.Env, []string{key + "=" + value})
	}
	closeJob, err := startBoundedProcess(cmd)
	if err != nil {
		return err
	}
	err = cmd.Wait()
	if err != nil {
		err = fmt.Errorf("bounded command: %w: %s", err, diagnostic.String())
	}
	return errors.Join(err, closeJob())
}
