package groupassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

var pythonPackage = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}(==[A-Za-z0-9][A-Za-z0-9._+-]{0,79})?$`)
var nodePackage = regexp.MustCompile(`^(?:@[a-z0-9._-]+/)?[a-z0-9][a-z0-9._-]{0,79}(?:@[0-9][A-Za-z0-9._+-]{0,79})?$`)
var softwareID = regexp.MustCompile(`^[0-9a-f]{32}$`)

type softwareManifest struct {
	ID, Ecosystem, Package, Path string
}

func (c *ControlTools) installInvocation(ecosystem, pkg, stage string) ([]string, error) {
	target := filepath.Join(stage, "packages")
	switch ecosystem {
	case "python":
		if !pythonPackage.MatchString(pkg) || !toolInsideRoots(c.cfg.PythonCommand, c.cfg.ToolRoots) {
			return nil, ErrInvalid
		}
		return []string{c.cfg.PythonCommand, "-m", "pip", "--isolated", "--disable-pip-version-check", "--no-input", "install",
			"--index-url", "https://pypi.org/simple", "--keyring-provider", "disabled", "--only-binary=:all:", "--no-cache-dir", "--no-compile", "--ignore-installed", "--target", target, pkg}, nil
	case "node":
		if !nodePackage.MatchString(pkg) || !toolInsideRoots(c.cfg.NodeCommand, c.cfg.ToolRoots) || !toolInsideRoots(c.cfg.NpmCLI, c.cfg.ToolRoots) {
			return nil, ErrInvalid
		}
		return []string{c.cfg.NodeCommand, c.cfg.NpmCLI, "install", "--prefix", target, "--cache", filepath.Join(stage, "cache"),
			"--userconfig", filepath.Join(stage, "user.npmrc"), "--globalconfig", filepath.Join(stage, "global.npmrc"), "--registry", "https://registry.npmjs.org/",
			"--ignore-scripts", "--no-audit", "--no-fund", "--no-progress", "--", pkg}, nil
	}
	return nil, ErrInvalid
}

func (c *ControlTools) manageSoftware(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct{ Action, Ecosystem, Package, ID string }
	if err := decodeControlArgs(raw, &args); err != nil {
		return "", err
	}
	switch args.Action {
	case "list":
		if args.Ecosystem != "" || args.Package != "" || args.ID != "" {
			return "", ErrInvalid
		}
		entries, err := fs.ReadDir(c.software.FS(), ".")
		if err != nil {
			return "", ErrUnavailable
		}
		var installed []softwareManifest
		for _, entry := range entries {
			if !entry.IsDir() || !softwareID.MatchString(entry.Name()) {
				continue
			}
			data, err := c.software.ReadFile(entry.Name() + "/manifest.json")
			var item softwareManifest
			if err != nil || len(data) > 4096 || json.Unmarshal(data, &item) != nil {
				return "", ErrInvalid
			}
			installed = append(installed, item)
			if len(installed) > 100 {
				return "", ErrBusy
			}
		}
		data, _ := json.Marshal(installed)
		return string(data), nil
	case "remove":
		if args.Package != "" || args.Ecosystem != "" || !softwareID.MatchString(args.ID) {
			return "", ErrInvalid
		}
		data, err := c.software.ReadFile(args.ID + "/manifest.json")
		var item softwareManifest
		if err != nil || len(data) > 4096 || json.Unmarshal(data, &item) != nil || item.ID != args.ID {
			return "", ErrDenied
		}
		if err := c.requireApproval(ctx, c.T(core.MsgGroupRemoveAction, item.Ecosystem, item.Package, args.ID)); err != nil {
			return "", err
		}
		if err := c.software.RemoveAll(args.ID); err != nil {
			return "", ErrUnavailable
		}
		return "Managed installation removed: " + args.ID, nil
	case "install":
		if args.ID != "" {
			return "", ErrInvalid
		}
		return c.installSoftware(ctx, args.Ecosystem, args.Package)
	}
	return "", ErrInvalid
}

func (c *ControlTools) installSoftware(ctx context.Context, ecosystem, pkg string) (string, error) {
	// Validate all executable paths/specifications before prompting or staging.
	if _, err := c.installInvocation(ecosystem, pkg, "stage"); err != nil {
		return "", err
	}
	if err := c.requireApproval(ctx, c.T(core.MsgGroupInstallAction, ecosystem, pkg)); err != nil {
		return "", err
	}
	// Keep staging on the destination volume; WorkDir may be on another drive.
	stage, err := os.MkdirTemp(c.softwarePath, ".ccga-install-")
	if err != nil {
		return "", ErrUnavailable
	}
	defer os.RemoveAll(stage)
	if err := os.MkdirAll(filepath.Join(stage, "packages"), 0700); err != nil {
		return "", ErrUnavailable
	}
	for _, name := range []string{"user.npmrc", "global.npmrc"} {
		if err := os.WriteFile(filepath.Join(stage, name), nil, 0600); err != nil {
			return "", ErrUnavailable
		}
	}
	command, err := c.installInvocation(ecosystem, pkg, stage)
	if err != nil {
		return "", err
	}
	if err := c.runInstaller(ctx, stage, command); err != nil {
		return "", fmt.Errorf("managed installation failed; no system tools changed; package may require unsupported build/install scripts")
	}
	var total int64
	count := 0
	err = filepath.WalkDir(filepath.Join(stage, "packages"), func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrInvalid
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return ErrInvalid
		}
		total += info.Size()
		count++
		if total > 512<<20 || count > 20000 {
			return ErrInvalid
		}
		return nil
	})
	if err != nil || ctx.Err() != nil {
		return "", ErrInvalid
	}
	id, err := token()
	if err != nil {
		return "", ErrUnavailable
	}
	manifest := softwareManifest{ID: id, Ecosystem: ecosystem, Package: pkg, Path: filepath.Join(c.softwarePath, id, "packages")}
	// Never replace a previous installation. Each approved install has a new ID.
	if err := c.software.Mkdir(id, 0700); err != nil {
		return "", ErrUnavailable
	}
	ok := false
	defer func() {
		if !ok {
			c.software.RemoveAll(id)
		}
	}()
	if err := os.Rename(filepath.Join(stage, "packages"), manifest.Path); err != nil {
		return "", ErrUnavailable
	}
	data, _ := json.Marshal(manifest)
	if err := c.software.WriteFile(id+"/manifest.json", data, 0600); err != nil {
		return "", ErrUnavailable
	}
	ok = true
	return string(data) + "\nUse this read-only path via per-command PYTHONPATH/NODE_PATH or explicit imports; never persist environment changes.", nil
}

func (c *ControlTools) runInstaller(ctx context.Context, stage string, command []string) error {
	// Package managers only need the fixed public registries. Dependency metadata
	// cannot use an approved install to contact arbitrary hosts or local services.
	hosts := []string{"pypi.org", "files.pythonhosted.org", "registry.npmjs.org"}
	for _, host := range hosts {
		if _, err := c.validateURL("https://" + host + "/"); err != nil {
			return err
		}
	}
	args, err := BoundaryArgs(stage, filepath.Join(stage, "public"), c.cfg.CodexHome, c.cfg.ToolRoots, hosts, true)
	if err != nil {
		return err
	}
	args = append(args, "-c", "windows.sandbox=\"mxc\"", "-c", "permissions.cc_member.network.allow_local_binding=true", "-c", "cli_auth_credentials_store=\"file\"")
	invocation := append([]string{"sandbox", "--permission-profile", "cc_member", "-C", stage, "--"}, command...)
	env := map[string]string{"TEMP": stage, "TMP": stage, "TMPDIR": stage, "PYTHONDONTWRITEBYTECODE": "1", "PIP_CONFIG_FILE": os.DevNull}
	if c.runInstall != nil {
		return c.runInstall(ctx, stage, invocation, env)
	}
	agent, err := core.CreateAgent("codex", map[string]any{
		"cmd": append([]string{c.cfg.CodexCommand}, args...), "backend": "app_server", "permission_profile": "cc_member", "codex_home": c.cfg.CodexHome,
	})
	if err != nil {
		return err
	}
	defer agent.Stop()
	executor, ok := agent.(core.BoundedCommandRunner)
	if !ok {
		return ErrUnavailable
	}
	// Boundaries and networking apply only to this separately approved installer.
	return executor.RunBoundedCommand(ctx, stage, invocation, env)
}

func toolInsideRoots(name string, roots []string) bool {
	if !filepath.IsAbs(name) {
		return false
	}
	for _, root := range roots {
		rel, err := filepath.Rel(root, name)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
