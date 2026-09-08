package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"go.lsp.dev/protocol"

	"github.com/luispabon/steiner/internal/config"
)

type readinessTestSession struct {
	progress chan ProgressEvent
	exited   chan struct{}
}

func (s *readinessTestSession) Definition(context.Context, string, int, int) ([]Location, error) {
	return nil, nil
}

func (s *readinessTestSession) References(context.Context, string, int, int, bool) ([]Location, error) {
	return nil, nil
}

func (s *readinessTestSession) Hover(context.Context, string, int, int) (HoverContent, error) {
	return HoverContent{}, nil
}

func (s *readinessTestSession) WorkspaceSymbol(context.Context, string) ([]SymbolInfo, error) {
	return nil, nil
}

func (s *readinessTestSession) DocumentSymbol(context.Context, string) ([]SymbolInfo, error) {
	return nil, nil
}

func (s *readinessTestSession) DidOpen(context.Context, string, string, string, int32) error {
	return nil
}

func (s *readinessTestSession) DidClose(context.Context, string) error { return nil }

func (s *readinessTestSession) Diagnostics() <-chan PublishedDiagnostics { return nil }

func (s *readinessTestSession) Progress() <-chan ProgressEvent { return s.progress }

func (s *readinessTestSession) Exited() <-chan struct{} { return s.exited }

func (s *readinessTestSession) Close(context.Context) error { return nil }

// TestReadinessQueuedProgressTakesPrecedenceAtTimeout verifies that a queued
// complete cycle is processed when the readiness timeout is already ready.
func TestReadinessQueuedProgressTakesPrecedenceAtTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess := &readinessTestSession{
		progress: make(chan ProgressEvent, 2),
		exited:   make(chan struct{}),
	}
	sess.progress <- ProgressEvent{Token: "token", Kind: "begin"}
	sess.progress <- ProgressEvent{Token: "token", Kind: "end"}

	cfg := config.LSPConfig{
		ReadyTimeout:     config.MustDuration("0s"),
		ReadyGracePeriod: config.MustDuration("1h"),
	}
	m := NewManager(cfg, t.TempDir(), nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ent := &entry{session: sess, readiness: newReadiness(cfg)}
	go m.trackReadiness(ent, sess, ent.readiness)

	incomplete, err := m.awaitReady(ctx, ent)
	if err != nil {
		t.Fatalf("awaitReady: %v", err)
	}
	if incomplete {
		t.Error("awaitReady returned incomplete=true, want false")
	}
}

// readinessProgressObserver signals after trackReadiness has observed a begin
// event for the target token. Progress is checked on the next select iteration,
// after the event handler has updated openTokens.
type readinessProgressObserver struct {
	session
	ent      *entry
	token    string
	observed chan<- struct{}
}

func (s *readinessProgressObserver) Progress() <-chan ProgressEvent {
	s.ent.mu.Lock()
	_, open := s.ent.readiness.openTokens[s.token]
	s.ent.mu.Unlock()
	if open {
		select {
		case s.observed <- struct{}{}:
		default:
		}
	}
	return s.session.Progress()
}

