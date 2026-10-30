package usagestats

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// gateLocker blocks lock until release is closed and counts acquisitions, so a
// test can hold the store lock and see how many writes the writer attempted.
type gateLocker struct {
	acquired chan struct{}
	release  chan struct{}

	mu        sync.Mutex
	locks     int
	releaseOn sync.Once
}

func newGateLocker() *gateLocker {
	return &gateLocker{acquired: make(chan struct{}, 16), release: make(chan struct{})}
}

func (l *gateLocker) lock(uintptr) error {
	l.mu.Lock()
	l.locks++
	l.mu.Unlock()
	l.acquired <- struct{}{}
	<-l.release
	return nil
}

func (l *gateLocker) unlock(uintptr) error { return nil }

func (l *gateLocker) unlockAll() {
	l.releaseOn.Do(func() { close(l.release) })
}

func (l *gateLocker) lockCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.locks
}

func waitAcquired(t *testing.T, l *gateLocker) {
	t.Helper()
	select {
	case <-l.acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("writer never attempted the store write")
	}
}

func usageObservation(alias string) Observation {
	return Observation{
		ProviderAlias:    alias,
		ProviderType:     "openai",
		BackendModelID:   "m",
		PromptTokens:     10,
		CompletionTokens: 2,
		Source:           SourceParent,
		At:               baseTime,
	}
}

func usageKey(alias string) bucketKey {
	return bucketKey{
		providerAlias:  alias,
		providerType:   "openai",
		backendModelID: "m",
		hourUnix:       hourStart.Unix(),
		source:         SourceParent,
	}
}

// newLockedTestRecorder returns a recorder isolated to a temp store whose writer
// uses locker when non-nil. The locker is swapped before any Record, so the
// writer only reads it after the happens-before edge of the first queued delta.
func newLockedTestRecorder(t *testing.T, locker fileLocker) *Recorder {
	t.Helper()
	isolateTest(t)
	t.Setenv(TelemetryEnvVar, "")
	rec := newTestRecorder(t, fixedClock(baseTime))
	if locker != nil {
		rec.store.locker = locker
	}
	return rec
}

func mustDrain(t *testing.T, rec *Recorder) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rec.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}
}

// TestRecordDoesNotWaitForHeldStoreLock proves recording never blocks on the
// cross-process store lock, and that an expiring Drain reports the block.
func TestRecordDoesNotWaitForHeldStoreLock(t *testing.T) {
	gate := newGateLocker()
	t.Cleanup(gate.unlockAll)
	rec := newLockedTestRecorder(t, gate)

	recorded := make(chan struct{})
	go func() {
		defer close(recorded)
		for range 3 {
			rec.Record(usageObservation("a"))
		}
	}()
	select {
	case <-recorded:
	case <-time.After(2 * time.Second):
		t.Fatal("Record waited on the held store lock")
	}
	waitAcquired(t, gate)

	drainCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := rec.Drain(drainCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain with the store lock held = %v, want context.DeadlineExceeded", err)
	}

	gate.unlockAll()
	mustDrain(t, rec)

	if b := rec.store.load()[usageKey("a")]; b == nil || b.Requests != 3 {
		t.Fatalf("persisted bucket = %+v, want 3 requests", b)
	}
}

