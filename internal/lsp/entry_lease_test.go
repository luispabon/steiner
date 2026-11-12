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

	incomplete, err := m.awaitReadySession(context.Background(), lease.readiness, sess)
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

	if _, err := m.awaitReadySession(context.Background(), lease.readiness, sess); !errors.Is(err, errServerExited) {
		t.Fatalf("awaitReadySession error = %v, want errServerExited", err)
	}
}

// TestAwaitReadyUsesLeasedReadiness pins that readiness waiting observes the
// tracker captured with the leased session. A later respawn replaces the entry's
// readiness; waiting on that tracker would block until the wrong session loaded.
func TestAwaitReadyUsesLeasedReadiness(t *testing.T) {
	t.Parallel()
	var closeCount atomic.Int32
	first := newReadiness()

	m, ent, sess, lease := newLeasedReadyEntry(t, newCountingSession(&closeCount), first)
	defer func() {
		lease.Release()
		_ = m.Close()
	}()

	// A later respawn publishes a new session and replaces the tracker, as
	// completeSpawn does under ent.mu.
	respawned := newReadiness()
	ent.mu.Lock()
	ent.session = newCountingSession(&closeCount)
	ent.readiness = respawned
	ent.mu.Unlock()

	// Only the leased tracker is determined; the respawn's is still open.
	first.markReady()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	incomplete, err := m.awaitReadySession(ctx, lease.readiness, sess)
	if err != nil {
		t.Fatalf("awaitReadySession: %v (waited on a later respawn's readiness)", err)
	}
	if incomplete {
		t.Error("awaitReadySession returned incomplete=true, want false")
	}
}

// gatedRequestSession is a session double whose Definition request blocks until
// the test releases it, so one document cycle can be held open while another
// queues on cycleMu.
type gatedRequestSession struct {
	*readinessTestSession
	entered     chan<- struct{}
	release     <-chan struct{}
	shutdownCnt *atomic.Int32
}

func (s *gatedRequestSession) Definition(ctx context.Context, _ string, _, _ int) ([]Location, error) {
	s.entered <- struct{}{}
	select {
	case <-s.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *gatedRequestSession) Close(context.Context) error {
	s.shutdownCnt.Add(1)
	return nil
}

// TestActiveAndQueuedCyclesSurviveReap pins that a reap pass leaves a session
// alone while one document cycle is inside a request and another is queued on
// cycleMu, and that both cycles then complete against the same session.
func TestActiveAndQueuedCyclesSurviveReap(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpdir, "go.mod"), []byte(""), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	file := filepath.Join(tmpdir, "file.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.LSPConfig{
		Enabled:        true,
		IdleTimeout:    config.MustDuration("50ms"),
		ReadyTimeout:   config.MustDuration("500ms"),
		RequestTimeout: config.MustDuration("5s"),
		Servers: map[string]config.LSPServerConfig{
			"go": {Enabled: true, FileExtensions: []string{".go"}, RootMarkers: []string{"go.mod"}},
		},
	}
	m := NewManager(cfg, tmpdir, nil, ManagerOptions{})
	defer func() { _ = m.Close() }()

	var closeCount atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	live := &gatedRequestSession{
		readinessTestSession: &readinessTestSession{
			progress: make(chan ProgressEvent),
			exited:   make(chan struct{}),
		},
		entered:     entered,
		release:     release,
		shutdownCnt: &closeCount,
	}

	ready := newReadiness()
	ready.markReady()
	ent := &entry{
		state: ServerState{
			Name: "go", Root: tmpdir, Status: ServerStatusReady,
			StartedAt: time.Now(), LastUsed: time.Now(),
		},
		session:   live,
		readiness: ready,
	}
	m.mu.Lock()
	m.sessions[sessionKey{server: "go", root: tmpdir}] = ent
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), lifecycleTestTimeout)
	defer cancel()

	type cycleResult struct {
		result Result
		err    error
	}

	// Active cycle: holds a lease and cycleMu and blocks inside the request.
	active := make(chan cycleResult, 1)
	go func() {
		res, err := m.Definitions(ctx, file, 1, 1)
		active <- cycleResult{result: res, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(managerTestTimeout):
		t.Fatal("timeout waiting for the active cycle to enter its request")
	}

	// Queued cycle: takes its lease, then waits on cycleMu behind the active one.
	queued := make(chan cycleResult, 1)
	go func() {
		res, err := m.Definitions(ctx, file, 2, 1)
		queued <- cycleResult{result: res, err: err}
	}()
	waitForLeases(t, ent, 2)

	// The entry is far past idle_timeout, but both cycles must pin it.
	ent.mu.Lock()
	ent.state.LastUsed = time.Now().Add(-time.Hour)
	ent.mu.Unlock()

	m.doReap()

	if closeCount.Load() != 0 {
		t.Fatalf("reaper closed a session with active and queued document cycles")
	}
	ent.mu.Lock()
	status := ent.state.Status
	ent.mu.Unlock()
	if status != ServerStatusReady {
		t.Fatalf("status during the cycles = %q, want ready", status)
	}

	close(release)

	for name, done := range map[string]chan cycleResult{"active": active, "queued": queued} {
		select {
		case res := <-done:
			if res.err != nil {
				t.Errorf("%s cycle: %v", name, res.err)
			}
		case <-time.After(managerTestTimeout):
			t.Fatalf("%s cycle did not finish", name)
		}
	}

	if closeCount.Load() != 0 {
		t.Errorf("session was closed %d times during the cycles, want 0", closeCount.Load())
	}
}

