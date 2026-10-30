package quota

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestObserveSkipsAppendForUnchangedStateWithinHour(t *testing.T) {
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	var logs []string
	clock := testBase
	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return clock },
		Logf: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	})
	t.Cleanup(func() { mustDrain(t, svc) })

	svc.Register(Account{Key: "k", Aliases: []string{"a"}})

	// The first observation is appended normally and records the append time.
	svc.Observe(sampleSnapshot("k", testBase, 10))
	mustDrain(t, svc)
	if len(logs) != 0 {
		t.Fatalf("first observation logged %v", logs)
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
			before := len(logs)
			svc.Observe(sampleSnapshot("k", step.at, step.used))
			mustDrain(t, svc)

			if attempted := len(logs) > before; attempted != step.wantAppend {
				t.Errorf("store append attempted = %v, want %v (logs %v)", attempted, step.wantAppend, logs[before:])
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
// writer goroutine blocks in Store.Append until the returned release runs.
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

func TestObserveDoesNotWaitForStoreLock(t *testing.T) {
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	release := holdStoreLock(t, path)

	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return testBase },
	})
	t.Cleanup(func() { mustDrain(t, svc) })
	svc.Register(Account{Key: "k", Aliases: []string{"a"}})

	observed := make(chan struct{})
	go func() {
		defer close(observed)
		svc.Observe(sampleSnapshot("k", testBase, 10))
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

	latest, err := NewStore(path).Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	snap, ok := latest["k"]
	if !ok {
		t.Fatal("snapshot was not persisted after the drain")
	}
	if len(snap.Windows) != 1 || snap.Windows[0].UsedPercent != 10 {
		t.Errorf("persisted snapshot = %+v, want the observed 10%% window", snap)
	}
}

func TestDrainFlushesPendingWritesInOrder(t *testing.T) {
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	release := holdStoreLock(t, path)

	svc := NewService(ServiceOptions{
		Store: NewStore(path),
		Now:   func() time.Time { return testBase },
	})
	t.Cleanup(func() { mustDrain(t, svc) })
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
