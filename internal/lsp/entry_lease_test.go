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

// newCountingSession returns a blocking session whose Close records that it was
// called and returns immediately.
func newCountingSession(closeCount *atomic.Int32) *blockingSession {
	released := make(chan struct{})
	close(released)
	return &blockingSession{
		entered:  make(chan struct{}, 1),
		release:  released,
		diags:    make(chan PublishedDiagnostics),
		progress: make(chan ProgressEvent),
		exited:   make(chan struct{}),
		onClose: func() {
			closeCount.Add(1)
		},
	}
}

// TestDoReapSkipsLeasedEntry pins that an entry past idle_timeout survives the
// reaper while a document cycle holds its lease, and is reaped once the cycle
// releases it.
func TestDoReapSkipsLeasedEntry(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpdir, "go.mod"), []byte(""), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("1m"),
		ReadyTimeout: config.MustDuration("500ms"),
		Servers: map[string]config.LSPServerConfig{
			"go": {Enabled: true, FileExtensions: []string{".go"}, RootMarkers: []string{"go.mod"}},
		},
	}

	m := NewManager(cfg, tmpdir, nil, ManagerOptions{})
	defer func() { _ = m.Close() }()

	entered := make(chan struct{}, 1)
	released := make(chan struct{})
	close(released)
	live := newBlockingSession(entered, released)

	ent := &entry{
		state: ServerState{
			Name: "go", Root: tmpdir, Status: ServerStatusReady,
			StartedAt: time.Now(), LastUsed: time.Now(),
		},
		session: live,
	}
	m.mu.Lock()
	m.sessions[sessionKey{server: "go", root: tmpdir}] = ent
	m.mu.Unlock()

	file := filepath.Join(tmpdir, "file.go")
	gotEnt, sess, lease, err := m.acquireEntry(context.Background(), file)
	if err != nil {
		t.Fatalf("acquireEntry: %v", err)
	}
	if gotEnt != ent || sess != session(live) {
		t.Fatalf("acquireEntry = (%v, %v), want the injected ready entry and session", gotEnt, sess)
	}

	ent.mu.Lock()
	ent.state.LastUsed = time.Now().Add(-time.Hour) // long past idle_timeout, but leased
	ent.mu.Unlock()

	m.doReap()

	select {
	case <-entered:
		t.Fatal("idle reaper closed a leased session")
	default:
	}
	ent.mu.Lock()
	status := ent.state.Status
	ent.mu.Unlock()
	if status != ServerStatusReady {
		t.Fatalf("leased entry status = %q, want ready", status)
	}

	// Releasing refreshes LastUsed and drops the lease, so a genuinely idle
	// entry is reaped on the next pass.
	lease.Release()
	ent.mu.Lock()
	ent.state.LastUsed = time.Now().Add(-time.Hour)
	leases := ent.leases
	ent.mu.Unlock()
	if leases != 0 {
		t.Fatalf("leases after Release = %d, want 0", leases)
	}

	m.doReap()

	select {
	case <-entered:
	case <-time.After(managerTestTimeout):
		t.Fatal("idle reaper did not close the released idle session")
	}
	ent.mu.Lock()
	status = ent.state.Status
	ent.mu.Unlock()
	if status != ServerStatusStopped {
		t.Errorf("status after reap = %q, want stopped", status)
	}
}

// TestCompleteSpawnRejectsSessionAfterClose pins that a session whose spawn
// finishes after Close started is never published: the entry stays stopped, the
// waiters wake, and the late session is closed.
func TestCompleteSpawnRejectsSessionAfterClose(t *testing.T) {
	t.Parallel()
	m := NewManager(config.LSPConfig{}, t.TempDir(), nil, ManagerOptions{})
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var closeCount atomic.Int32
	late := newCountingSession(&closeCount)
	ent := &entry{state: ServerState{Name: "go", Status: ServerStatusStarting}}
	ready := make(chan struct{})

	lease, err := m.completeSpawn(ent, late, newReadiness(), nil, ready)
	if !errors.Is(err, errManagerClosed) {
		t.Fatalf("completeSpawn error = %v, want errManagerClosed", err)
	}
	if lease != nil {
		t.Error("completeSpawn returned a lease for a rejected session")
	}
	if closeCount.Load() != 1 {
		t.Errorf("late session closed %d times, want 1", closeCount.Load())
	}

	select {
	case <-ready:
	default:
		t.Error("completeSpawn did not wake the waiters on ready")
	}

	ent.mu.Lock()
	status, sess := ent.state.Status, ent.session
	ent.mu.Unlock()
	if status != ServerStatusStopped {
		t.Errorf("rejected entry status = %q, want stopped", status)
	}
	if sess != nil {
		t.Error("rejected session was published on the entry")
	}
}

