package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/sandbox"
)

// sandboxTmpMaxAttempts bounds createSandboxTmpDir ID collision retries.
const sandboxTmpMaxAttempts = 8

// errSandboxTmpCollision reports that a generated sandbox tmp dir ID is already
// in use, so a fresh ID must be generated.
var errSandboxTmpCollision = errors.New("sandbox tmp dir id collision")

// sandboxTmpRandRead reads random bytes for sandbox tmp dir IDs; tests replace it.
var sandboxTmpRandRead = rand.Read

// cleanupSandboxTmpOrphans removes stale per-session sandbox tmp directories
// older than maxAge from parentDir and returns how many it removed.
// Best-effort: never fails the caller. Directories still locked by a live
// steiner runtime are kept.
func cleanupSandboxTmpOrphans(parentDir string, maxAge time.Duration) int {
	return sandbox.CleanupOrphans(parentDir, maxAge)
}

// pruneSandboxHomeCache trims stale entries from the private cache bound over
// the host cache for sandboxed commands. It returns how many files it removed,
// their total size in bytes, and whether the prune was skipped because the host
// cache is bound instead. Best-effort: a prune error is logged and never fails
// startup.
func pruneSandboxHomeCache(projectRoot string, bindHostCache bool) (removed int, freed int64, skipped bool) {
	if bindHostCache {
		return 0, 0, true
	}
	removed, freed, err := sandbox.PruneHomeCache(projectRoot)
	if err != nil {
		slog.Warn("prune sandbox home cache", "error", err)
	}
	return removed, freed, false
}

// cacheCleanupDetail renders the completed cache cleanup line. Removed files
// carry their freed bytes; removed sandbox tmp directories are reported
// separately because their size is unknown. With nothing removed it names the
// host-cache skip or reports "up to date".
func cacheCleanupDetail(removedFiles int, freed int64, removedTmpDirs int, pruneSkipped bool) string {
	var parts []string
	if removedFiles > 0 {
		noun := "files"
		if removedFiles == 1 {
			noun = "file"
		}
		parts = append(parts, fmt.Sprintf("%d %s, %s freed", removedFiles, noun, formatCacheBytes(freed)))
	}
	if removedTmpDirs > 0 {
		noun := "tmp dirs"
		if removedTmpDirs == 1 {
			noun = "tmp dir"
		}
		parts = append(parts, fmt.Sprintf("%d %s removed", removedTmpDirs, noun))
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	if pruneSkipped {
		return "host cache bound"
	}
	return "up to date"
}

// runSandboxCacheCleanups brackets the two best-effort startup cleanups with
// the checklist logger and records their results.
func runSandboxCacheCleanups(sl *startupLogger, projectRoot, parentDir string, bindHostCache bool) {
	sl.pending("Clearing caches")
	removedTmpDirs := cleanupSandboxTmpOrphans(parentDir, 48*time.Hour)
	removedFiles, freed, pruneSkipped := pruneSandboxHomeCache(projectRoot, bindHostCache)
	slog.Info("sandbox cache cleanup",
		"removed_files", removedFiles,
		"freed_bytes", freed,
		"removed_tmp_dirs", removedTmpDirs,
		"prune_skipped", pruneSkipped,
	)
	sl.step("Clearing caches", cacheCleanupDetail(removedFiles, freed, removedTmpDirs, pruneSkipped))
}

// formatCacheBytes renders a byte count for the cache cleanup detail line.
func formatCacheBytes(n int64) string {
	const (
		gib = 1 << 30
		mib = 1 << 20
		kib = 1 << 10
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/gib)
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/mib)
	default:
		return fmt.Sprintf("%.1f KiB", float64(n)/kib)
	}
}

// createSandboxTmpDir generates a random 8-byte hex ID and creates a fresh
// session-scoped directory at parentDir/<id>. A generated ID that already
// exists belongs to another session, so a collision draws a new ID instead of
// removing the existing directory.
func createSandboxTmpDir(parentDir string) (string, error) {
	if err := sandbox.EnsureDirectoryPath(filepath.Dir(filepath.Dir(filepath.Dir(parentDir))), parentDir); err != nil {
		return "", fmt.Errorf("unsafe sandbox tmp parent: %w", err)
	}
	for attempt := 0; attempt < sandboxTmpMaxAttempts; attempt++ {
		tmpDir, err := createSandboxTmpDirAttempt(parentDir)
		if err == nil {
			return tmpDir, nil
		}
		if !errors.Is(err, errSandboxTmpCollision) {
			return "", err
		}
	}
	return "", fmt.Errorf("create sandbox tmp dir: no unique id after %d attempts", sandboxTmpMaxAttempts)
}

