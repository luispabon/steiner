package lsp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

const managerTestTimeout = 2 * time.Second

// rewindSpawnFailure ages every entry's StartedAt past spawnFailureBackoff so a
// retry is due without the test waiting out the real backoff.
func rewindSpawnFailure(t *testing.T, m *Manager) {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sessions) == 0 {
		t.Fatal("no entries to rewind")
	}
	for _, ent := range m.sessions {
		ent.mu.Lock()
		ent.state.StartedAt = time.Now().Add(-2 * spawnFailureBackoff)
		ent.mu.Unlock()
	}
}

func TestBuildServerEnv(t *testing.T) {
	cacheDir := "/cache/root"

	tests := []struct {
		name   string
		base   []string
		srvEnv map[string]string
		want   map[string]string
		absent []string
	}{
		{
			name: "inherits the process environment",
			base: []string{"PATH=/usr/bin", "MALFORMED"},
			want: map[string]string{
				"PATH":           "/usr/bin",
				"HOME":           cacheDir,
				"XDG_CACHE_HOME": cacheDir,
				"GOCACHE":        filepath.Join(cacheDir, "go"),
			},
			absent: []string{"MALFORMED"},
		},
		{
			name:   "server env overrides an inherited value",
			base:   []string{"PATH=/usr/bin"},
			srvEnv: map[string]string{"PATH": "/opt/bin"},
			want:   map[string]string{"PATH": "/opt/bin"},
		},
		{
			name:   "cache defaults yield to server env",
			base:   []string{"HOME=/home/user", "GOCACHE=/host/gocache"},
			srvEnv: map[string]string{"GOCACHE": "/srv/gocache"},
			want: map[string]string{
				"HOME":           cacheDir,
				"XDG_CACHE_HOME": cacheDir,
				"GOCACHE":        "/srv/gocache",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildServerEnv(tt.base, config.LSPServerConfig{Env: tt.srvEnv}, cacheDir)

			if !sort.StringsAreSorted(got) {
				t.Errorf("environment is not deterministically ordered: %v", got)
			}

			seen := make(map[string]string, len(got))
			for _, kv := range got {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					t.Fatalf("malformed entry %q", kv)
				}
				seen[k] = v
			}

			for k, want := range tt.want {
				if seen[k] != want {
					t.Errorf("%s = %q, want %q", k, seen[k], want)
				}
			}
			for _, k := range tt.absent {
				if _, ok := seen[k]; ok {
					t.Errorf("%s should not be present", k)
				}
			}
		})
	}
}

func TestSpawnServerInheritsEnvironment(t *testing.T) {
	t.Setenv("STEINER_LSP_TEST_INHERITED", "inherited-value")

	env := buildServerEnv(os.Environ(), config.LSPServerConfig{}, t.TempDir())

	found := false
	for _, kv := range env {
		if kv == "STEINER_LSP_TEST_INHERITED=inherited-value" {
			found = true
		}
	}
	if !found {
		t.Error("spawned server environment does not inherit steiner's environment")
	}
}

// blockingSession is a session stub whose Close signals entry and then blocks,
// so a test can hold doReap between the two sessions it reaps.
type blockingSession struct {
	entered  chan<- struct{}
	release  <-chan struct{}
	diags    chan PublishedDiagnostics
	progress chan ProgressEvent
	exited   chan struct{}
	onClose  func()
}

func newBlockingSession(entered chan<- struct{}, release <-chan struct{}) *blockingSession {
	return &blockingSession{
		entered:  entered,
		release:  release,
		diags:    make(chan PublishedDiagnostics),
		progress: make(chan ProgressEvent),
		exited:   make(chan struct{}),
	}
}

func (s *blockingSession) Definition(context.Context, string, int, int) ([]Location, error) {
	return nil, nil
}

func (s *blockingSession) References(context.Context, string, int, int, bool) ([]Location, error) {
	return nil, nil
}

func (s *blockingSession) DidOpen(context.Context, string, string, string, int32) error { return nil }

func (s *blockingSession) DidClose(context.Context, string) error { return nil }

func (s *blockingSession) Diagnostics() <-chan PublishedDiagnostics { return s.diags }

func (s *blockingSession) Progress() <-chan ProgressEvent { return s.progress }

func (s *blockingSession) Exited() <-chan struct{} { return s.exited }

func (s *blockingSession) Close(context.Context) error {
	if s.onClose != nil {
		s.onClose()
	}
	s.entered <- struct{}{}
	<-s.release
	return nil
}