// TestAcquireEntryAfterClose pins that a lookup after shutdown fails fast with
// errManagerClosed instead of inserting an entry or spawning a server.
func TestAcquireEntryAfterClose(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()
	cfg := config.LSPConfig{
		Enabled: true,
		Servers: map[string]config.LSPServerConfig{
			"go": {Enabled: true, FileExtensions: []string{".go"}},
		},
	}

	m := NewManager(cfg, tmpdir, nil, ManagerOptions{})
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, _, lease, err := m.acquireEntry(context.Background(), filepath.Join(tmpdir, "file.go"))
	if !errors.Is(err, errManagerClosed) {
		t.Fatalf("acquireEntry error = %v, want errManagerClosed", err)
	}
	lease.Release() // must be nil-safe: callers defer it before checking err

	m.mu.Lock()
	entries := len(m.sessions)
	m.mu.Unlock()
	if entries != 0 {
		t.Errorf("acquireEntry inserted %d entries after Close", entries)
	}
}

// TestManagerCloseDuringSpawnRejectsLateSession pins that a spawn still in
// flight when Close runs does not block shutdown, and that its session is never
// published: the waiting call returns errManagerClosed and the entry stays
// stopped.
func TestManagerCloseDuringSpawnRejectsLateSession(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()
	cacheDir := filepath.Join(tmpdir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpdir, "go.mod"), []byte(""), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	spawnEntered := make(chan struct{})
	spawnGate := make(chan struct{})
	var enteredOnce sync.Once

	wrapFn := func(cmd *exec.Cmd) (*exec.Cmd, error) {
		enteredOnce.Do(func() { close(spawnEntered) })
		<-spawnGate
		return cmd, nil
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

	m := NewManager(cfg, tmpdir, wrapFn, ManagerOptions{})

	type spawnResult struct {
		sess  session
		lease *entryLease
		err   error
	}
	done := make(chan spawnResult, 1)
	go func() {
		_, sess, lease, err := m.acquireEntry(context.Background(), filepath.Join(tmpdir, "file.go"))
		done <- spawnResult{sess: sess, lease: lease, err: err}
	}()

	select {
	case <-spawnEntered:
	case <-time.After(lifecycleTestTimeout):
		t.Fatal("timeout waiting for the spawn to start")
	}

	// Close must not wait for the gated spawn.
	closeDone := make(chan error, 1)
	go func() { closeDone <- m.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(managerTestTimeout):
		t.Fatal("Close blocked on an in-flight spawn")
	}

	close(spawnGate)

	var res spawnResult
	select {
	case res = <-done:
	case <-time.After(lifecycleTestTimeout):
		t.Fatal("timeout waiting for the rejected spawn to return")
	}

	if !errors.Is(res.err, errManagerClosed) {
		t.Fatalf("acquireEntry error = %v, want errManagerClosed", res.err)
	}
	if res.sess != nil || res.lease != nil {
		t.Errorf("rejected spawn returned session %v and lease %v, want nil", res.sess, res.lease)
	}

	m.mu.Lock()
	ent := m.sessions[sessionKey{server: "go", root: tmpdir}]
	m.mu.Unlock()
	if ent == nil {
		t.Fatal("no entry recorded for the rejected spawn")
	}
	ent.mu.Lock()
	status, sess := ent.state.Status, ent.session
	ent.mu.Unlock()
	if status == ServerStatusReady {
		t.Error("late spawn published a ready entry after shutdown")
	}
	if sess != nil {
		t.Error("late spawn published its session after shutdown")
	}
}

// TestCloseJoinsReaperAndConcurrentClose pins that Close waits for an in-flight
// reap to finish its session close, and that concurrent Close calls are safe and
// all wait for that shutdown.
func TestCloseJoinsReaperAndConcurrentClose(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()
	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("100ms"),
		ReadyTimeout: config.MustDuration("500ms"),
		Servers: map[string]config.LSPServerConfig{
			"go": {Enabled: true, FileExtensions: []string{".go"}},
		},
	}

	m := NewManager(cfg, tmpdir, nil, ManagerOptions{})

	var closeCount atomic.Int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	idle := &blockingSession{
		entered:  entered,
		release:  release,
		diags:    make(chan PublishedDiagnostics),
		progress: make(chan ProgressEvent),
		exited:   make(chan struct{}),
		onClose: func() {
			closeCount.Add(1)
		},
	}

	ent := &entry{
		state: ServerState{
			Name: "go", Root: tmpdir, Status: ServerStatusReady,
			StartedAt: time.Now(), LastUsed: time.Now().Add(-time.Hour),
		},
		session: idle,
	}
	m.mu.Lock()
	m.sessions[sessionKey{server: "go", root: tmpdir}] = ent
	m.mu.Unlock()

	// The reaper starts closing the idle session and blocks inside Close.
	select {
	case <-entered:
	case <-time.After(lifecycleTestTimeout):
		t.Fatal("timeout waiting for the idle reaper to start closing the session")
	}

	const closers = 4
	var wg sync.WaitGroup
	returned := make(chan struct{}, closers)
	for range closers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
			returned <- struct{}{}
		}()
	}

	select {
	case <-returned:
		t.Fatal("Close returned while the reaper's session close was still in progress")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	wg.Wait()

	if closeCount.Load() != 1 {
		t.Errorf("session closed %d times, want 1", closeCount.Load())
	}
	ent.mu.Lock()
	status := ent.state.Status
	ent.mu.Unlock()
	if status != ServerStatusStopped {
		t.Errorf("status after close = %q, want stopped", status)
	}
}

