package sandbox

import (
	"github.com/luispabon/steiner/internal/config"
)

// BuildArgs returns the bwrap argument list (excluding the trailing -- cmd args).
func BuildArgs(workspace, sandboxHome, userHome string, _ config.PermissionsConfig, hostMounts []config.HostMount) []string {
	var args []string

	// Namespace isolation: unshare all but share network.
	args = append(args, "--unshare-all", "--share-net")

	// Root filesystem: entire root read-only (base layer).
	args = append(args, "--ro-bind", "/", "/")

	// System mounts.
	args = append(args,
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
	)

	// Project workspace writable at original absolute path.
	args = append(args, "--bind", workspace, workspace)

	// Sandbox state directory writable at original absolute path.
	args = append(args, "--bind", sandboxHome, sandboxHome)

	// User cache directory writable so tools can read and write cached data.
	if userHome != "" {
		cacheDir := userHome + "/.cache"
		args = append(args, "--bind", cacheDir, cacheDir)
	}

	// Set working directory to workspace.
	args = append(args, "--chdir", workspace)

	// Additional host mounts from config.
	for _, hm := range hostMounts {
		flag := "--ro-bind"
		if hm.Mode == "rw" {
			flag = "--bind"
		}
		args = append(args, flag, hm.Path, hm.Path)
	}

	return args
}
