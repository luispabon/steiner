package quota

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestObserveSkipsAppendForUnchangedStateWithinHour(t *testing.T) {
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	logs := &logBuffer{}
	clock := testBase
	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return clock },
		Logf:  logs.logf,
	})
	t.Cleanup(func() {
		mustDrain(t, svc)
		svc.Close()
	})

	svc.Register(Account{Key: "k", Aliases: []string{"a"}})

	// The first observation is appended normally and records the append time.
	svc.Observe(sampleSnapshot("k", testBase, 10))
	mustDrain(t, svc)
	if got := logs.messages(); len(got) != 0 {
		t.Fatalf("first observation logged %v", got)
	}

	// A directory at the lock path makes every Store.Append fail, so any call
	// that reaches the store shows up as a logged persist error.
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatalf("remove lock file: %v", err)
	}
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatalf("create lock directory: %v", err)
	}

	steps := []struct {
		name       string
		at         time.Time
		used       float64
		wantAppend bool
	}{
		{"unchanged within an hour skips the store", testBase.Add(30 * time.Minute), 10, false},
		{"unchanged after an hour reaches the store", testBase.Add(61 * time.Minute), 10, true},
		{"changed state reaches the store", testBase.Add(62 * time.Minute), 20, true},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			select {
			case <-svc.Updates():
			default:
			}
			clock = step.at
			before := len(logs.messages())
			svc.Observe(sampleSnapshot("k", step.at, step.used))
			mustDrain(t, svc)

			if attempted := len(logs.messages()) > before; attempted != step.wantAppend {
				t.Errorf("store append attempted = %v, want %v (logs %v)", attempted, step.wantAppend, logs.messages()[before:])
			}
			select {
			case <-svc.Updates():
			default:
				t.Error("observation did not notify Updates")
			}
			latest := svc.Views("")[0].Latest
			if !latest.FetchedAt.Equal(step.at) || latest.Windows[0].UsedPercent != step.used {
				t.Errorf("in-memory latest = fetched %s at %v%%, want %s at %v%%", latest.FetchedAt, latest.Windows[0].UsedPercent, step.at, step.used)
			}
		})
	}
}

func TestObserveStampsZeroFetchedAt(t *testing.T) {
	svc, clock := newTestService(t, nil)
	svc.Register(Account{Key: "k", Aliases: []string{"a"}})
	*clock = testBase.Add(5 * time.Minute)
	svc.Observe(Snapshot{AccountKey: "k", Windows: []Window{win("5h", 20)}})

	latest := svc.Views("")[0].Latest
	if latest == nil || !latest.FetchedAt.Equal(testBase.Add(5*time.Minute)) {
		t.Errorf("zero-FetchedAt observation stored FetchedAt %v, want service clock %s", latest, testBase.Add(5*time.Minute))
	}
}

func TestRegisterUnavailableReplacesWhenNonEmptyAndNotifies(t *testing.T) {
	svc, _ := newTestService(t, nil)
	steps := []struct {
		name        string
		unavailable string
		want        string
	}{
		{"first registration sets the reason", "no plan", "no plan"},
		{"empty reason keeps the previous reason", "", "no plan"},
		{"non-empty reason replaces the previous reason", "plan expired", "plan expired"},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			select {
			case <-svc.Updates():
			default:
			}
			svc.Register(Account{Key: "k", Aliases: []string{"a"}, Unavailable: step.unavailable})
			if got := svc.Views("")[0].Unavailable; got != step.want {
				t.Errorf("Unavailable = %q, want %q", got, step.want)
			}
			select {
			case <-svc.Updates():
			default:
				t.Error("Register did not notify Updates")
			}
		})
	}
}

// holdStoreLock takes the store lock the way another process would, so the
// writer goroutine blocks in Store.Append until the returned release runs. The
// release is also registered as a cleanup, so register any later cleanup that
// waits on the writer before calling this, keeping the lock release first in
// LIFO order.
func holdStoreLock(t *testing.T, path string) (release func()) {
	t.Helper()
	lock, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open store lock: %v", err)
	}
	if err := lockFile(lock.Fd()); err != nil {
		t.Fatalf("lock store: %v", err)
	}
	released := false
	release = func() {
		if released {
			return
		}
		released = true
		if err := unlockFile(lock.Fd()); err != nil {
			t.Errorf("unlock store: %v", err)
		}
		if err := lock.Close(); err != nil {
			t.Errorf("close store lock: %v", err)
		}
	}
	t.Cleanup(release)
	return release
}