// TestWriterCoalescesQueuedDeltas proves deltas queued behind a held lock are
// summed into one follow-up write whose totals equal the individual deltas.
func TestWriterCoalescesQueuedDeltas(t *testing.T) {
	gate := newGateLocker()
	t.Cleanup(gate.unlockAll)
	rec := newLockedTestRecorder(t, gate)

	rec.Record(usageObservation("a"))
	waitAcquired(t, gate)

	// The writer is now blocked inside the first batch, so these deltas pile up
	// in one pending map for a single follow-up write.
	rec.Record(Observation{
		ProviderAlias:    "a",
		ProviderType:     "openai",
		BackendModelID:   "m",
		PromptTokens:     30,
		CacheReadTokens:  5,
		CompletionTokens: 1,
		At:               baseTime.Add(10 * time.Minute),
	})
	rec.Record(Observation{
		ProviderAlias:  "b",
		ProviderType:   "openai",
		BackendModelID: "m",
		PromptTokens:   7,
		Source:         SourceSubAgent,
		At:             nextHour,
	})

	gate.unlockAll()
	mustDrain(t, rec)

	if got := gate.lockCount(); got != 2 {
		t.Fatalf("store writes: got %d, want 2 (first batch blocked, the rest coalesced)", got)
	}
	stored := rec.store.load()
	a := stored[usageKey("a")]
	if a == nil {
		t.Fatal("bucket a was not persisted")
	}
	if a.Requests != 2 || a.InputTokens != 35 || a.CacheReadTokens != 5 || a.CompletionTokens != 3 {
		t.Errorf("bucket a = %+v, want {Requests:2 InputTokens:35 CacheReadTokens:5 CompletionTokens:3}", *a)
	}
	bKey := bucketKey{
		providerAlias:  "b",
		providerType:   "openai",
		backendModelID: "m",
		hourUnix:       nextHour.Unix(),
		source:         SourceSubAgent,
	}
	if b := stored[bKey]; b == nil || b.Requests != 1 || b.InputTokens != 7 {
		t.Fatalf("bucket b = %+v, want 1 request and 7 input tokens", b)
	}
}

// TestConcurrentRecordersOnOneStoreLoseNothing proves the additive flock merge
// keeps every delta when two recorder-owned stores write the same path at once.
func TestConcurrentRecordersOnOneStoreLoseNothing(t *testing.T) {
	isolateTest(t)
	t.Setenv(TelemetryEnvVar, "")
	path := filepath.Join(t.TempDir(), "cache-stats.json")
	oldPath := storePath
	storePath = func() string { return path }
	t.Cleanup(func() { storePath = oldPath })

	const (
		recorderCount = 2
		perRecorder   = 20
	)
	recs := make([]*Recorder, recorderCount)
	for i := range recs {
		recs[i] = newTestRecorder(t, fixedClock(baseTime))
	}

	var wg sync.WaitGroup
	for _, rec := range recs {
		wg.Add(1)
		go func(rec *Recorder) {
			defer wg.Done()
			for range perRecorder {
				rec.Record(usageObservation("a"))
			}
		}(rec)
	}
	wg.Wait()

	var drainWG sync.WaitGroup
	for _, rec := range recs {
		drainWG.Add(1)
		go func(rec *Recorder) {
			defer drainWG.Done()
			mustDrain(t, rec)
		}(rec)
	}
	drainWG.Wait()

	b := newStore(fixedClock(baseTime)).load()[usageKey("a")]
	if b == nil {
		t.Fatal("bucket a was not persisted")
	}
	if b.Requests != recorderCount*perRecorder {
		t.Errorf("Requests: got %d, want %d", b.Requests, recorderCount*perRecorder)
	}
	if b.InputTokens != recorderCount*perRecorder*10 {
		t.Errorf("InputTokens: got %d, want %d", b.InputTokens, recorderCount*perRecorder*10)
	}
}

// TestPersistWriteFailureIsLoggedAndDoesNotBlock proves a store write failure is
// logged by the writer and neither blocks Record nor fails Drain.
func TestPersistWriteFailureIsLoggedAndDoesNotBlock(t *testing.T) {
	rec := newLockedTestRecorder(t, failingFileLocker{})
	logs := make(chan string, 4)
	rec.writer.logf = func(format string, args ...any) {
		select {
		case logs <- fmt.Sprintf(format, args...):
		default:
		}
	}

	rec.Record(usageObservation("a"))
	mustDrain(t, rec)

	select {
	case msg := <-logs:
		if !strings.Contains(msg, "lock store file") {
			t.Fatalf("failure log = %q, want the store lock error", msg)
		}
	default:
		t.Fatal("write failure was not logged")
	}
	if _, err := os.Stat(rec.store.path); !os.IsNotExist(err) {
		t.Fatalf("store file written despite lock failure: %v", err)
	}
}

