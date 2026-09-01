package sandbox

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

// BuildArgs returns the bwrap argument list (excluding the trailing -- cmd args).
func BuildArgs(writableRoot, workDir, sandboxHome, userHome string, hostMounts []config.HostMount, overlayArgs []string, tmpDir string, readOnlyProject bool, perms config.PermissionsConfig) []string {
	var args []string

	// Namespace isolation: unshare all but share network.
	args = append(args, "--unshare-all", "--share-net")

	// Root filesystem: entire root read-only (base layer).
	args = append(args, "--ro-bind", "/", "/")

	// System mounts.
	args = append(args,
		"--dev", "/dev",
		"--proc", "/proc",
	)
	if tmpDir != "" {
		args = append(args, "--bind", tmpDir, "/tmp")
	} else {
		args = append(args, "--tmpfs", "/tmp")
	}

	// Project workspace binding: read-only or writable depending on plan mode.
	if readOnlyProject {
		args = append(args, "--ro-bind", writableRoot, writableRoot)
		args = append(args, "--bind", filepath.Join(writableRoot, ".steiner", "plans"), filepath.Join(writableRoot, ".steiner", "plans"))
		// Plan mode keeps the working tree read-only but must still allow git
		// metadata operations (branch/commit/stage) so a planning session can
		// hand off to implementation. .git is existence-gated (unlike
		// .steiner/plans, it cannot be created) and bound whole rather than by
		// path, since git writes transient lock files (index.lock,
		// config.lock, packed-refs.new) that don't exist at mount time.
		for _, gitBind := range gitWritableBinds(writableRoot) {
			args = append(args, "--bind", gitBind, gitBind)
		}
	} else {
		args = append(args, "--bind", writableRoot, writableRoot)
	}

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

	// Docker socket masking: appended after host mounts and overlay args, and
	// immediately before --chdir, so no earlier bind — including a user
	// host_mounts entry that rw-binds /run — can unmask the socket. Later
	// bwrap operations win. When perms.Docker is true the socket is already
	// reachable via the root bind and nothing is emitted; that asymmetry is
	// the entire point of the permission.
	if !perms.Docker {
		args = append(args, dockerDenyArgs(dockerSocketCandidates())...)
	}

	// Set working directory to workspace after all mounts have been established.
	args = append(args, "--chdir", workDir)

	return args
}

// gitWritableBinds returns the absolute paths that must be bound writable
// (on top of an otherwise read-only project mount) for git plumbing to work:
// the repo's .git directory, or — for a linked worktree, where .git is a
// pointer file — the worktree-specific gitdir and its shared common dir.
// Returns nil (no binds) whenever a path can't be resolved or doesn't exist;
// bwrap hard-fails the whole invocation if a bind source is missing, so
// binds are only ever emitted for paths confirmed to exist.
func gitWritableBinds(root string) []string {
	gitPath := filepath.Join(root, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return nil
	}
	if info.IsDir() {
		return []string{gitPath}
	}

	// .git is a file: linked worktree, pointing at the real gitdir via
	// "gitdir: <path>".
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return nil
	}
	const prefix = "gitdir:"
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, prefix) {
		return nil
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	if _, err := os.Stat(gitDir); err != nil {
		return nil
	}
	binds := []string{gitPath, gitDir}

	// The worktree gitdir holds only worktree-local state (HEAD, index,
	// logs); refs, objects, and config live in the common dir it points to
	// via "commondir". Branch/commit operations need that writable too.
	if commonData, readErr := os.ReadFile(filepath.Join(gitDir, "commondir")); readErr == nil {
		commonDir := strings.TrimSpace(string(commonData))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
		if resolved, absErr := filepath.Abs(commonDir); absErr == nil {
			if _, statErr := os.Stat(resolved); statErr == nil && resolved != gitDir {
				binds = append(binds, resolved)
			}
		}
	}

	return binds
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

// WritableHostMounts returns the sandbox host-mount paths configured
// writable, preserving config order. Mounts with Mode other than "rw"
// (including empty, meaning read-only) are excluded. Paths are already
// home-expanded at config load.
func WritableHostMounts(cfg config.SandboxConfig) []string {
	var mounts []string
	for _, m := range cfg.HostMounts {
		if m.Mode == "rw" {
			mounts = append(mounts, m.Path)
		}
	}
	return mounts
}
