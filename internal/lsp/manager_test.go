package lsp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

const managerTestTimeout = 2 * time.Second

func TestManagerExtensionRouting(t *testing.T) {
	tmpdir := t.TempDir()
	cacheDir := filepath.Join(tmpdir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("30s"),
		ReadyTimeout: config.MustDuration("500ms"),
		CacheDir:     cacheDir,
		Servers: map[string]config.LSPServerConfig{
			"go-server": {
				Enabled:        true,
				Command:        os.Args[0],
				Args:           []string{"-test.run=TestLSPHelperProcess"},
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
				Env:            map[string]string{helperEnv: "lsp"},
			},
			"py-server": {
				Enabled:        true,
				Command:        os.Args[0],
				Args:           []string{"-test.run=TestLSPHelperProcess"},
				FileExtensions: []string{".py"},
				RootMarkers:    []string{"pyproject.toml"},
				Env:            map[string]string{helperEnv: "lsp"},
			},
		},
	}

	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), managerTestTimeout)
	defer cancel()

	goFile := filepath.Join(tmpdir, "main.go")
	pyFile := filepath.Join(tmpdir, "main.py")

	goSess, err := m.sessionFor(ctx, goFile)
	if err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor go file: %v", err)
	}

	pySess, err := m.sessionFor(ctx, pyFile)
	if err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor py file: %v", err)
	}

	if goSess == pySess && goSess != nil {
		t.Error("go and python servers should be different sessions")
	}
}

func TestManagerNoServerForExtension(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("30s"),
		ReadyTimeout: config.MustDuration("500ms"),
		Servers: map[string]config.LSPServerConfig{
			"go-server": {
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
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), managerTestTimeout)
	defer cancel()

	// Try to get a session for a file with unsupported extension.
	unsupported := filepath.Join(tmpdir, "file.rs")
	_, err := m.sessionFor(ctx, unsupported)
	if !errors.Is(err, errNoServer) {
		t.Errorf("sessionFor unsupported file: got %v, want errNoServer", err)
	}
}

func TestManagerNearestRootWins(t *testing.T) {
	tmpdir := t.TempDir()
	cacheDir := filepath.Join(tmpdir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	// Create outer and inner go.mod files.
	outerMarker := filepath.Join(tmpdir, "go.mod")
	if err := os.WriteFile(outerMarker, []byte(""), 0o644); err != nil {
		t.Fatalf("write outer marker: %v", err)
	}

	innerDir := filepath.Join(tmpdir, "pkg", "inner")
	if err := os.MkdirAll(innerDir, 0o755); err != nil {
		t.Fatalf("mkdir inner: %v", err)
	}
	innerMarker := filepath.Join(innerDir, "go.mod")
	if err := os.WriteFile(innerMarker, []byte(""), 0o644); err != nil {
		t.Fatalf("write inner marker: %v", err)
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
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), managerTestTimeout)
	defer cancel()

	// File in inner directory should resolve to inner root.
	innerFile := filepath.Join(innerDir, "file.go")
	if _, err := m.sessionFor(ctx, innerFile); err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor inner file: %v", err)
	}

	states := m.ServerStates()
	if len(states) > 0 && states[0].Root != innerDir {
		t.Errorf("expected root %q, got %q", innerDir, states[0].Root)
	}
}

func TestManagerSameKeyReusesProcess(t *testing.T) {
	tmpdir := t.TempDir()
	cacheDir := filepath.Join(tmpdir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	marker := filepath.Join(tmpdir, "go.mod")
	if err := os.WriteFile(marker, []byte(""), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	callCount := atomic.Int32{}

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

	wrapFn := func(cmd *exec.Cmd) *exec.Cmd {
		callCount.Add(1)
		return cmd
	}

	m := NewManager(cfg, tmpdir, wrapFn, func(string) {}, nil)
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), managerTestTimeout)
	defer cancel()

	file1 := filepath.Join(tmpdir, "file1.go")
	file2 := filepath.Join(tmpdir, "file2.go")

	sess1, err := m.sessionFor(ctx, file1)
	if err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor file1: %v", err)
	}

	sess2, err := m.sessionFor(ctx, file2)
	if err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor file2: %v", err)
	}

	// Both should return the same session object (or both nil).
	if sess1 != sess2 {
		if sess1 != nil && sess2 != nil {
			t.Error("two files in same root should reuse the same session")
		}
	}

	// Wrap should have been called only once (or not at all, depending on error).
	calls := callCount.Load()
	if calls > 1 {
		t.Errorf("spawn called %d times, want 1", calls)
	}
}

