package sandbox

import (
	"os"
	"path/filepath"

	"github.com/luispabon/steiner/internal/config"
)

// BuildArgs returns the bwrap argument list (excluding the trailing -- cmd args).
func BuildArgs(workspace, sandboxHome, userHome string, _ config.PermissionsConfig, hostMounts []config.HostMount, overlayArgs []string) []string {
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
		if cacheDir := cacheMountPath(userHome); cacheDir != "" {
			args = append(args, "--bind", cacheDir, cacheDir)
		}
	}

	// Additional host mounts from config.
	for _, hm := range hostMounts {
		flag := "--ro-bind"
		if hm.Mode == "rw" {
			flag = "--bind"
		}
		args = append(args, flag, hm.Path, hm.Path)
	}

	args = append(args, overlayArgs...)

	// Set working directory to workspace after all mounts have been established.
	args = append(args, "--chdir", workspace)

	return args
}

func cacheMountPath(userHome string) string {
	cacheDir := filepath.Join(userHome, ".cache")
	if _, err := os.Stat(cacheDir); err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(cacheDir); err == nil {
		return resolved
	}
	return cacheDir
}