// TestReadinessBegEndFlipsReady verifies that a single begin/end cycle marks
// readiness as ready, and subsequent awaitReady calls return immediately.
func TestReadinessBegEndFlipsReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	cfg := config.LSPConfig{
		ReadyTimeout:     config.MustDuration("5s"),
		ReadyGracePeriod: config.MustDuration("5s"),
	}

	tmpdir := t.TempDir()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ent := &entry{session: sess, readiness: newReadiness(cfg)}
	beginObserved := make(chan struct{}, 1)
	progressSession := &readinessProgressObserver{
		session:  sess,
		ent:      ent,
		token:    "token1",
		observed: beginObserved,
	}
	go m.trackReadiness(ent, progressSession, ent.readiness)

	begin, _ := json.Marshal(protocol.WorkDoneProgressBegin{Kind: "begin", Title: "Loading workspace"})
	beginParams := protocol.ProgressParams{
		Token: protocol.String("token1"),
		Value: protocol.LSPAny(begin),
	}

	start := time.Now()
	if err := fs.client.Progress(ctx, &beginParams); err != nil {
		t.Fatalf("send begin: %v", err)
	}

	beginWait := time.NewTimer(testTimeout)
	defer beginWait.Stop()
	select {
	case <-beginObserved:
	case <-beginWait.C:
		t.Fatal("timed out waiting for begin progress to be observed")
	}

	end, _ := json.Marshal(protocol.WorkDoneProgressEnd{Kind: "end"})
	endParams := protocol.ProgressParams{
		Token: protocol.String("token1"),
		Value: protocol.LSPAny(end),
	}
	if err := fs.client.Progress(ctx, &endParams); err != nil {
		t.Fatalf("send end: %v", err)
	}

	incomplete, err := m.awaitReady(ctx, ent)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("awaitReady: %v", err)
	}
	if incomplete {
		t.Error("awaitReady returned incomplete=true, want false")
	}
	if elapsed > 1*time.Second {
		t.Errorf("awaitReady took %v, expected < 1s", elapsed)
	}

	incomplete2, err2 := m.awaitReady(ctx, ent)
	if err2 != nil {
		t.Errorf("second awaitReady: %v", err2)
	}
	if incomplete2 {
		t.Error("second awaitReady returned incomplete=true, want false")
	}
}

// TestReadinessNoProgressFlipsReadyAfterGracePeriod verifies that when no
// progress arrives, readiness flips to ready after ReadyGracePeriod, not
// after ReadyTimeout.
func TestReadinessNoProgressFlipsReadyAfterGracePeriod(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	cfg := config.LSPConfig{
		ReadyTimeout:     config.MustDuration("5s"),
		ReadyGracePeriod: config.MustDuration("50ms"),
	}

	tmpdir := t.TempDir()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ent := &entry{session: sess, readiness: newReadiness(cfg)}
	go m.trackReadiness(ent, sess, ent.readiness)

	start := time.Now()
	incomplete, err := m.awaitReady(ctx, ent)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("awaitReady: %v", err)
	}
	if incomplete {
		t.Error("awaitReady returned incomplete=true, want false")
	}
	if elapsed < 40*time.Millisecond || elapsed >= 1*time.Second {
		t.Errorf("awaitReady took %v, want >= 40ms and < 1s", elapsed)
	}
}

// TestReadinessUnterminatedBeginReturnsIncompleteAfterTimeout verifies that
// when a begin arrives but no matching end, awaitReady returns incomplete=true
// after ReadyTimeout, with err=nil (no error).
func TestReadinessUnterminatedBeginReturnsIncompleteAfterTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	cfg := config.LSPConfig{
		ReadyTimeout:     config.MustDuration("100ms"),
		ReadyGracePeriod: config.MustDuration("10ms"),
	}

	tmpdir := t.TempDir()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ent := &entry{session: sess, readiness: newReadiness(cfg)}
	go m.trackReadiness(ent, sess, ent.readiness)

	begin, _ := json.Marshal(protocol.WorkDoneProgressBegin{Kind: "begin", Title: "Loading"})
	progressParams := protocol.ProgressParams{
		Token: protocol.String("token1"),
		Value: protocol.LSPAny(begin),
	}
	if err := fs.client.Progress(ctx, &progressParams); err != nil {
		t.Fatalf("send begin: %v", err)
	}

	start := time.Now()
	incomplete, err := m.awaitReady(ctx, ent)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("awaitReady: %v, want nil", err)
	}
	if !incomplete {
		t.Error("awaitReady returned incomplete=false, want true")
	}
	if elapsed < 80*time.Millisecond || elapsed >= 500*time.Millisecond {
		t.Errorf("awaitReady took %v, want >= 80ms and < 500ms", elapsed)
	}
}

