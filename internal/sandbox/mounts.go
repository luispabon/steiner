package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

// BuildArgs returns the bwrap argument list (excluding the trailing -- cmd args).
func BuildArgs(writableRoot, workDir, sandboxHome, userHome string, hostMounts []config.HostMount, overlayArgs []string, tmpDir string, readOnlyProject bool, perms config.PermissionsConfig, bindHostCache bool) []string {
	var args []string

	// Namespace isolation: unshare all but share network.
	args = append(args, "--unshare-all", "--share-net")

	// Tie the sandbox lifetime to steiner's and detach from the controlling
	// terminal so sandboxed processes cannot open /dev/tty (TIOCSTI injection).
	args = append(args, "--die-with-parent", "--new-session")

	// Root filesystem: entire root read-only (base layer).
	args = append(args, "--ro-bind", "/", "/")

	// System mounts.
	args = append(args,
		"--dev", "/dev",
		"--proc", "/proc",
	)
	if tmpDir != "" && (!readOnlyProject || safeDirectoryPath(writableRoot, tmpDir) == nil) {
		args = append(args, "--bind", tmpDir, "/tmp")
	} else {
		args = append(args, "--tmpfs", "/tmp")
	}

	// Project workspace binding: read-only or writable depending on plan mode.
	if readOnlyProject {
		args = append(args, "--ro-bind", writableRoot, writableRoot)
		for _, path := range planModeWritableBinds(writableRoot) {
			args = append(args, "--bind", path, path)
		}
		// Plan mode keeps the working tree read-only but must still allow git
		// metadata operations (branch/commit/stage) so a planning session can
		// hand off to implementation. .git is existence-gated (unlike the
		// plan-mode writable directories, it cannot be created) and bound whole
		// rather than by path, since git writes transient lock files (index.lock,
		// config.lock, packed-refs.new) that don't exist at mount time.
		for _, gitBind := range gitWritableBinds(writableRoot) {
			args = append(args, "--bind", gitBind, gitBind)
		}
	} else {
		args = append(args, "--bind", writableRoot, writableRoot)
	}

	// Sandbox state directory writable at original absolute path.
	sandboxHomeSafe := !readOnlyProject || safeDirectoryPath(writableRoot, sandboxHome) == nil
	if sandboxHomeSafe {
		args = append(args, "--bind", sandboxHome, sandboxHome)
	}

	// The cache location is backed by a sandbox-private directory unless the
	// user opted in to the real host cache, which lets sandboxed tools poison
	// caches (go-build, pip, uv) later consumed outside the sandbox.
	if userHome != "" {
		if cacheDir := cacheMountPath(userHome); cacheDir != "" {
			src := privateCacheDir(sandboxHome)
			if bindHostCache {
				src = cacheDir
			}
			cacheSafe := !readOnlyProject || (sandboxHomeSafe && safeDirectoryPath(writableRoot, src) == nil && realDirectory(cacheDir))
			if cacheSafe {
				args = append(args, "--bind", src, cacheDir)
			}
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

// planModeWritableBinds returns only existing, in-root plan directories safe to bind writable.
func planModeWritableBinds(root string) []string {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() {
		return nil
	}

	steinerPath := filepath.Join(root, ".steiner")
	steinerInfo, err := os.Lstat(steinerPath)
	if err != nil || steinerInfo.Mode()&os.ModeSymlink != 0 || !steinerInfo.IsDir() {
		return nil
	}

	var binds []string
	for _, dir := range config.PlanModeWritableDirs() {
		path := filepath.Join(root, filepath.FromSlash(dir))
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		resolvedPath, err := filepath.EvalSymlinks(path)
		if err != nil || !pathWithin(resolvedRoot, resolvedPath) {
			continue
		}
		binds = append(binds, path)
	}
	return binds
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func safeDirectoryPath(root, path string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() {
		return fmt.Errorf("root is not a directory")
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) || !pathWithin(root, filepath.Clean(path)) {
		return fmt.Errorf("path is outside root")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("path component is not a real directory")
		}
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil || !pathWithin(resolvedRoot, resolvedPath) {
		return fmt.Errorf("path resolves outside root")
	}
	return nil
}

func realDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

// gitWritableBinds returns the absolute paths that must be bound writable
// (on top of an otherwise read-only project mount) for git plumbing to work:
// the repo's .git directory, or — for a linked worktree, where .git is a
// pointer file — the worktree-specific gitdir and its shared common dir.
// Returns nil (no binds) whenever a path can't be resolved, doesn't exist, or
// fails a sanity check appropriate to a real git layout; bwrap hard-fails the
// whole invocation if a bind source is missing, so binds are only ever
// emitted for paths confirmed to exist and to have been resolved through any
// symlinks along the way.
func gitWritableBinds(root string) []string {
	gitPath := filepath.Join(root, ".git")
	fi, err := os.Lstat(gitPath)
	if err != nil {
		return nil
	}

	// Real git never makes .git itself a symlink — it's either an ordinary
	// directory (plain repo) or a regular pointer file (linked worktree).
	// Reject a symlinked .git outright rather than deciding whether to bind
	// the symlink path or its resolved target: the bind's destination is
	// always gitPath (the call site binds it onto itself), so resolving and
	// binding the target instead would expose that target writable at its
	// own real location too, which is strictly worse. There's no legitimate
	// case for .git-as-symlink, so it fails closed here.
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if fi.IsDir() {
		return []string{gitPath}
	}
	if !fi.Mode().IsRegular() {
		return nil
	}

	// .git is a regular file: linked worktree, pointing at the real gitdir
	// via "gitdir: <path>".
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
	resolvedGitDir, err := filepath.EvalSymlinks(gitDir)
	if err != nil {
		return nil
	}

	// A real worktree gitdir always lives at
	// <main-repo>/.git/worktrees/<name>; anything else (e.g. a "gitdir:"
	// line pointing straight at an attacker-chosen directory like /etc)
	// doesn't have that shape and is rejected rather than trusted.
	if filepath.Base(filepath.Dir(resolvedGitDir)) != "worktrees" {
		return nil
	}

	binds := []string{gitPath, resolvedGitDir}

	// The worktree gitdir holds only worktree-local state (HEAD, index,
	// logs); refs, objects, and config live in the common dir it points to
	// via "commondir". Branch/commit operations need that writable too. Git
	// always records this as "../..", i.e. two levels up from
	// .git/worktrees/<name> — verify the resolved commondir actually lands
	// there rather than trusting whatever the file says.
	if commonData, readErr := os.ReadFile(filepath.Join(resolvedGitDir, "commondir")); readErr == nil {
		commonDir := strings.TrimSpace(string(commonData))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(resolvedGitDir, commonDir)
		}
		if resolvedCommonDir, err := filepath.EvalSymlinks(commonDir); err == nil {
			expectedCommonDir := filepath.Dir(filepath.Dir(resolvedGitDir))
			if resolvedCommonDir == expectedCommonDir && resolvedCommonDir != resolvedGitDir {
				binds = append(binds, resolvedCommonDir)
			}
		}
	}

	return binds
}

func privateCacheDir(sandboxHome string) string {
	return filepath.Join(sandboxHome, "cache")
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
