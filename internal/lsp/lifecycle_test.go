package lsp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

const lifecycleTestTimeout = 5 * time.Second

func TestManagerSessionSurvivesRequestContextCancellation(t *testing.T) {
	tmpdir := t.TempDir()
	cacheDir := filepath.Join(tmpdir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	marker := filepath.Join(tmpdir, "go.mod")
	if err := os.WriteFile(marker, []byte(""), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("30s"),
		ReadyTimeout: config.MustDuration("500ms"),
		CacheDir:     cacheDir,
		Servers: map[string]config.LSPServerConfig{
			"go": {
				Enabled:        true,
				Command:        os.Args[0],
				Args:           []string{"-test.run=TestLSPHelperProcess"},
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
				Env:            map[string]string{helperEnv: "lsp"},
			},
		},
	}

	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ctx1, cancel1 := context.WithTimeout(context.Background(), lifecycleTestTimeout)
	defer cancel1()

	file := filepath.Join(tmpdir, "file.go")
	sess1, err := m.sessionFor(ctx1, file)
	if err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor: %v", err)
	}

	// Cancel the request context.
	cancel1()
	time.Sleep(50 * time.Millisecond)

	// A new request context should get the same session (process should still be alive).
	ctx2, cancel2 := context.WithTimeout(context.Background(), lifecycleTestTimeout)
	defer cancel2()

	sess2, err := m.sessionFor(ctx2, file)
	if err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor after context cancel: %v", err)
	}

	if sess1 != nil && sess2 != nil && sess1 != sess2 {
		t.Error("process should survive request context cancellation")
	}
}

func TestManagerReaperDoesNotKillInFlightRequests(t *testing.T) {
	tmpdir := t.TempDir()
	cacheDir := filepath.Join(tmpdir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	marker := filepath.Join(tmpdir, "go.mod")
	if err := os.WriteFile(marker, []byte(""), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("100ms"),
		ReadyTimeout: config.MustDuration("500ms"),
		CacheDir:     cacheDir,
		Servers: map[string]config.LSPServerConfig{
			"go": {
				Enabled:        true,
				Command:        os.Args[0],
				Args:           []string{"-test.run=TestLSPHelperProcess"},
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
				Env:            map[string]string{helperEnv: "lsp"},
			},
		},
	}

	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), lifecycleTestTimeout)
	defer cancel()

	file := filepath.Join(tmpdir, "file.go")

	// Spawn a session.
	sess1, err := m.sessionFor(ctx, file)
	if err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor: %v", err)
	}

	// Wait for the idle timeout to pass (session should be reaped).
	time.Sleep(250 * time.Millisecond)

	// Now call sessionFor again. The reaper may have tried to kill the session,
	// but the session should still be accessible or a new one created.
	ctx2, cancel2 := context.WithTimeout(context.Background(), lifecycleTestTimeout)
	defer cancel2()

	sess2, err := m.sessionFor(ctx2, file)
	if err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor after idle: %v", err)
	}

	// Both should be non-nil or both nil. If sess1 is non-nil, sess2 should either
	// be the same (if not reaped yet) or a new one (if reaped and respawned).
	if sess1 != nil && sess2 == nil {
		t.Error("session should either survive or respawn after idle timeout")
	}
}

func TestManagerIdleTimeoutZeroDisablesReaping(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.Duration{},
		ReadyTimeout: config.MustDuration("500ms"),
		Servers:      map[string]config.LSPServerConfig{},
	}

	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	// Should not panic; reaper should not be started.
	// This test just checks that zero timeout doesn't cause a panic.
}

func TestManagerZeroIdleTimeoutNoReaper(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.Duration{},
		ReadyTimeout: config.MustDuration("500ms"),
		Servers:      map[string]config.LSPServerConfig{},
	}

	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)

	// If ticker is nil, no goroutine is running.
	if m.reapTicker != nil {
		t.Error("reapTicker should be nil when IdleTimeout is 0")
	}

	// Close should not hang or error.
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestManagerFailedEntryRetryAfterCooldown(t *testing.T) {
	tmpdir := t.TempDir()
	cacheDir := filepath.Join(tmpdir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	spawnCount := 0
	spawnCountMu := &sync.Mutex{}

	wrapFn := func(cmd *exec.Cmd) *exec.Cmd {
		spawnCountMu.Lock()
		spawnCount++
		spawnCountMu.Unlock()
		return cmd
	}

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("100ms"),
		ReadyTimeout: config.MustDuration("100ms"),
		CacheDir:     cacheDir,
		Servers: map[string]config.LSPServerConfig{
			"missing": {
				Enabled:        true,
				Command:        "/nonexistent/lsp",
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
			},
		},
	}

	m := NewManager(cfg, tmpdir, wrapFn, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), lifecycleTestTimeout)
	defer cancel()

	file := filepath.Join(tmpdir, "file.go")

	// First call fails.
	_, _ = m.sessionFor(ctx, file)

	beforeCount := spawnCount

	// Second call within cooldown should not retry.
	_, _ = m.sessionFor(ctx, file)
	if spawnCount > beforeCount {
		t.Errorf("spawn retry within cooldown, before %d, after %d", beforeCount, spawnCount)
	}

	// After cooldown, should retry.
	time.Sleep(150 * time.Millisecond)
	_, _ = m.sessionFor(ctx, file)
	if spawnCount <= beforeCount {
		t.Errorf("no retry after cooldown, before %d, after %d", beforeCount, spawnCount)
	}
}
