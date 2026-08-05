package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/luispabon/steiner/internal/config"
)

var (
	lookupBwrap       = exec.LookPath
	prepareSSHOverlay = prepareSSHOverlayFromPath
)

// Sandbox wraps bubblewrap invocation for tool execution.
type Sandbox struct {
	cfg        config.SandboxConfig
	perms      config.PermissionsConfig
	root       string // absolute project root
	workDir    string // absolute agent workDir
	userHome   string // host user home
	tmpDir     string // session-scoped temp directory
	hostMounts []config.HostMount
	envPolicy  EnvPolicy
}

// New creates a Sandbox. rootDir, workDir, userHome, and tmpDir must be absolute paths.
func New(cfg config.SandboxConfig, perms config.PermissionsConfig, hostMounts []config.HostMount, rootDir, workDir, userHome, tmpDir string) *Sandbox {
	return &Sandbox{
		cfg:        cfg,
		perms:      perms,
		root:       rootDir,
		workDir:    workDir,
		userHome:   userHome,
		tmpDir:     tmpDir,
		hostMounts: hostMounts,
		envPolicy:  NewEnvPolicy(cfg.EnvPassthroughAll, cfg.EnvPassthrough),
	}
}

// Enabled reports whether sandboxing is active.
func (s *Sandbox) Enabled() bool {
	return s.cfg.Enabled
}

// TmpDir returns the session-scoped temporary directory.
func (s *Sandbox) TmpDir() string {
	return s.tmpDir
}

// WrapCommandMode wraps cmd with bubblewrap, optionally with project read-only mode.
// Returns cmd unchanged when sandbox disabled or bwrap is unavailable.
func (s *Sandbox) WrapCommandMode(cmd *exec.Cmd, readOnlyProject bool) *exec.Cmd {
	if !s.cfg.Enabled {
		return cmd
	}

	overlayFDBase := 3 + len(cmd.ExtraFiles)
	overlay, err := prepareSSHOverlay(sshSystemConfigPath, overlayFDBase)
	if err != nil {
		if overlay != nil {
			_ = overlay.Close() // Best-effort cleanup; overlay failures must not block sandboxing.
		}
		overlay = nil
	}

	bwrapPath, err := lookupBwrap("bwrap")
	if err != nil {
		// bwrap not available — return cmd unchanged; caller should have run PrereqCheck.
		if overlay != nil {
			_ = overlay.Close() // Best-effort cleanup; overlay files will not be handed to a child process.
		}
		return cmd
	}

	if readOnlyProject {
		_ = os.MkdirAll(filepath.Join(s.root, ".steiner"), 0o755) // Best-effort; ro-bind will fail if it still doesn't exist, but create attempt must not block.
	}

	sandboxHome := filepath.Join(s.root, ".steiner", "home")
	var overlayArgs []string
	if overlay != nil {
		overlayArgs = overlay.bwrapArgs
	}
	bwrapArgs := BuildArgs(s.root, s.workDir, sandboxHome, s.userHome, s.hostMounts, overlayArgs, s.tmpDir, readOnlyProject, s.perms)

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
		Path:   bwrapPath,
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
	}
	return wrapped
}

// WrapCommand wraps cmd with bubblewrap. Returns cmd unchanged when sandbox disabled.
func (s *Sandbox) WrapCommand(cmd *exec.Cmd) *exec.Cmd {
	return s.WrapCommandMode(cmd, false)
}

// EnsureHome creates .steiner/home/ inside workspaceDir.
func (s *Sandbox) EnsureHome() error {
	sandboxHome := filepath.Join(s.root, ".steiner", "home")
	if err := os.MkdirAll(sandboxHome, 0o755); err != nil {
		return fmt.Errorf("create sandbox home dir: %w", err)
	}
	return nil
}

// Cleanup removes the session-scoped tmp directory.
// No-op when tmpDir is empty.
func (s *Sandbox) Cleanup() error {
	if s.tmpDir == "" {
		return nil
	}
	if err := os.RemoveAll(s.tmpDir); err != nil {
		return fmt.Errorf("remove sandbox tmp dir: %w", err)
	}
	return nil
}

// ResetTmp removes all entries inside tmpDir but keeps the directory.
// No-op when tmpDir is empty. If tmpDir does not exist, returns nil.
func (s *Sandbox) ResetTmp() error {
	if s.tmpDir == "" {
		return nil
	}
	entries, err := os.ReadDir(s.tmpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read sandbox tmp dir: %w", err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(s.tmpDir, e.Name())); err != nil {
			return fmt.Errorf("remove sandbox tmp entry: %w", err)
		}
	}
	return nil
}
