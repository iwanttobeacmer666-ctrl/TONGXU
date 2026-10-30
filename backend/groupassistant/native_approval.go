package groupassistant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

func ReviewableNativeAction(event core.Event, workspace string) (string, string, error) {
	if event.ToolName != "Patch" {
		return ReviewableNativeCommand(event, workspace)
	}
	raw := event.ToolInputRaw
	if raw == nil || raw["grantRoot"] != nil || raw["additionalPermissions"] != nil {
		return "", "", ErrDenied
	}
	data, err := json.Marshal(raw["changes"])
	if err != nil || len(data) > 64<<10 {
		return "", "", ErrDenied
	}
	var changes []struct {
		Path string
		Diff *string
		Kind struct {
			Type     string
			MovePath *string `json:"move_path"`
		}
	}
	if json.Unmarshal(data, &changes) != nil || len(changes) == 0 || len(changes) > 100 {
		return "", "", ErrDenied
	}
	for _, change := range changes {
		if change.Diff == nil {
			return "", "", ErrDenied
		}
		if change.Kind.Type != "add" && change.Kind.Type != "delete" && change.Kind.Type != "update" {
			return "", "", ErrDenied
		}
		if !nativePathInside(workspace, change.Path) || change.Kind.MovePath != nil && !nativePathInside(workspace, *change.Kind.MovePath) {
			return "", "", ErrDenied
		}
	}
	return string(data), workspace, nil
}

func nativePathInside(workspace, name string) bool {
	if !filepath.IsAbs(name) {
		return false
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return false
	}
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(name)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			relative, err := filepath.Rel(root, resolved)
			return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
		}
		if !os.IsNotExist(err) || filepath.Dir(name) == name {
			return false
		}
		missing = append(missing, filepath.Base(name))
		name = filepath.Dir(name)
	}
}

// ReviewableNativeCommand admits one in-boundary command, never a permission
// expansion, remote environment, stdin injection or persistent policy amendment.
func ReviewableNativeCommand(event core.Event, workspace string) (string, string, error) {
	if event.ToolName != "Bash" || event.ToolInputRaw == nil {
		return "", "", ErrDenied
	}
	raw := event.ToolInputRaw
	if raw["additionalPermissions"] != nil || raw["networkApprovalContext"] != nil {
		return "", "", ErrDenied
	}
	if environment, _ := raw["environmentId"].(string); environment != "" && environment != "local" {
		return "", "", ErrDenied
	}
	if kind, _ := raw["kind"].(string); kind != "" && kind != "command" {
		return "", "", ErrDenied
	}
	command, _ := raw["command"].(string)
	cwd, _ := raw["cwd"].(string)
	if command == "" || len(command) > 64<<10 || strings.ContainsRune(command, 0) || !filepath.IsAbs(cwd) {
		return "", "", ErrDenied
	}
	cwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", "", ErrDenied
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", "", ErrDenied
	}
	relative, err := filepath.Rel(root, cwd)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", ErrDenied
	}
	return command, cwd, nil
}
