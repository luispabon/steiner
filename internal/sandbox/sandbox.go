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
	cfg         config.SandboxConfig
	root        string // absolute project root
	workDir     string // absolute agent workDir
	userHome    string // host user home
	tmpDir      string // session-scoped temp directory
	hostMounts  []config.HostMount
	permissions config.PermissionsConfig
}

// New creates a Sandbox. rootDir, workDir, userHome, and tmpDir must be absolute paths.
func New(cfg config.SandboxConfig, perms config.PermissionsConfig, hostMounts []config.HostMount, rootDir, workDir, userHome, tmpDir string) *Sandbox {
	return &Sandbox{
		cfg:         cfg,
		root:        rootDir,
		workDir:     workDir,
		userHome:    userHome,
		tmpDir:      tmpDir,
		hostMounts:  hostMounts,
		permissions: perms,
	}
}

// Enabled reports whether sandboxing is active.
func (s *Sandbox) Enabled() bool {
	return s.cfg.Enabled
}

// WrapCommand wraps cmd with bubblewrap. Returns cmd unchanged when sandbox disabled.
func (s *Sandbox) WrapCommand(cmd *exec.Cmd) *exec.Cmd {
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

	sandboxHome := filepath.Join(s.root, ".steiner", "home")
	var overlayArgs []string
	if overlay != nil {
		overlayArgs = overlay.bwrapArgs
	}
	bwrapArgs := BuildArgs(s.root, s.workDir, sandboxHome, s.userHome, s.permissions, s.hostMounts, overlayArgs, s.tmpDir)

	// Build the new Args slice: [bwrap, ...bwrap-args..., "--", original-cmd, original-args...]
	args := make([]string, 0, 1+len(bwrapArgs)+1+len(cmd.Args))
	args = append(args, "bwrap")
	args = append(args, bwrapArgs...)
	args = append(args, "--")
	args = append(args, cmd.Args...)

	wrapped := &exec.Cmd{
		Path:   bwrapPath,
		Args:   args,
		Stdin:  cmd.Stdin,
		Stdout: cmd.Stdout,
		Stderr: cmd.Stderr,
		Env:    FilterEnv(cmd.Env),
	}
	if len(cmd.ExtraFiles) > 0 {
		wrapped.ExtraFiles = append(wrapped.ExtraFiles, cmd.ExtraFiles...)
	}
	if overlay != nil {
		wrapped.ExtraFiles = append(wrapped.ExtraFiles, overlay.memfds...)
	}
	return wrapped
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
