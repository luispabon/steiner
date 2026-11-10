package sandbox

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/config"
)

var (
	lookupBwrap       = exec.LookPath
	prepareSSHOverlay = prepareSSHOverlayFromPath
)

// Sandbox wraps bubblewrap invocation for tool execution.
type Sandbox struct {
	cfg       config.SandboxConfig
	perms     config.PermissionsConfig
	root      string // absolute project root
	workDir   string // absolute agent workDir
	userHome  string // host user home
	tmpDir    string // session-scoped temp directory
	envPolicy EnvPolicy

	bwrapPath string // absolute bwrap path resolved at construction
	bwrapErr  error  // non-nil when bwrap could not be resolved

	resourceMu       sync.Mutex
	commandResources map[*exec.Cmd]*sshOverlay

	tmpMu                 sync.Mutex
	tmpLock               *os.File // held flock on the tmp dir inode, nil when unprotected
	tmpLockUnsupported    bool     // platform has no advisory directory lock
	tmpClosed             bool     // Cleanup already ran
	tmpWarned             bool     // reprovision handler already fired
	tmpReprovisionHandler func(string)
}

// New creates a Sandbox. rootDir, workDir, userHome, and tmpDir must be absolute paths.
func New(cfg config.SandboxConfig, perms config.PermissionsConfig, rootDir, workDir, userHome, tmpDir string) *Sandbox {
	s := &Sandbox{
		cfg:       cfg,
		perms:     perms,
		root:      rootDir,
		workDir:   workDir,
		userHome:  userHome,
		tmpDir:    tmpDir,
		envPolicy: newEnvPolicy(cfg.EnvPassthroughAll, cfg.EnvPassthrough),
	}
	if cfg.Enabled {
		s.bwrapPath, s.bwrapErr = resolveBwrap()
	}
	return s
}

func resolveBwrap() (string, error) {
	path, err := lookupBwrap("bwrap")
	if err != nil {
		return "", fmt.Errorf("locate bwrap: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve bwrap path: %w", err)
	}
	return abs, nil
}

// Enabled reports whether sandboxing is active.
func (s *Sandbox) Enabled() bool {
	return s.cfg.Enabled
}

// TmpDir returns the session-scoped temporary directory.
func (s *Sandbox) TmpDir() string {
	return s.tmpDir
}

// ensurePlanModeDirs best-effort creates the plan-mode writable directories.
// It never follows symlinks while creating .steiner or its leaf directories.
func ensurePlanModeDirs(root string) {
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() {
		return
	}

	steinerPath := filepath.Join(root, ".steiner")
	if !ensurePlanModeDir(steinerPath) {
		return
	}
	for _, dir := range config.PlanModeWritableDirs() {
		path := filepath.Join(root, filepath.FromSlash(dir))
		if filepath.Dir(path) != steinerPath {
			continue
		}
		_ = ensurePlanModeDir(path)
	}
}

func ensurePlanModeDir(path string) bool {
	info, err := os.Lstat(path)
	if err == nil {
		return info.IsDir() && info.Mode()&os.ModeSymlink == 0
	}
	if !os.IsNotExist(err) {
		return false
	}
	return os.Mkdir(path, 0o755) == nil
}

// EnsureDirectoryPath verifies or creates path below root without following
// symlinks. The root itself may be a symlink, but no child component may be.
func EnsureDirectoryPath(root, path string) error {
	resolvedRoot, rel, err := prepareDirectoryPath(root, path)
	if err != nil {
		return err
	}
	if err := ensureDirectoryComponents(root, rel); err != nil {
		return err
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil || !pathWithin(resolvedRoot, resolvedPath) {
		return fmt.Errorf("path %q resolves outside root %q", path, root)
	}
	return nil
}

func prepareDirectoryPath(root, path string) (string, string, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve sandbox root: %w", err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		return "", "", fmt.Errorf("invalid sandbox root: %w", err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path %q is outside root %q", path, root)
	}
	return resolvedRoot, rel, nil
}

func ensureDirectoryComponents(root, rel string) error {
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		if err := ensureDirectoryComponent(current); err != nil {
			return err
		}
	}
	return nil
}

