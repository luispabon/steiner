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

func TestManagerClosePreservesProcessContextForGracefulSession(t *testing.T) {
	m := NewManager(config.LSPConfig{}, t.TempDir(), nil, func(string) {}, nil)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	closeChecked := make(chan struct{})
	m.sessions[sessionKey{server: "test", root: t.TempDir()}] = &entry{
		state: ServerState{Status: ServerStatusReady},
		session: &blockingSession{
			entered:  entered,
			release:  release,
			diags:    make(chan PublishedDiagnostics),
			progress: make(chan ProgressEvent),
			exited:   make(chan struct{}),
			onClose: func() {
				if err := m.spawnCtx.Err(); !errors.Is(err, context.Canceled) {
					t.Errorf("spawn context error = %v, want context canceled", err)
				}
				if err := m.mgrCtx.Err(); err != nil {
					t.Errorf("process context canceled before graceful close: %v", err)
				}
				close(closeChecked)
			},
		},
	}

	closeDone := make(chan struct{})
	go func() {
		if err := m.Close(); err != nil {
			t.Errorf("manager close: %v", err)
		}
		close(closeDone)
	}()

	select {
	case <-closeChecked:
	case <-time.After(lifecycleTestTimeout):
		t.Fatal("timeout waiting for graceful session close")
	}
	close(release)

	select {
	case <-closeDone:
	case <-time.After(lifecycleTestTimeout):
		t.Fatal("timeout waiting for manager close")
	}
	if err := m.mgrCtx.Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("process context error = %v, want context canceled after cleanup", err)
	}
}

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
	_, sess1, err := m.entryFor(ctx1, file)
	if err != nil {
		t.Fatalf("entryFor: %v", err)
	}
	if sess1 == nil {
		t.Fatal("entryFor returned a nil session")
	}

	// Cancel the request context. The ready server must remain alive.
	cancel1()
	select {
	case <-sess1.Exited():
		t.Fatal("server exited when the handshake context was cancelled")
	case <-time.After(100 * time.Millisecond):
	}

	// A new request context should get the same session (process should still be alive).
	ctx2, cancel2 := context.WithTimeout(context.Background(), lifecycleTestTimeout)
	defer cancel2()

	_, sess2, err := m.entryFor(ctx2, file)
	if err != nil {
		t.Fatalf("entryFor after context cancel: %v", err)
	}

	if sess1 != sess2 {
		t.Error("process should survive request context cancellation")
	}

	if err := m.Close(); err != nil {
		t.Fatalf("manager close: %v", err)
	}
	select {
	case <-sess1.Exited():
	case <-time.After(lifecycleTestTimeout):
		t.Fatal("server did not exit after manager close")
	}
}

// TestManagerIdleReapRespawnsSession pins the idle-reaping behavior this test
// actually exercises: an idle session is closed (its process exits) and a
// later request for the same file spawns a fresh, usable session. It does
// not exercise in-flight request survival, since the manager has no
// mechanism that tracks or protects requests in flight against the reaper -
// only entry.LastUsed, refreshed at the start of entryFor, guards reaping.
func TestManagerIdleReapRespawnsSession(t *testing.T) {
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
	_, sess1, err := m.entryFor(ctx, file)
	requireSessionStarted(t, sess1, err)

	// Wait for the idle timeout to pass; the reaper should close sess1.
	select {
	case <-sess1.Exited():
	case <-time.After(lifecycleTestTimeout):
		t.Fatal("timeout waiting for idle-reaped session to exit")
	}

	// A new request for the same file should spawn a fresh, usable session.
	ctx2, cancel2 := context.WithTimeout(context.Background(), lifecycleTestTimeout)
	defer cancel2()

	_, sess2, err := m.entryFor(ctx2, file)
	requireSessionStarted(t, sess2, err)

	if sess1 == sess2 {
		t.Error("expected a new session after idle reap, got the reaped one")
	}
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
	_, _, _ = m.entryFor(ctx, file)

	beforeCount := spawnCount

	// Second call within cooldown should not retry.
	_, _, _ = m.entryFor(ctx, file)
	if spawnCount > beforeCount {
		t.Errorf("spawn retry within cooldown, before %d, after %d", beforeCount, spawnCount)
	}

	// After the spawn-failure backoff, should retry.
	rewindSpawnFailure(t, m)
	_, _, _ = m.entryFor(ctx, file)
	if spawnCount <= beforeCount {
		t.Errorf("no retry after cooldown, before %d, after %d", beforeCount, spawnCount)
	}
}