// newLeasedReadyEntry installs a Ready entry for the given session and returns
// the manager plus what acquireEntry handed out.
func newLeasedReadyEntry(t *testing.T, sess *blockingSession, r *readiness) (*Manager, *entry, session, *entryLease) {
	t.Helper()
	tmpdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpdir, "go.mod"), []byte(""), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("1m"),
		ReadyTimeout: config.MustDuration("500ms"),
		Servers: map[string]config.LSPServerConfig{
			"go": {Enabled: true, FileExtensions: []string{".go"}, RootMarkers: []string{"go.mod"}},
		},
	}
	m := NewManager(cfg, tmpdir, nil, ManagerOptions{})

	ent := &entry{
		state: ServerState{
			Name: "go", Root: tmpdir, Status: ServerStatusReady,
			StartedAt: time.Now(), LastUsed: time.Now(),
		},
		session:   sess,
		readiness: r,
	}
	m.mu.Lock()
	m.sessions[sessionKey{server: "go", root: tmpdir}] = ent
	m.mu.Unlock()

	gotEnt, live, lease, err := m.acquireEntry(context.Background(), filepath.Join(tmpdir, "file.go"))
	if err != nil {
		t.Fatalf("acquireEntry: %v", err)
	}
	return m, gotEnt, live, lease
}

// TestAwaitReadyWithRetiredSession pins that readiness waiting uses the session
// the caller acquired. A concurrent crashed-session retirement clears
// entry.session while leaving the dead session's readiness installed, which used
// to make awaitReady call Exited on a nil session.
func TestAwaitReadyWithRetiredSession(t *testing.T) {
	t.Parallel()
	var closeCount atomic.Int32
	r := newReadiness()
	r.markReady()

	m, ent, sess, lease := newLeasedReadyEntry(t, newCountingSession(&closeCount), r)
	defer func() {
		lease.Release()
		_ = m.Close()
	}()
	if sess == nil {
		t.Fatal("acquireEntry returned a nil session")
	}

	// Retirement by a concurrent caller (readyEntry) or by Close.
	ent.mu.Lock()
	ent.session = nil
	ent.state.Status = ServerStatusStopped
	ent.mu.Unlock()

	incomplete, err := m.awaitReadySession(context.Background(), ent, sess)
	if err != nil {
		t.Fatalf("awaitReadySession: %v", err)
	}
	if incomplete {
		t.Error("awaitReadySession returned incomplete=true, want false")
	}
}

// TestAwaitReadyObservesAcquiredSessionExit pins that an exit observed through
// the acquired session is reported even after the entry dropped that session.
func TestAwaitReadyObservesAcquiredSessionExit(t *testing.T) {
	t.Parallel()
	var closeCount atomic.Int32
	live := newCountingSession(&closeCount)

	m, ent, sess, lease := newLeasedReadyEntry(t, live, newReadiness())
	defer func() {
		lease.Release()
		_ = m.Close()
	}()

	ent.mu.Lock()
	ent.session = nil
	ent.state.Status = ServerStatusStopped
	ent.mu.Unlock()
	close(live.exited)

	if _, err := m.awaitReadySession(context.Background(), ent, sess); !errors.Is(err, errServerExited) {
		t.Fatalf("awaitReadySession error = %v, want errServerExited", err)
	}
}

// TestClaimSpawnRejectsAfterClose pins that a spawn cannot be claimed once
// shutdown started: the closing check and the Starting transition share the
// manager lock, so no server is spawned after Close.
func TestClaimSpawnRejectsAfterClose(t *testing.T) {
	t.Parallel()
	m := NewManager(config.LSPConfig{}, t.TempDir(), nil, ManagerOptions{})
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ent := &entry{state: ServerState{Name: "go", Status: ServerStatusDeclared}}
	ready, claimed, err := m.claimSpawn(ent)
	if !errors.Is(err, errManagerClosed) {
		t.Fatalf("claimSpawn error = %v, want errManagerClosed", err)
	}
	if claimed || ready != nil {
		t.Errorf("claimSpawn returned claimed=%v ready=%v after Close", claimed, ready)
	}

	ent.mu.Lock()
	status := ent.state.Status
	ent.mu.Unlock()
	if status == ServerStatusStarting {
		t.Error("claimSpawn moved the entry to starting after Close")
	}
}