// createSandboxTmpDirAttempt creates one random candidate directory, returning
// errSandboxTmpCollision when that ID is already taken.
func createSandboxTmpDirAttempt(parentDir string) (string, error) {
	var idBuf [8]byte
	if _, err := sandboxTmpRandRead(idBuf[:]); err != nil {
		return "", fmt.Errorf("generate sandbox tmp id: %w", err)
	}
	tmpDir := filepath.Join(parentDir, fmt.Sprintf("%x", idBuf[:]))
	if info, err := os.Lstat(tmpDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("unsafe sandbox tmp dir: %s is not a real directory", tmpDir)
		}
		return "", errSandboxTmpCollision
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect sandbox tmp dir: %w", err)
	}
	if err := os.Mkdir(tmpDir, 0o755); err != nil {
		if os.IsExist(err) {
			return "", errSandboxTmpCollision
		}
		return "", fmt.Errorf("create sandbox tmp dir: %w", err)
	}
	return tmpDir, nil
}

// buildRuntimeSandbox creates a Sandbox when sandboxing is enabled. Returns nil
// when cfg.Sandbox.Enabled is false (e.g. --unsafe flag was set). Returns nil
// when bwrap is unavailable (unsupported platform or missing binary). The
// returned status is "bypassed", "unavailable", or "active" respectively. sl
// receives the cache cleanup progress lines.
func buildRuntimeSandbox(cfg *config.Config, projectRoot, workDir, userHome string, sl *startupLogger) (*sandbox.Sandbox, string, error) {
	if !cfg.Sandbox.Enabled {
		return nil, "bypassed", nil
	}
	if err := sandbox.PrereqCheck(); err != nil {
		return nil, "unavailable", nil
	}

	// Session-scoped tmp directory.
	parentDir := filepath.Join(projectRoot, ".steiner", "tmp", "sandbox-tmp")

	if err := sandbox.EnsureDirectoryPath(projectRoot, parentDir); err != nil {
		return nil, "", fmt.Errorf("sandbox setup: unsafe temporary directory: %w", err)
	}
	tmpDir, err := createSandboxTmpDir(parentDir)
	if err != nil {
		return nil, "", err
	}

	s := sandbox.New(cfg.Sandbox, cfg.Permissions, projectRoot, workDir, userHome, tmpDir)
	if err := s.LockTmpDir(); err != nil {
		_ = s.Cleanup()
		return nil, "", fmt.Errorf("sandbox setup: %w", err)
	}
	if err := s.EnsureHome(); err != nil {
		_ = s.Cleanup()
		return nil, "", fmt.Errorf("sandbox setup: %w", err)
	}
	runSandboxCacheCleanups(sl, projectRoot, parentDir, cfg.Sandbox.BindHostCache)
	return s, "active", nil
}

// wireSandboxTmpWarning points the sandbox tmp warning handler at events so a
// lost /tmp reaches the sink of the run that is actually live: the TUI and
// oneshot phase runtimes swap in their own sink after the runtime is built.
func wireSandboxTmpWarning(sb *sandbox.Sandbox, events output.EventSink) {
	if sb == nil || events == nil {
		return
	}
	sb.SetTmpReprovisionHandler(sandboxTmpWarningHandler(events))
}

// sandboxTmpWarningHandler builds the callback a lost sandbox tmp dir reports
// through, split out from wireSandboxTmpWarning so the emitted diagnostic shape
// is testable without a Sandbox.
func sandboxTmpWarningHandler(events output.EventSink) func(string) {
	return func(message string) {
		events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:     "session_health",
			Scope:    output.SessionHealthScopeSandboxTmp,
			Severity: "warning",
			Notes:    []string{message},
		}))
	}
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