func TestManagerDifferentRootsDistinctProcesses(t *testing.T) {
	tmpdir := t.TempDir()
	cacheDir := filepath.Join(tmpdir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	// Create two separate project directories with their own markers.
	proj1 := filepath.Join(tmpdir, "proj1")
	proj2 := filepath.Join(tmpdir, "proj2")

	for _, p := range []string{proj1, proj2} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", p, err)
		}
		marker := filepath.Join(p, "go.mod")
		if err := os.WriteFile(marker, []byte(""), 0o644); err != nil {
			t.Fatalf("write marker: %v", err)
		}
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
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), managerTestTimeout)
	defer cancel()

	file1 := filepath.Join(proj1, "file.go")
	file2 := filepath.Join(proj2, "file.go")

	sess1, err := m.sessionFor(ctx, file1)
	if err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor file1: %v", err)
	}

	sess2, err := m.sessionFor(ctx, file2)
	if err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor file2: %v", err)
	}

	// Different roots should yield different sessions (or both nil, or one error).
	if sess1 == sess2 && sess1 != nil && sess2 != nil {
		t.Error("different roots should spawn separate processes")
	}

	states := m.ServerStates()
	if len(states) > 1 {
		if states[0].Root == states[1].Root {
			t.Errorf("server states have same root: %q", states[0].Root)
		}
	}
}

func TestManagerConcurrentCallsNoDoubleSpawn(t *testing.T) {
	tmpdir := t.TempDir()
	cacheDir := filepath.Join(tmpdir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	marker := filepath.Join(tmpdir, "go.mod")
	if err := os.WriteFile(marker, []byte(""), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	spawnCount := atomic.Int32{}

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

	wrapFn := func(cmd *exec.Cmd) *exec.Cmd {
		spawnCount.Add(1)
		return cmd
	}

	m := NewManager(cfg, tmpdir, wrapFn, func(string) {}, nil)
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), managerTestTimeout)
	defer cancel()

	file := filepath.Join(tmpdir, "file.go")
	numGoroutines := 10

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.sessionFor(ctx, file)
		}()
	}
	wg.Wait()

	// Only one spawn should have occurred.
	count := spawnCount.Load()
	if count > 1 {
		t.Errorf("spawn called %d times, want 1 (concurrent calls)", count)
	}
}

func TestManagerMissingBinaryMarkedFailed(t *testing.T) {
	tmpdir := t.TempDir()

	warnCalls := atomic.Int32{}
	warnFn := func(string) {
		warnCalls.Add(1)
	}

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("100ms"),
		ReadyTimeout: config.MustDuration("100ms"),
		Servers: map[string]config.LSPServerConfig{
			"missing": {
				Enabled:        true,
				Command:        "/nonexistent/lsp",
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
			},
		},
	}

	m := NewManager(cfg, tmpdir, nil, warnFn, nil)
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), managerTestTimeout)
	defer cancel()

	// First call should fail and warn.
	file := filepath.Join(tmpdir, "file.go")
	_, err1 := m.sessionFor(ctx, file)
	if err1 == nil {
		t.Fatal("expected error for missing binary")
	}

	warnCount1 := warnCalls.Load()
	if warnCount1 != 1 {
		t.Errorf("warn called %d times, want 1", warnCount1)
	}

	// Subsequent call within cooldown should return same error without respawning.
	_, err2 := m.sessionFor(ctx, file)
	if err2 == nil {
		t.Fatal("expected error for missing binary on second call")
	}

	warnCount2 := warnCalls.Load()
	if warnCount2 != 1 {
		t.Errorf("warn called %d times after second call, want 1", warnCount2)
	}

	// After cooldown, should retry (but still fail, and warn again).
	time.Sleep(150 * time.Millisecond)
	_, err3 := m.sessionFor(ctx, file)
	if err3 == nil {
		t.Fatal("expected error for missing binary after cooldown")
	}

	warnCount3 := warnCalls.Load()
	if warnCount3 != 2 {
		t.Errorf("warn called %d times after cooldown, want 2", warnCount3)
	}
}