// TestReadinessCancelledCtx verifies that cancellation of the caller's context
// causes awaitReady to return promptly with ctx.Err().
func TestReadinessCancelledCtx(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	cfg := config.LSPConfig{
		ReadyTimeout:     config.MustDuration("10s"),
		ReadyGracePeriod: config.MustDuration("10s"),
	}

	tmpdir := t.TempDir()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ent := &entry{session: sess, readiness: newReadiness(cfg)}
	go m.trackReadiness(ent, sess, ent.readiness)

	cancelCtx, cancelFn := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancelFn)

	start := time.Now()
	incomplete, err := m.awaitReady(cancelCtx, ent)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("awaitReady returned %v, want context.Canceled", err)
	}
	if elapsed >= 1*time.Second {
		t.Errorf("awaitReady took %v, expected < 1s", elapsed)
	}
	_ = incomplete
}

// TestReadinessServerExitWhileBlocked verifies that when the server exits
// while awaitReady is blocked, awaitReady returns promptly with
// errServerExited, not waiting for ReadyTimeout.
func TestReadinessServerExitWhileBlocked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	sess, proc, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	cfg := config.LSPConfig{
		ReadyTimeout:     config.MustDuration("5s"),
		ReadyGracePeriod: config.MustDuration("5s"),
	}

	tmpdir := t.TempDir()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ent := &entry{session: sess, readiness: newReadiness(cfg)}
	go m.trackReadiness(ent, sess, ent.readiness)

	time.AfterFunc(50*time.Millisecond, func() {
		proc.markExited()
	})

	start := time.Now()
	incomplete, err := m.awaitReady(ctx, ent)
	elapsed := time.Since(start)

	if !errors.Is(err, errServerExited) {
		t.Errorf("awaitReady returned %v, want errServerExited", err)
	}
	if elapsed >= 1*time.Second {
		t.Errorf("awaitReady took %v, expected < 1s", elapsed)
	}
	_ = incomplete
}

// TestReadinessConcurrentAwaiters verifies that multiple goroutines calling
// awaitReady on the same entry all unblock with the same result when the entry
// becomes ready or times out.
func TestReadinessConcurrentAwaiters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	cfg := config.LSPConfig{
		ReadyTimeout:     config.MustDuration("500ms"),
		ReadyGracePeriod: config.MustDuration("50ms"),
	}

	tmpdir := t.TempDir()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ent := &entry{session: sess, readiness: newReadiness(cfg)}
	go m.trackReadiness(ent, sess, ent.readiness)

	const numGoroutines = 10
	var wg sync.WaitGroup
	results := make([]struct {
		incomplete bool
		err        error
	}, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			inc, e := m.awaitReady(ctx, ent)
			results[idx].incomplete = inc
			results[idx].err = e
		}(i)
	}

	wg.Wait()

	if len(results) == 0 {
		t.Fatal("no results")
	}

	firstIncomplete := results[0].incomplete
	firstErr := results[0].err

	for i, res := range results {
		if res.incomplete != firstIncomplete {
			t.Errorf("goroutine %d: incomplete=%v, want %v", i, res.incomplete, firstIncomplete)
		}
		if !errors.Is(res.err, firstErr) {
			t.Errorf("goroutine %d: err=%v, want %v", i, res.err, firstErr)
		}
	}
}

