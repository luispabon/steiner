package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/sandbox"
)

// cleanupSandboxTmpOrphans removes stale per-session sandbox tmp directories
// older than maxAge from parentDir. Best-effort: never fails the caller.
// Emits start/done status to stderr when there is work to do, since removing
// large trees (e.g. Go module caches) can take a while.
func cleanupSandboxTmpOrphans(parentDir string, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	var oldCount int
	if entries, err := os.ReadDir(parentDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if fi, err := e.Info(); err == nil && fi.ModTime().Before(cutoff) {
				oldCount++
			}
		}
	}
	if oldCount == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "Cleaning up %d old sandbox tmp director%s...\n", oldCount, pluralSuffix(oldCount))
	sandbox.CleanupOrphans(parentDir, maxAge)
	fmt.Fprintf(os.Stderr, "Done.\n")
}

func pluralSuffix(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// createSandboxTmpDir generates a random 8-byte hex ID and creates a fresh
// session-scoped directory at parentDir/<id>. If a directory with the
// generated ID already exists (collision), it is removed and recreated.
func createSandboxTmpDir(parentDir string) (string, error) {
	var idBuf [8]byte
	if _, err := rand.Read(idBuf[:]); err != nil {
		return "", fmt.Errorf("generate sandbox tmp id: %w", err)
	}
	id := fmt.Sprintf("%x", idBuf[:])
	tmpDir := filepath.Join(parentDir, id)
	if _, err := os.Stat(tmpDir); err == nil {
		if err := os.RemoveAll(tmpDir); err != nil {
			return "", fmt.Errorf("remove stale sandbox tmp dir: %w", err)
		}
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", fmt.Errorf("create sandbox tmp dir: %w", err)
	}
	return tmpDir, nil
}

// buildRuntimeSandbox creates a Sandbox when sandboxing is enabled. Returns nil
// when cfg.Sandbox.Enabled is false (e.g. --unsafe flag was set). Returns nil
// when bwrap is unavailable (unsupported platform or missing binary). The
// returned status is "bypassed", "unavailable", or "active" respectively.
func buildRuntimeSandbox(cfg *config.Config, projectRoot, workDir, userHome string) (*sandbox.Sandbox, string, error) {
	if !cfg.Sandbox.Enabled {
		return nil, "bypassed", nil
	}
	if err := sandbox.PrereqCheck(); err != nil {
		return nil, "unavailable", nil
	}

	// Session-scoped tmp directory.
	parentDir := filepath.Join(projectRoot, ".steiner", "tmp", "sandbox-tmp")

	cleanupSandboxTmpOrphans(parentDir, 48*time.Hour)
	tmpDir, err := createSandboxTmpDir(parentDir)
	if err != nil {
		return nil, "", err
	}

	s := sandbox.New(cfg.Sandbox, cfg.Permissions, projectRoot, workDir, userHome, tmpDir)
	if err := s.EnsureHome(); err != nil {
		_ = s.Cleanup()
		return nil, "", fmt.Errorf("sandbox setup: %w", err)
	}
	return s, "active", nil
}

// emitSandboxWarning emits a SandboxStatusEvent when sandbox is not active and
// WarningOnUnsupportedPlatform is enabled, and independently when the sandbox
// is active but env_passthrough_all disables the credential barrier.
func emitSandboxWarning(cfg config.Config, status string, events output.EventSink) {
	if cfg.Sandbox.Enabled && cfg.Sandbox.EnvPassthroughAll {
		events.Emit(output.NewSandboxStatusEvent(status, "sandbox env_passthrough_all is enabled: the credential barrier is disabled and the full host environment (including credentials) is passed to sandboxed processes unfiltered."))
	}
	if status == "active" || !cfg.Sandbox.WarningOnUnsupportedPlatform {
		return
	}
	var msg string
	switch status {
	case "unavailable":
		msg = fmt.Sprintf("sandbox unavailable: bubblewrap is not supported on %s. Bash and subprocess tools run unsandboxed.", runtime.GOOS)
	case "bypassed":
		switch cfg.Sandbox.DisabledBy {
		case config.SandboxDisabledByCLIUnsafe:
			msg = "sandbox bypassed by --unsafe. Bash and subprocess tools run unsandboxed."
		case config.SandboxDisabledByProjectConfig:
			msg = "sandbox bypassed by sandbox.enabled=false in the project config. Bash and subprocess tools run unsandboxed."
		case config.SandboxDisabledByGlobalConfig:
			msg = "sandbox bypassed by sandbox.enabled=false in the global config. Bash and subprocess tools run unsandboxed."
		default:
			msg = "sandbox bypassed: running with --unsafe or sandbox.enabled=false. Bash and subprocess tools run unsandboxed."
		}
	}
	if msg != "" {
		events.Emit(output.NewSandboxStatusEvent(status, msg))
	}
}

// emitProjectContextDeprecationWarning warns when the legacy
// project_context.max_tokens key is set. The event is advisory only and never
// carries sandbox status.
func emitProjectContextDeprecationWarning(cfg config.Config, events output.EventSink) {
	for _, msg := range projectContextConfigWarnings(cfg) {
		events.Emit(output.NewConfigWarningEvent(msg))
	}
}

// projectContextConfigWarnings returns the user-facing warnings for deprecated
// project_context config keys, empty when none apply.
func projectContextConfigWarnings(cfg config.Config) []string {
	if cfg.ProjectContext.MaxTokens == 0 {
		return nil
	}
	return []string{"project_context.max_tokens is deprecated; use max_bytes (converted as max_tokens x 4)"}
}