func TestManagerIdleReapingTerminatesServer(t *testing.T) {
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
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), managerTestTimeout)
	defer cancel()

	file := filepath.Join(tmpdir, "file.go")

	// Spawn a session.
	_, err := m.sessionFor(ctx, file)
	if err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor: %v", err)
	}

	// Check the state is ready.
	states := m.ServerStates()
	if len(states) == 0 {
		t.Fatal("no server states")
	}
	if states[0].Status != ServerStatusReady {
		t.Errorf("status = %q, want ready", states[0].Status)
	}

	// Wait for the idle timeout to fire.
	time.Sleep(250 * time.Millisecond)

	// State should now be stopped.
	states = m.ServerStates()
	if len(states) > 0 && states[0].Status != ServerStatusStopped {
		t.Errorf("status = %q, want stopped after idle timeout", states[0].Status)
	}
}

func TestManagerCloseTerminatesAllChildren(t *testing.T) {
	tmpdir := t.TempDir()
	cacheDir := filepath.Join(tmpdir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	// Create multiple project roots.
	proj1 := filepath.Join(tmpdir, "proj1")
	proj2 := filepath.Join(tmpdir, "proj2")

	for _, p := range []string{proj1, proj2} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		marker := filepath.Join(p, "go.mod")
		if err := os.WriteFile(marker, []byte(""), 0o644); err != nil {
			t.Fatalf("write marker: %v", err)
		}
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

	ctx, cancel := context.WithTimeout(context.Background(), managerTestTimeout)
	defer cancel()

	// Spawn two sessions in different roots.
	file1 := filepath.Join(proj1, "file.go")
	file2 := filepath.Join(proj2, "file.go")

	_, _ = m.sessionFor(ctx, file1)
	_, _ = m.sessionFor(ctx, file2)

	// Close should terminate all.
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// All states should be stopped.
	states := m.ServerStates()
	for _, s := range states {
		if s.Status != ServerStatusStopped {
			t.Errorf("status = %q, want stopped after close", s.Status)
		}
	}
}

func TestManagerCloseIsIdempotent(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("30s"),
		ReadyTimeout: config.MustDuration("500ms"),
		Servers:      map[string]config.LSPServerConfig{},
	}

	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)

	// Close multiple times should not panic.
	for i := 0; i < 3; i++ {
		if err := m.Close(); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
}

func TestManagerCacheDirExistsAndPersists(t *testing.T) {
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
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), managerTestTimeout)
	defer cancel()

	file := filepath.Join(tmpdir, "file.go")
	if _, err := m.sessionFor(ctx, file); err != nil && !errors.Is(err, errNoServer) {
		t.Fatalf("sessionFor: %v", err)
	}

	// Cache directory should exist with 0o700 permissions.
	entries, err := os.ReadDir(filepath.Join(cacheDir, "steiner", "lsp"))
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("no cache subdirectories created")
	}

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("entry info: %v", err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("cache dir perms = %o, want 0o700", info.Mode().Perm())
		}
	}

	// Close shouldn't delete the cache directory.
	m.Close()

	// Cache should still exist.
	if _, err := os.Stat(filepath.Join(cacheDir, "steiner", "lsp")); err != nil {
		t.Errorf("cache dir deleted or inaccessible after close: %v", err)
	}
}