// TestReadinessMultipleTokens verifies that readiness is triggered once the
// first complete begin/end cycle completes, even if other tokens are still
// in-flight or additional begins arrive afterward.
func TestReadinessMultipleTokens(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	cfg := config.LSPConfig{
		ReadyTimeout:     config.MustDuration("5s"),
		ReadyGracePeriod: config.MustDuration("100ms"),
	}

	tmpdir := t.TempDir()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ent := &entry{session: sess, readiness: newReadiness(cfg)}
	go m.trackReadiness(ent, sess, ent.readiness)

	begin, _ := json.Marshal(protocol.WorkDoneProgressBegin{Kind: "begin", Title: "Loading A"})
	// Send begin(A)
	progressParams := protocol.ProgressParams{
		Token: protocol.String("tokenA"),
		Value: protocol.LSPAny(begin),
	}
	if err := fs.client.Progress(ctx, &progressParams); err != nil {
		t.Fatalf("send begin(A): %v", err)
	}

	// Send begin(B)
	progressParams.Token = protocol.String("tokenB")
	if err := fs.client.Progress(ctx, &progressParams); err != nil {
		t.Fatalf("send begin(B): %v", err)
	}

	// Send end(A) - should trigger readiness
	end, _ := json.Marshal(protocol.WorkDoneProgressEnd{Kind: "end"})
	progressParams.Token = protocol.String("tokenA")
	progressParams.Value = protocol.LSPAny(end)
	if err := fs.client.Progress(ctx, &progressParams); err != nil {
		t.Fatalf("send end(A): %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	incomplete, err := m.awaitReady(ctx, ent)
	if err != nil {
		t.Errorf("awaitReady: %v", err)
	}
	if incomplete {
		t.Error("awaitReady returned incomplete=true, want false")
	}
}

// TestReadinessIgnoreOrphanEnd verifies that an end event for a token whose
// begin was never observed does not corrupt the in-flight count and does not
// trigger readiness.
func TestReadinessIgnoreOrphanEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	cfg := config.LSPConfig{
		ReadyTimeout:     config.MustDuration("500ms"),
		ReadyGracePeriod: config.MustDuration("200ms"),
	}

	tmpdir := t.TempDir()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ent := &entry{session: sess, readiness: newReadiness(cfg)}
	go m.trackReadiness(ent, sess, ent.readiness)

	end, _ := json.Marshal(protocol.WorkDoneProgressEnd{Kind: "end"})
	// Send end for unknown token - should be ignored
	progressParams := protocol.ProgressParams{
		Token: protocol.String("unknownToken"),
		Value: protocol.LSPAny(end),
	}
	if err := fs.client.Progress(ctx, &progressParams); err != nil {
		t.Fatalf("send orphan end: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// State should still be notReady
	ent.mu.Lock()
	if ent.readiness.isTerminal() {
		t.Error("readiness is terminal after orphan end, want notReady")
	}
	ent.mu.Unlock()

	begin, _ := json.Marshal(protocol.WorkDoneProgressBegin{Kind: "begin", Title: "Loading"})
	// Send begin(A)
	progressParams.Token = protocol.String("tokenA")
	progressParams.Value = protocol.LSPAny(begin)
	if err := fs.client.Progress(ctx, &progressParams); err != nil {
		t.Fatalf("send begin(A): %v", err)
	}

	// Send begin(B)
	progressParams.Token = protocol.String("tokenB")
	if err := fs.client.Progress(ctx, &progressParams); err != nil {
		t.Fatalf("send begin(B): %v", err)
	}

	// Send end(A) - should trigger readiness
	progressParams.Token = protocol.String("tokenA")
	progressParams.Value = protocol.LSPAny(end)
	if err := fs.client.Progress(ctx, &progressParams); err != nil {
		t.Fatalf("send end(A): %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	incomplete, err := m.awaitReady(ctx, ent)
	if err != nil {
		t.Errorf("awaitReady: %v", err)
	}
	if incomplete {
		t.Error("awaitReady returned incomplete=true, want false")
	}
}

// TestReadinessReadyBeforeTimeoutThenAwaitAfter verifies that when readiness
// reaches ready before timeout, a caller invoking awaitReady after the timeout
// window has passed still correctly returns incomplete=false. This tests that
// the redundant timer race condition does not exist.
func TestReadinessReadyBeforeTimeoutThenAwaitAfter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	cfg := config.LSPConfig{
		ReadyTimeout:     config.MustDuration("50ms"),
		ReadyGracePeriod: config.MustDuration("20ms"),
	}

	tmpdir := t.TempDir()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ent := &entry{session: sess, readiness: newReadiness(cfg)}
	go m.trackReadiness(ent, sess, ent.readiness)

	// Send begin/end to trigger readiness immediately.
	begin, _ := json.Marshal(protocol.WorkDoneProgressBegin{Kind: "begin", Title: "Loading workspace"})
	progressParams := protocol.ProgressParams{
		Token: protocol.String("token1"),
		Value: protocol.LSPAny(begin),
	}
	if err := fs.client.Progress(ctx, &progressParams); err != nil {
		t.Fatalf("send begin: %v", err)
	}

	end, _ := json.Marshal(protocol.WorkDoneProgressEnd{Kind: "end"})
	progressParams.Value = protocol.LSPAny(end)
	if err := fs.client.Progress(ctx, &progressParams); err != nil {
		t.Fatalf("send end: %v", err)
	}

	// Wait for readyCh to close, confirming readiness was reached.
	select {
	case <-ent.readiness.readyCh:
		// readyCh closed, readiness reached as expected.
	case <-time.After(1 * time.Second):
		t.Fatal("readyCh never closed; readiness was not determined")
	}

	// Sleep past the ReadyTimeout to exercise the race window on the old code.
	time.Sleep(100 * time.Millisecond)

	// Now call awaitReady. On the broken code, this could race with the redundant timer
	// and incorrectly return incomplete=true. On the fixed code, it must return
	// incomplete=false deterministically.
	incomplete, err := m.awaitReady(ctx, ent)

	if err != nil {
		t.Errorf("awaitReady: %v", err)
	}
	if incomplete {
		t.Error("awaitReady returned incomplete=true, want false (readiness was reached before timeout)")
	}
}

// TestReadinessServerExitThenAwaitAfter verifies that when the server exits
// while notReady, a caller invoking awaitReady after the exit has already
// happened correctly returns errServerExited (not falsely claiming readiness).
// This tests that the defer's channel-close-without-state bug does not exist.
func TestReadinessServerExitThenAwaitAfter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	sess, proc, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	cfg := config.LSPConfig{
		ReadyTimeout:     config.MustDuration("5s"),
		ReadyGracePeriod: config.MustDuration("5s"),
	}

	tmpdir := t.TempDir()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ent := &entry{session: sess, readiness: newReadiness(cfg)}
	go m.trackReadiness(ent, sess, ent.readiness)

	// Send only begin, no end, so readiness stays notReady.
	begin, _ := json.Marshal(protocol.WorkDoneProgressBegin{Kind: "begin", Title: "Loading"})
	progressParams := protocol.ProgressParams{
		Token: protocol.String("token1"),
		Value: protocol.LSPAny(begin),
	}
	if err := fs.client.Progress(ctx, &progressParams); err != nil {
		t.Fatalf("send begin: %v", err)
	}

	// Exit the server while notReady.
	proc.markExited()

	// Give trackReadiness time to exit and handle the exit case.
	time.Sleep(50 * time.Millisecond)

	// Now call awaitReady after the exit has already happened.
	// On the broken code, readyCh would be closed (by the defer) but state
	// would still be notReady, causing it to falsely return (false, nil).
	// On the fixed code, it must return errServerExited.
	incomplete, err := m.awaitReady(ctx, ent)

	if !errors.Is(err, errServerExited) {
		t.Errorf("awaitReady returned %v, want errServerExited", err)
	}

	// Verify the invariant: readyCh must still be open and state must be notReady
	// (on the broken code, the defer closes readyCh without setting state).
	ent.mu.Lock()
	r := ent.readiness
	select {
	case <-r.readyCh:
		t.Error("readyCh closed without terminal state; the defer bug exists")
	default:
		// readyCh still open, as expected on the fixed code.
	}
	if r.isTerminal() {
		t.Error("readiness state is terminal after server exit; should stay notReady")
	}
	ent.mu.Unlock()
	_ = incomplete
}