// pendingJobs returns how many snapshots are queued or in flight in the writer.
func pendingJobs(svc *Service) int {
	svc.writer.mu.Lock()
	defer svc.writer.mu.Unlock()
	n := len(svc.writer.queue)
	if svc.writer.writing {
		n++
	}
	return n
}

// waitForWriterBusy waits until the writer has popped a job and is appending it.
func waitForWriterBusy(t *testing.T, svc *Service) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		svc.writer.mu.Lock()
		busy := svc.writer.writing
		svc.writer.mu.Unlock()
		if busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("writer did not start an append")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestObserveDoesNotWaitForStoreLock(t *testing.T) {
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return testBase },
	})
	t.Cleanup(func() {
		mustDrain(t, svc)
		svc.Close()
	})
	release := holdStoreLock(t, path)
	svc.Register(Account{Key: "k", Aliases: []string{"a"}})

	observed := make(chan struct{})
	go func() {
		defer close(observed)
		// Identical observations queue up while the lock is held: the service
		// dedupe baseline only moves on a write, so all three are enqueued.
		for range 3 {
			svc.Observe(sampleSnapshot("k", testBase, 10))
		}
		// A caller reading in-memory state does not wait for the writer either.
		svc.Views("")
	}()
	select {
	case <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("Observe waited for the store lock")
	}

	drainCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := svc.Drain(drainCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain with the store lock held = %v, want context.DeadlineExceeded", err)
	}

	release()
	mustDrain(t, svc)

	// The store's file-level dedupe collapses the queued duplicates into one
	// entry.
	stored, err := NewStore(path).readAll()
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored %d snapshots for identical queued observations, want 1", len(stored))
	}
	snap := stored[0]
	if len(snap.Windows) != 1 || snap.Windows[0].UsedPercent != 10 {
		t.Errorf("persisted snapshot = %+v, want the observed 10%% window", snap)
	}
}

func TestDrainFlushesPendingWritesInOrder(t *testing.T) {
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return testBase },
	})
	t.Cleanup(func() {
		mustDrain(t, svc)
		svc.Close()
	})
	release := holdStoreLock(t, path)
	svc.Register(Account{Key: "k", Aliases: []string{"a"}})

	const observations = 5
	for i := range observations {
		svc.Observe(sampleSnapshot("k", testBase.Add(time.Duration(i)*time.Minute), float64(i)))
	}

	release()
	mustDrain(t, svc)

	stored, err := NewStore(path).readAll()
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if len(stored) != observations {
		t.Fatalf("stored %d snapshots, want %d", len(stored), observations)
	}
	for i, snap := range stored {
		if snap.Windows[0].UsedPercent != float64(i) || !snap.FetchedAt.Equal(testBase.Add(time.Duration(i)*time.Minute)) {
			t.Errorf("stored[%d] = %v%% at %s, want %d%% at %s", i, snap.Windows[0].UsedPercent, snap.FetchedAt, i, testBase.Add(time.Duration(i)*time.Minute))
		}
	}
}

// TestDedupeComparesAgainstPersistedBaseline pins that the service dedupe
// baseline is the last snapshot written to the store, not the previous
// observation. Every step here drifts the window reset by one second, so a
// previous-observation baseline would call all twenty unchanged and store only
// the first; the persisted baseline lets cumulative drift surface.
func TestDedupeComparesAgainstPersistedBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-history.json")
	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return testBase },
	})
	t.Cleanup(func() {
		mustDrain(t, svc)
		svc.Close()
	})
	svc.Register(Account{Key: "k", Aliases: []string{"k"}})

	const observations = 20
	for i := range observations {
		snap := sampleSnapshot("k", testBase.Add(time.Duration(i)*time.Minute), 10)
		snap.Windows[0].ResetsAt = snap.Windows[0].ResetsAt.Add(time.Duration(i) * time.Second)
		svc.Observe(snap)
	}
	mustDrain(t, svc)

	stored, err := NewStore(path).readAll()
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if len(stored) < 2 {
		t.Fatalf("stored %d snapshots, want the initial one plus at least one drift past the tolerance", len(stored))
	}
	first := stored[0].Windows[0].ResetsAt
	last := stored[len(stored)-1].Windows[0].ResetsAt
	if drift := last.Sub(first); drift < resetsAtTolerance {
		t.Errorf("stored reset drift = %v, want at least %v", drift, resetsAtTolerance)
	}
}

