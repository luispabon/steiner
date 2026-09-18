package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/luispabon/steiner/internal/config"
)

// probeSession records the context its DidClose was handed, as observed from
// inside the call. It embeds the readiness double, whose Close is a no-op and
// whose session methods ignore ctx.
type probeSession struct {
	*readinessTestSession

	mu       sync.Mutex
	ctx      context.Context
	ctxErr   error
	deadline time.Time
	hasDL    bool
}

func (s *probeSession) DidClose(ctx context.Context, _ string) error {
	deadline, hasDeadline := ctx.Deadline()

	s.mu.Lock()
	s.ctx = ctx
	s.ctxErr = ctx.Err()
	s.deadline = deadline
	s.hasDL = hasDeadline
	s.mu.Unlock()

	return ctx.Err()
}

func (s *probeSession) closeCall() (context.Context, time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx, s.deadline, s.hasDL, s.ctxErr
}

// TestWithDocumentDidCloseIsBoundedAndDetached pins that the best-effort
// DidClose runs on a context detached from the caller's cancellation but still
// carrying a deadline, so a wedged server cannot hold the cycle lock forever.
func TestWithDocumentDidCloseIsBoundedAndDetached(t *testing.T) {
	sess := &probeSession{readinessTestSession: &readinessTestSession{}}

	testFile := filepath.Join(t.TempDir(), "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// An already-cancelled caller: the close must not inherit that cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := withDocument(ctx, sess, testFile, func() error { return nil }); err != nil {
		t.Fatalf("withDocument: %v", err)
	}

	closeCtx, deadline, hasDeadline, closeErr := sess.closeCall()
	if closeCtx == nil {
		t.Fatal("DidClose was not called")
	}
	if closeErr != nil {
		t.Errorf("DidClose context err = %v, want nil: the close must be detached from the caller's cancellation", closeErr)
	}
	if !hasDeadline {
		t.Fatal("DidClose context has no deadline: an unresponsive server would hold the cycle lock indefinitely")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > didCloseTimeout {
		t.Errorf("DidClose deadline is %v away, want within (0, %v]", remaining, didCloseTimeout)
	}
	if err := closeCtx.Err(); err == nil {
		t.Error("DidClose context was never cancelled: withDocument must release it on return")
	}
}

// TestClientHandlerProgressDropsNewestWhenFull pins that a full progress
// channel drops the newest event instead of parking the notification handler.
func TestClientHandlerProgressDropsNewestWhenFull(t *testing.T) {
	ch := &clientHandler{
		diagnostics: make(chan PublishedDiagnostics, 1),
		progress:    make(chan ProgressEvent, notifyBuffer),
	}

	queued := make([]ProgressEvent, 0, notifyBuffer)
	for i := range notifyBuffer {
		event := ProgressEvent{Token: fmt.Sprintf("queued-%d", i), Kind: "report", Message: "queued"}
		ch.progress <- event
		queued = append(queued, event)
	}

	begin, err := json.Marshal(protocol.WorkDoneProgressBegin{Kind: "begin", Title: "newest"})
	if err != nil {
		t.Fatalf("marshal begin: %v", err)
	}

	returned := make(chan error, 1)
	go func() {
		returned <- ch.Progress(context.Background(), &protocol.ProgressParams{
			Token: protocol.String("newest"),
			Value: protocol.LSPAny(begin),
		})
	}()

	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Progress: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Progress blocked on a full channel")
	}

	if got := len(ch.progress); got != notifyBuffer {
		t.Errorf("progress channel depth = %d, want %d: a full channel drops the newest event", got, notifyBuffer)
	}
	for i, want := range queued {
		if got := <-ch.progress; got != want {
			t.Errorf("queued progress[%d] = %+v, want the already-queued %+v", i, got, want)
		}
	}
}