// waitForLeases waits until ent reports want outstanding leases.
func waitForLeases(t *testing.T, ent *entry, want int) {
	t.Helper()
	deadline := time.Now().Add(managerTestTimeout)
	for {
		ent.mu.Lock()
		leases := ent.leases
		ent.mu.Unlock()
		if leases == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("entry leases = %d, want %d", leases, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitForLastUsedAfter waits until ent's LastUsed advances past before, which is
// how a lookup that finds the entry records its arrival.
func waitForLastUsedAfter(t *testing.T, ent *entry, before time.Time) {
	t.Helper()
	deadline := time.Now().Add(managerTestTimeout)
	for {
		ent.mu.Lock()
		lastUsed := ent.state.LastUsed
		ent.mu.Unlock()
		if lastUsed.After(before) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the second caller never reached the entry lookup")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestCompleteSpawnFailsWithoutSession pins that a transport reporting success
// with no session fails the spawn instead of publishing a ready entry whose
// session is nil.
func TestCompleteSpawnFailsWithoutSession(t *testing.T) {
	t.Parallel()
	m := NewManager(config.LSPConfig{}, t.TempDir(), nil, ManagerOptions{})
	defer func() { _ = m.Close() }()

	ent := &entry{state: ServerState{Name: "go", Status: ServerStatusStarting}}
	ready := make(chan struct{})

	lease, err := m.completeSpawn(ent, nil, newReadiness(), nil, ready)
	if !errors.Is(err, errNoSpawnedSession) {
		t.Fatalf("completeSpawn error = %v, want errNoSpawnedSession", err)
	}
	if lease != nil {
		t.Error("completeSpawn returned a lease for a missing session")
	}

	ent.mu.Lock()
	status, sess, stateErr := ent.state.Status, ent.session, ent.state.Err
	ent.mu.Unlock()
	if status != ServerStatusFailed {
		t.Errorf("status = %q, want failed", status)
	}
	if sess != nil {
		t.Error("a nil session was published on the entry")
	}
	if !errors.Is(stateErr, errNoSpawnedSession) {
		t.Errorf("entry error = %v, want errNoSpawnedSession", stateErr)
	}

	select {
	case <-ready:
	default:
		t.Error("completeSpawn did not wake the waiters on ready")
	}
}

// TestStartingWaiterLeasesPublishedSession pins that a caller that waited on an
// in-flight spawn gets a lease on the session the spawn published: both holders
// pin the same session and readiness tracker.
func TestStartingWaiterLeasesPublishedSession(t *testing.T) {
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
		ReadyTimeout: config.MustDuration("5s"),
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
	defer func() { _ = m.Close() }()

	type acquired struct {
		sess  session
		lease *entryLease
		err   error
	}
	file := filepath.Join(tmpdir, "file.go")

	first := make(chan acquired, 1)
	go func() {
		_, sess, lease, err := m.acquireEntry(context.Background(), file)
		first <- acquired{sess: sess, lease: lease, err: err}
	}()
	select {
	case <-spawnEntered:
	case <-time.After(lifecycleTestTimeout):
		t.Fatal("timeout waiting for the spawn to start")
	}

	// Snapshot LastUsed before the second caller runs: lookupEntry refreshes it,
	// so that refresh observes the waiter has entered the lookup and is about to
	// wait on the entry's ready channel. Publication is still gated, so the entry
	// is Starting for it.
	m.mu.Lock()
	ent := m.sessions[sessionKey{server: "go", root: tmpdir}]
	m.mu.Unlock()
	if ent == nil {
		t.Fatal("no entry recorded for the in-flight spawn")
	}
	ent.mu.Lock()
	before := ent.state.LastUsed
	ent.mu.Unlock()

	second := make(chan acquired, 1)
	go func() {
		_, sess, lease, err := m.acquireEntry(context.Background(), file)
		second <- acquired{sess: sess, lease: lease, err: err}
	}()
	waitForLastUsedAfter(t, ent, before)

	close(spawnGate)

	var got [2]acquired
	for i, ch := range []chan acquired{first, second} {
		select {
		case got[i] = <-ch:
		case <-time.After(lifecycleTestTimeout):
			t.Fatalf("timeout waiting for caller %d", i+1)
		}
	}

	for i, a := range got {
		if a.err != nil {
			t.Fatalf("caller %d: %v", i+1, a.err)
		}
		if a.sess == nil || a.lease == nil {
			t.Fatalf("caller %d returned session %v lease %v, want both non-nil", i+1, a.sess, a.lease)
		}
		defer a.lease.Release()
	}
	if got[0].sess != got[1].sess {
		t.Error("waiter leased a different session than the spawn published")
	}
	if got[0].lease.readiness != got[1].lease.readiness {
		t.Error("waiter leased a different readiness tracker than the spawn published")
	}

	m.mu.Lock()
	leases := ent.leases
	m.mu.Unlock()
	if leases != 2 {
		t.Errorf("entry leases = %d, want 2 (both callers)", leases)
	}
}
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