// TestRestartSeedsDedupeBaselineFromStore pins that a new service loads the
// stored snapshot as its dedupe baseline, so an unchanged observation after a
// restart queues no append at all.
func TestRestartSeedsDedupeBaselineFromStore(t *testing.T) {
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	stored := sampleSnapshot("k", testBase, 10)
	if err := NewStore(path).Append(stored, testBase); err != nil {
		t.Fatalf("seed Append: %v", err)
	}

	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return testBase.Add(10 * time.Minute) },
	})
	t.Cleanup(func() {
		mustDrain(t, svc)
		svc.Close()
	})
	svc.Register(Account{Key: "k", Aliases: []string{"k"}})

	release := holdStoreLock(t, path)
	defer release()

	svc.Observe(stored)
	svc.Observe(stored)
	if pending := pendingJobs(svc); pending != 0 {
		t.Fatalf("%d append(s) queued for an unchanged snapshot after restart, want none", pending)
	}
}

// TestSkippedAppendSeedsDedupeBaseline pins that a file-level dedupe skip moves
// the in-process baseline to the stored snapshot, so a state another process
// already wrote is not re-queued on every observation.
func TestSkippedAppendSeedsDedupeBaseline(t *testing.T) {
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	if err := NewStore(path).Append(sampleSnapshot("k", testBase, 10), testBase); err != nil {
		t.Fatalf("seed Append: %v", err)
	}

	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return testBase.Add(30 * time.Minute) },
	})
	t.Cleanup(func() {
		mustDrain(t, svc)
		svc.Close()
	})
	svc.Register(Account{Key: "k", Aliases: []string{"k"}})

	// Another process writes a newer state after this service loaded, so the
	// in-process baseline is stale relative to the file.
	svc.Load()
	other := sampleSnapshot("k", testBase.Add(20*time.Minute), 40)
	if err := NewStore(path).Append(other, testBase.Add(20*time.Minute)); err != nil {
		t.Fatalf("second process Append: %v", err)
	}

	// The append is skipped by the file-level dedupe; the skip must move the
	// in-process baseline to the stored snapshot.
	svc.Observe(other)
	mustDrain(t, svc)

	release := holdStoreLock(t, path)
	defer release()

	svc.Observe(other)
	if pending := pendingJobs(svc); pending != 0 {
		t.Fatalf("%d append(s) queued for a state the store already holds, want none", pending)
	}
}

func TestPersistFailureIsLoggedAndDoesNotBlock(t *testing.T) {
	// A regular file where the store directory belongs makes every append fail
	// before it takes the lock.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	logs := &logBuffer{}
	svc := NewService(ServiceOptions{
		Store: NewStore(filepath.Join(blocked, "quota-history.json")),
		Now:   func() time.Time { return testBase },
		Logf:  logs.logf,
	})
	t.Cleanup(func() {
		mustDrain(t, svc)
		svc.Close()
	})
	svc.Register(Account{Key: "k", Aliases: []string{"k"}})

	svc.Observe(sampleSnapshot("k", testBase, 10))
	mustDrain(t, svc)

	if logs.count("quota: persist") == 0 {
		t.Errorf("logged %v, want a persist failure", logs.messages())
	}

	// The service keeps accepting observations after a failed append.
	svc.Observe(sampleSnapshot("k", testBase.Add(time.Minute), 20))
	mustDrain(t, svc)
}

