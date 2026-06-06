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
	workspace   string // absolute path
	userHome    string // host user home
	hostMounts  []config.HostMount
	permissions config.PermissionsConfig
}

// New creates a Sandbox. workspaceDir and userHome must be absolute paths.
func New(cfg config.SandboxConfig, perms config.PermissionsConfig, hostMounts []config.HostMount, workspaceDir, userHome string) *Sandbox {
	return &Sandbox{
		cfg:         cfg,
		workspace:   workspaceDir,
		userHome:    userHome,
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

	overlay, err := prepareSSHOverlay(sshSystemConfigPath)
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

	sandboxHome := filepath.Join(s.workspace, ".steiner", "home")
	var overlayArgs []string
	if overlay != nil {
		overlayArgs = overlay.bwrapArgs
	}
	bwrapArgs := BuildArgs(s.workspace, sandboxHome, s.userHome, s.permissions, s.hostMounts, overlayArgs)

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
	if overlay != nil {
		wrapped.ExtraFiles = append(wrapped.ExtraFiles, overlay.memfds...)
	}
	if len(cmd.ExtraFiles) > 0 {
		wrapped.ExtraFiles = append(wrapped.ExtraFiles, cmd.ExtraFiles...)
	}
	return wrapped
}

// EnsureHome creates .steiner/home/ inside workspaceDir and adds it to .gitignore.
func (s *Sandbox) EnsureHome() error {
	sandboxHome := filepath.Join(s.workspace, ".steiner", "home")
	if err := os.MkdirAll(sandboxHome, 0o755); err != nil {
		return fmt.Errorf("create sandbox home dir: %w", err)
	}

	gitignorePath := filepath.Join(s.workspace, ".gitignore")
	if err := ensureGitignoreEntry(gitignorePath, ".steiner/home/"); err != nil {
		return fmt.Errorf("update .gitignore: %w", err)
	}
	return nil
}

// ensureGitignoreEntry adds entry to the gitignore file if not already present.
func ensureGitignoreEntry(path, entry string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read gitignore: %w", err)
	}

	content := string(data)
	for _, line := range splitLines(content) {
		if line == entry {
			return nil
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open gitignore: %w", err)
	}
	defer f.Close() //nolint:errcheck

	_, err = fmt.Fprintf(f, "%s\n", entry)
	return err
}

// splitLines splits s into lines, trimming trailing newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