// TestCloseWakesDrainAndStopsWrites proves Close releases a waiting Drain, drops
// further deltas instead of writing them, stops the goroutine, and is
// idempotent.
func TestCloseWakesDrainAndStopsWrites(t *testing.T) {
	gate := newGateLocker()
	t.Cleanup(gate.unlockAll)
	rec := newLockedTestRecorder(t, gate)
	logs := make(chan string, 8)
	rec.writer.logf = func(format string, args ...any) {
		select {
		case logs <- fmt.Sprintf(format, args...):
		default:
		}
	}

	rec.Record(usageObservation("a"))
	waitAcquired(t, gate)

	// While the writer is blocked mid-write, Drain cannot return: the write is in
	// flight, the writer has not stopped, and the context deadline is far away.
	// Spin until the goroutine has had a chance to enter that wait, asserting on
	// every pass that it has not returned early.
	drained := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { drained <- rec.Drain(ctx) }()
	for range 50 {
		select {
		case err := <-drained:
			t.Fatalf("Drain returned before Close while a write was in flight: %v", err)
		default:
		}
		time.Sleep(time.Millisecond)
	}

	rec.Close()

	select {
	case err := <-drained:
		if err != nil {
			t.Fatalf("Drain after Close = %v, want nil: Close must wake it, not the 10s deadline", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not wake the waiting Drain")
	}

	rec.Record(usageObservation("b"))
	select {
	case msg := <-logs:
		if !strings.Contains(msg, "persistence writer closed") {
			t.Fatalf("drop log = %q, want a writer-closed warning", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delta recorded after Close was not logged as dropped")
	}
	if got := gate.lockCount(); got != 1 {
		t.Fatalf("store writes after Close: got %d, want 1", got)
	}

	gate.unlockAll()
	select {
	case <-rec.writer.done:
	case <-time.After(5 * time.Second):
		t.Fatal("writer goroutine did not exit after Close")
	}

	rec.Close()
	if err := rec.Drain(context.Background()); err != nil {
		t.Fatalf("Drain after Close: %v", err)
	}
}

// TestCloseDropsPendingDeltas proves a Close during a write abandons the deltas
// that are still pending, logs the drop, and leaves them out of the store.
func TestCloseDropsPendingDeltas(t *testing.T) {
	gate := newGateLocker()
	t.Cleanup(gate.unlockAll)
	rec := newLockedTestRecorder(t, gate)
	logs := make(chan string, 8)
	rec.writer.logf = func(format string, args ...any) {
		select {
		case logs <- fmt.Sprintf(format, args...):
		default:
		}
	}

	rec.Record(usageObservation("a"))
	waitAcquired(t, gate)
	// The first delta is already claimed and in flight; this one is still pending
	// when Close runs.
	rec.Record(usageObservation("b"))

	rec.Close()
	gate.unlockAll()

	select {
	case <-rec.writer.done:
	case <-time.After(5 * time.Second):
		t.Fatal("writer goroutine did not exit after Close")
	}

	dropped := false
	for {
		select {
		case msg := <-logs:
			if strings.Contains(msg, "dropped 1 pending buckets") {
				dropped = true
			}
			continue
		default:
		}
		break
	}
	if !dropped {
		t.Fatal("Close did not log the pending delta drop")
	}

	stored := rec.store.load()
	if b := stored[usageKey("a")]; b == nil || b.Requests != 1 {
		t.Errorf("in-flight bucket a = %+v, want 1 persisted request", b)
	}
	if b := stored[usageKey("b")]; b != nil {
		t.Errorf("pending bucket b = %+v, want it dropped by Close", b)
	}
}
