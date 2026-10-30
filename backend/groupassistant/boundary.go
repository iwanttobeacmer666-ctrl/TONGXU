package groupassistant

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// BoundaryArgs configures native Codex resource permissions, retaining shell,
// file-editing, compilation and debugging tools. Each agent process gets its own
// workspace and profile instance; no profile is written into global settings.
func BoundaryArgs(workspace, public, codexHome string, toolRoots, hosts []string, admin bool) ([]string, error) {
	for _, p := range []string{workspace, public, codexHome} {
		if !filepath.IsAbs(p) {
			return nil, ErrInvalid
		}
	}
	prefix := "permissions.cc_member."
	// Codex splits the override key on dots without parsing quoted key
	// segments. Put path/domain keys inside TOML table values instead.
	filesystem := []string{
		"\":root\"=\"deny\"",
		"\":minimal\"=\"read\"",
		strconv.Quote(workspace) + "=\"write\"",
		strconv.Quote(codexHome) + "=\"deny\"",
	}
	if !admin {
		filesystem = append(filesystem, strconv.Quote(public)+"=\"read\"")
	}
	for _, p := range toolRoots {
		if !filepath.IsAbs(p) || filepath.Clean(p) == filepath.VolumeName(p)+string(filepath.Separator) {
			return nil, ErrInvalid
		}
		filesystem = append(filesystem, strconv.Quote(p)+"=\"read\"")
	}
	settings := []string{
		"default_permissions=\"cc_member\"",
		prefix + "filesystem={" + strings.Join(filesystem, ",") + "}",
		"shell_environment_policy.inherit=\"core\"",
		"shell_environment_policy.exclude=[\"*TOKEN*\",\"*KEY*\",\"*SECRET*\",\"*PASSWORD*\",\"CC_GROUP_*\"]",
		"projects={" + strconv.Quote(workspace) + "={trust_level=\"untrusted\"}}",
	}
	settings = append(settings, fmt.Sprintf("%snetwork.enabled=%t", prefix, len(hosts) > 0), "features.network_proxy=true")
	var domains []string
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || h != "*" && strings.ContainsAny(h, "/:*\\@") || strings.HasSuffix(h, ".") {
			return nil, ErrInvalid
		}
		domains = append(domains, strconv.Quote(h)+"=\"allow\"")
	}
	settings = append(settings, prefix+"network.domains={"+strings.Join(domains, ",")+"}")
	settings = append(settings, prefix+"network.allow_local_binding=false", prefix+"network.allow_upstream_proxy=false")
	args := make([]string, 0, 2*len(settings))
	for _, s := range settings {
		args = append(args, "-c", s)
	}
	return args, nil
}