// TestPersistQueueOverflowDropsOldest pins the overflow policy: once the queue
// is full the oldest pending job is dropped, so the newest observation is still
// written and the dropped ones never reach the store.
func TestPersistQueueOverflowDropsOldest(t *testing.T) {
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	logs := &logBuffer{}
	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return testBase },
		Logf:  logs.logf,
	})
	t.Cleanup(func() {
		mustDrain(t, svc)
		svc.Close()
	})
	release := holdStoreLock(t, path)
	svc.Register(Account{Key: "k", Aliases: []string{"k"}})

	const observations = 40
	svc.Observe(sampleSnapshot("k", testBase, 0))
	waitForWriterBusy(t, svc)
	for i := 1; i < observations; i++ {
		svc.Observe(sampleSnapshot("k", testBase.Add(time.Duration(i)*time.Second), float64(i)))
	}

	release()
	mustDrain(t, svc)

	// One append is in flight and persistQueueDepth jobs stay queued; every
	// further observation drops the oldest queued job.
	wantDropped := observations - 1 - persistQueueDepth
	if got := logs.count("persist queue full"); got != wantDropped {
		t.Errorf("logged %d queue-full drops, want %d (logs %v)", got, wantDropped, logs.messages())
	}

	stored, err := NewStore(path).readAll()
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if len(stored) == 0 {
		t.Fatal("no snapshot was persisted")
	}
	if newest := stored[len(stored)-1].Windows[0].UsedPercent; newest != observations-1 {
		t.Errorf("newest stored used = %v, want %d", newest, observations-1)
	}
	for _, snap := range stored {
		if used := snap.Windows[0].UsedPercent; used >= 1 && used <= float64(wantDropped) {
			t.Errorf("dropped observation with used %v was stored", used)
		}
	}
}

func TestConcurrentObserveAndDrain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-history.json")
	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return testBase },
	})
	t.Cleanup(func() {
		mustDrain(t, svc)
		svc.Close()
	})
	svc.Register(Account{Key: "k", Aliases: []string{"k"}})

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			svc.Observe(sampleSnapshot("k", testBase.Add(time.Duration(i)*time.Second), float64(i%4)))
		})
	}
	for range 8 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := svc.Drain(ctx); err != nil {
				t.Errorf("concurrent Drain: %v", err)
			}
		})
	}
	wg.Wait()
	mustDrain(t, svc)

	stored, err := NewStore(path).readAll()
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if len(stored) == 0 {
		t.Fatal("no snapshot was persisted alongside concurrent drains")
	}
}

// TestCloseStopsWriterAndDropsQueuedJobs pins Close's shutdown contract: queued
// jobs are dropped with a log, later observations are dropped too, and Drain
// after Close does not wait.
func TestCloseStopsWriterAndDropsQueuedJobs(t *testing.T) {
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	logs := &logBuffer{}
	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return testBase },
		Logf:  logs.logf,
	})
	release := holdStoreLock(t, path)
	svc.Register(Account{Key: "k", Aliases: []string{"k"}})

	svc.Observe(sampleSnapshot("k", testBase, 10))
	svc.Observe(sampleSnapshot("k", testBase.Add(time.Minute), 20))
	svc.Close()
	svc.Close() // idempotent

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := svc.Drain(ctx); err != nil {
		t.Fatalf("Drain after Close = %v, want nil", err)
	}

	svc.Observe(sampleSnapshot("k", testBase.Add(2*time.Minute), 30))
	if logs.count("persistence writer closed") == 0 {
		t.Errorf("logged %v, want a closed-writer drop", logs.messages())
	}

	release()
	select {
	case <-svc.writer.done:
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not stop after Close")
	}
	if pending := pendingJobs(svc); pending != 0 {
		t.Errorf("%d jobs still tracked after Close, want 0", pending)
	}
}

// TestCloseWakesWaitingDrain pins that Close releases a Drain already blocked on
// the writer, instead of leaving it to run out its context deadline.
func TestCloseWakesWaitingDrain(t *testing.T) {
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return testBase },
	})
	release := holdStoreLock(t, path)
	defer release()
	svc.Register(Account{Key: "k", Aliases: []string{"k"}})

	svc.Observe(sampleSnapshot("k", testBase, 10))
	waitForWriterBusy(t, svc)

	waiting := make(chan struct{})
	drained := make(chan error, 1)
	go func() {
		close(waiting)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		drained <- svc.Drain(ctx)
	}()
	<-waiting
	// Let the goroutine reach the select inside Drain before Close runs.
	time.Sleep(10 * time.Millisecond)

	svc.Close()
	select {
	case err := <-drained:
		if err != nil {
			t.Fatalf("Drain woken by Close = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not wake the waiting Drain")
	}
}