func ensureDirectoryComponent(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %q is a symlink", path)
		}
		if !info.IsDir() {
			return fmt.Errorf("path component %q is not a directory", path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("inspect path component %q: %w", path, err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		return fmt.Errorf("create path component %q: %w", path, err)
	}
	return nil
}

// WrapCommandMode wraps cmd with bubblewrap, optionally with project read-only mode.
// Returns cmd unchanged when sandbox is disabled. When enabled but bwrap could
// not be resolved, it fails closed with an error instead of returning cmd.
func (s *Sandbox) WrapCommandMode(cmd *exec.Cmd, readOnlyProject bool) (*exec.Cmd, error) {
	if err := s.validateWrap(readOnlyProject); err != nil {
		return nil, err
	}
	if !s.cfg.Enabled {
		return cmd, nil
	}
	tmpDir := s.tmpDir
	if tmpDir != "" {
		bindable, err := s.ensureTmpDir()
		if err != nil {
			if errors.Is(err, errSandboxClosed) || !readOnlyProject {
				return nil, err
			}
			// Read-only mode cannot bind a directory that is missing or
			// unusable, so it falls back to a tmpfs /tmp.
			slog.Warn("sandbox tmp dir unavailable, using tmpfs /tmp", "path", s.tmpDir, "error", err)
			tmpDir = ""
		} else if !bindable {
			if !readOnlyProject {
				return nil, fmt.Errorf("sandbox tmp dir %s: %w", s.tmpDir, errTmpDirNotOwned)
			}
			slog.Warn("sandbox tmp dir not owned by this session, using tmpfs /tmp", "path", s.tmpDir)
			tmpDir = ""
		}
	}
	overlay := s.prepareOverlay(cmd)
	sandboxHome := filepath.Join(s.root, ".steiner", "home")
	if err := s.prepareCache(sandboxHome, readOnlyProject); err != nil {
		if overlay != nil {
			_ = overlay.Close()
		}
		return nil, err
	}
	var overlayArgs []string
	if overlay != nil {
		overlayArgs = overlay.bwrapArgs
	}
	bwrapArgs := BuildArgs(s.root, s.workDir, sandboxHome, s.userHome, s.cfg.HostMounts, overlayArgs, tmpDir, readOnlyProject, s.perms, s.cfg.BindHostCache)

	// Build the new Args slice: [bwrap, ...bwrap-args..., "--", original-cmd, original-args...]
	args := make([]string, 0, 1+len(bwrapArgs)+1+len(cmd.Args))
	args = append(args, "bwrap")
	args = append(args, bwrapArgs...)
	args = append(args, "--")
	args = append(args, cmd.Args...)

	// A nil cmd.Env is not "no environment" — os/exec treats it as "inherit
	// the full host environment". Treating nil as already-filtered is the bug
	// that let every built-in tool run with steiner's complete environment,
	// credentials included, regardless of the allowlist.
	inherited := cmd.Env
	if inherited == nil {
		inherited = os.Environ()
	}
	wrapped := &exec.Cmd{
		Path:   s.bwrapPath,
		Args:   args,
		Stdin:  cmd.Stdin,
		Stdout: cmd.Stdout,
		Stderr: cmd.Stderr,
		Env:    FilterEnv(inherited, s.envPolicy),
	}
	if len(cmd.ExtraFiles) > 0 {
		wrapped.ExtraFiles = append(wrapped.ExtraFiles, cmd.ExtraFiles...)
	}
	if overlay != nil {
		wrapped.ExtraFiles = append(wrapped.ExtraFiles, overlay.memfds...)
		s.trackCommandResources(wrapped, overlay)
	}
	return wrapped, nil
}

func (s *Sandbox) validateWrap(readOnlyProject bool) error {
	if !s.cfg.Enabled {
		return nil
	}
	if s.bwrapErr != nil {
		return fmt.Errorf("sandbox enabled but unavailable: %w", s.bwrapErr)
	}
	if readOnlyProject {
		ensurePlanModeDirs(s.root)
		if err := EnsureDirectoryPath(s.root, filepath.Join(s.root, ".steiner", "home")); err != nil {
			return fmt.Errorf("unsafe plan sandbox home: %w", err)
		}
	}
	return nil
}

func (s *Sandbox) prepareOverlay(cmd *exec.Cmd) *sshOverlay {
	overlay, err := prepareSSHOverlay(sshSystemConfigPath, 3+len(cmd.ExtraFiles))
	if err != nil {
		if overlay != nil {
			_ = overlay.Close()
		}
		return nil
	}
	return overlay
}

func (s *Sandbox) prepareCache(sandboxHome string, readOnlyProject bool) error {
	if s.cfg.BindHostCache || s.userHome == "" || cacheMountPath(s.userHome) == "" {
		return nil
	}
	cacheRoot := s.root
	if readOnlyProject {
		cacheRoot = sandboxHome
	}
	if err := EnsureDirectoryPath(cacheRoot, privateCacheDir(sandboxHome)); err != nil {
		return fmt.Errorf("create sandbox cache dir: %w", err)
	}
	return nil
}

func (s *Sandbox) trackCommandResources(cmd *exec.Cmd, overlay *sshOverlay) {
	if overlay == nil {
		return
	}
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	if s.commandResources == nil {
		s.commandResources = make(map[*exec.Cmd]*sshOverlay)
	}
	s.commandResources[cmd] = overlay
}

// ReleaseCommandResources closes files owned by a wrapped command.
func (s *Sandbox) ReleaseCommandResources(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	s.resourceMu.Lock()
	overlay := s.commandResources[cmd]
	delete(s.commandResources, cmd)
	s.resourceMu.Unlock()
	if overlay != nil {
		_ = overlay.Close()
	}
}

// WrapCommand wraps cmd with bubblewrap. Returns cmd unchanged when sandbox disabled.
func (s *Sandbox) WrapCommand(cmd *exec.Cmd) (*exec.Cmd, error) {
	return s.WrapCommandMode(cmd, false)
}

// EnsureHome creates .steiner/home/ inside workspaceDir.
func (s *Sandbox) EnsureHome() error {
	sandboxHome := filepath.Join(s.root, ".steiner", "home")
	if err := EnsureDirectoryPath(s.root, sandboxHome); err != nil {
		return fmt.Errorf("create sandbox home dir: %w", err)
	}
	return nil
}