// TestDoReapReadsSessionUnderLock pins that doReap snapshots ent.session while
// holding ent.mu. Two idle entries are reaped; doReap blocks closing the first
// while fresh spawns install new sessions on both, so its second read collides
// with entryFor's write unless the read is locked. Run under -race.
func TestDoReapReadsSessionUnderLock(t *testing.T) {
	tmpdir := t.TempDir()
	cacheDir := filepath.Join(tmpdir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	roots := []string{filepath.Join(tmpdir, "projA"), filepath.Join(tmpdir, "projB")}
	for _, root := range roots {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", root, err)
		}
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(""), 0o644); err != nil {
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
	defer func() { _ = m.Close() }()

	entered := make(chan struct{}, len(roots))
	release := make(chan struct{})

	for _, root := range roots {
		m.sessions[sessionKey{server: "go", root: root}] = &entry{
			state: ServerState{
				Name:      "go",
				Root:      root,
				Status:    ServerStatusReady,
				StartedAt: time.Now(),
				LastUsed:  time.Now().Add(-time.Hour),
			},
			session: newBlockingSession(entered, release),
		}
	}

	reapDone := make(chan struct{})
	go func() {
		m.doReap()
		close(reapDone)
	}()

	// doReap is now inside the first Close, with the second entry's session
	// still unread.
	select {
	case <-entered:
	case <-time.After(managerTestTimeout):
		t.Fatal("timeout waiting for doReap to start closing a session")
	}

	ctx, cancel := context.WithTimeout(context.Background(), managerTestTimeout)
	defer cancel()

	// Both entries are marked stopped by now, so each call spawns and installs a
	// new session. doReap is released without waiting for them: nothing orders
	// those writes against doReap's remaining session reads.
	var wg sync.WaitGroup
	for _, root := range roots {
		wg.Add(1)
		go func(root string) {
			defer wg.Done()
			if _, err := m.sessionFor(ctx, filepath.Join(root, "file.go")); err != nil {
				t.Errorf("sessionFor %s: %v", root, err)
			}
		}(root)
	}

	close(release)
	wg.Wait()

	select {
	case <-reapDone:
	case <-time.After(managerTestTimeout):
		t.Fatal("timeout waiting for doReap to finish")
	}
}

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
	defer func() { _ = m.Close() }()

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
	defer func() { _ = m.Close() }()

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
	defer func() { _ = m.Close() }()

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
	defer func() { _ = m.Close() }()

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
	defer func() { _ = m.Close() }()

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
	defer func() { _ = m.Close() }()

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
	defer func() { _ = m.Close() }()

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

	// After the spawn-failure backoff, should retry (but still fail, and warn again).
	rewindSpawnFailure(t, m)
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
	defer func() { _ = m.Close() }()

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

func TestManagerContextCancellationDuringSpawn(t *testing.T) {
	tmpdir := t.TempDir()
	cacheDir := filepath.Join(tmpdir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	marker := filepath.Join(tmpdir, "go.mod")
	if err := os.WriteFile(marker, []byte(""), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	spawnEntered := make(chan struct{})
	spawnGate := make(chan struct{})
	var once sync.Once

	wrapFn := func(cmd *exec.Cmd) *exec.Cmd {
		once.Do(func() {
			close(spawnEntered)
			<-spawnGate
		})
		return cmd
	}

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("30s"),
		ReadyTimeout: config.MustDuration("10s"),
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

	m := NewManager(cfg, tmpdir, wrapFn, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	file := filepath.Join(tmpdir, "file.go")

	var wg sync.WaitGroup

	// Goroutine A: starts the spawn and stalls in wrap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = m.sessionFor(ctx, file)
	}()

	// Wait for goroutine A to enter the stall point.
	<-spawnEntered

	// Goroutine B: arrives during the spawn with a short timeout.
	start := time.Now()
	bgCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := m.sessionFor(bgCtx, file)

	elapsed := time.Since(start)

	// B should exit promptly with context cancellation, not wait for the full spawn.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}

	if elapsed >= 1*time.Second {
		t.Errorf("sessionFor took %v, should have returned within ~100ms due to context timeout", elapsed)
	}

	// Release goroutine A so it can clean up.
	close(spawnGate)
	wg.Wait()
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
	defer func() { _ = m.Close() }()

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
	_ = m.Close()

	// Cache should still exist.
	if _, err := os.Stat(filepath.Join(cacheDir, "steiner", "lsp")); err != nil {
		t.Errorf("cache dir deleted or inaccessible after close: %v", err)
	}
}