// TestClientHandlerPublishDiagnosticsDropsOldestWhenFull pins that a full
// diagnostics channel evicts its oldest publication and keeps the newest, so a
// later consumer never reads diagnostics the server already replaced.
func TestClientHandlerPublishDiagnosticsDropsOldestWhenFull(t *testing.T) {
	ch := &clientHandler{
		diagnostics: make(chan PublishedDiagnostics, notifyBuffer),
		progress:    make(chan ProgressEvent, 1),
	}

	for i := range notifyBuffer {
		ch.diagnostics <- PublishedDiagnostics{File: fmt.Sprintf("/stale-%d.go", i)}
	}

	params := &protocol.PublishDiagnosticsParams{
		URI: uri.File("/latest.go"),
		Diagnostics: []protocol.Diagnostic{{
			Range:    targetRange(),
			Severity: protocol.DiagnosticSeverityError,
			Message:  protocol.String("latest"),
		}},
	}

	returned := make(chan error, 1)
	go func() { returned <- ch.PublishDiagnostics(context.Background(), params) }()

	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("PublishDiagnostics: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("PublishDiagnostics blocked on a full channel")
	}

	if got := len(ch.diagnostics); got != notifyBuffer {
		t.Fatalf("diagnostics channel depth = %d, want %d", got, notifyBuffer)
	}

	// The oldest publication is evicted, the rest keep their order, and the
	// newest publication is enqueued last.
	if first := (<-ch.diagnostics).File; first != "/stale-1.go" {
		t.Errorf("oldest queued diagnostics = %q, want %q: the oldest must be dropped", first, "/stale-1.go")
	}
	var last PublishedDiagnostics
	for range notifyBuffer - 1 {
		last = <-ch.diagnostics
	}
	if last.File != "/latest.go" {
		t.Errorf("newest queued diagnostics = %q, want %q", last.File, "/latest.go")
	}
	if len(last.Items) != 1 || last.Items[0].Message != "latest" {
		t.Errorf("newest queued diagnostics items = %+v, want the latest publication", last.Items)
	}
}

// TestEntryForKeyRecordsLastUsedUnderEntryLock pins that entryForKey refreshes
// LastUsed under ent.mu, the lock that guards every other ServerState field.
// The reader goroutine below holds only ent.mu, matching entryForKey's own state
// machine, whose Status/StartedAt/Err accesses are guarded by ent.mu alone. A
// LastUsed write that skipped ent.mu is a data race with it and fails under
// -race.
func TestEntryForKeyRecordsLastUsedUnderEntryLock(t *testing.T) {
	root := t.TempDir()
	cfg := config.LSPConfig{
		Enabled: true,
		Servers: map[string]config.LSPServerConfig{
			"go": {Enabled: true, FileExtensions: []string{".go"}},
		},
	}

	m := NewManager(cfg, root, nil, func(string) {})
	defer func() { _ = m.Close() }()

	key := sessionKey{server: "go", root: root}
	ent := &entry{
		state:   ServerState{Name: "go", Root: root, Status: ServerStatusReady},
		session: &readinessTestSession{},
	}

	m.mu.Lock()
	m.sessions[key] = ent
	m.mu.Unlock()

	ent.mu.Lock()
	before := ent.state.LastUsed
	ent.mu.Unlock()

	srv := cfg.Servers["go"]
	ctx := context.Background()

	if _, _, err := m.entryForKey(ctx, "go", srv, key); err != nil {
		t.Fatalf("entryForKey: %v", err)
	}

	ent.mu.Lock()
	after := ent.state.LastUsed
	ent.mu.Unlock()
	if !after.After(before) {
		t.Errorf("LastUsed = %v, want later than %v: a sessionFor hit must refresh it", after, before)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var observed atomic.Int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			ent.mu.Lock()
			observed.Add(ent.state.LastUsed.UnixNano())
			ent.mu.Unlock()
		}
	}()

	for range 1000 {
		got, sess, err := m.entryForKey(ctx, "go", srv, key)
		if err != nil {
			t.Fatalf("entryForKey: %v", err)
		}
		if got != ent || sess == nil {
			t.Fatalf("entryForKey = (%v, %v), want the injected ready entry", got, sess)
		}
	}

	close(stop)
	wg.Wait()
	if observed.Load() == 0 {
		t.Error("reader goroutine never observed LastUsed")
	}
}
