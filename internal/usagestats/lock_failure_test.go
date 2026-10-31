package usagestats

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// failingFileLocker stands in for a platform whose file locking is unavailable.
type failingFileLocker struct{}

func (failingFileLocker) lock(uintptr) error   { return errors.New("locking unsupported") }
func (failingFileLocker) unlock(uintptr) error { return nil }

// TestStoreWriteFailsClosedWhenLockFails proves a lock failure aborts the write
// instead of persisting unlocked and clobbering concurrent writers.
func TestStoreWriteFailsClosedWhenLockFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache-stats.json")

	oldPath := storePath
	t.Cleanup(func() { storePath = oldPath })
	storePath = func() string { return path }

	s := newStore(newMockClock(time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)).Now)
	s.locker = failingFileLocker{}

	err := s.writeBatch(map[bucketKey]*bucket{
		{providerAlias: "p", providerType: "openai", backendModelID: "m"}: {Requests: 1},
	})
	if err == nil {
		t.Fatal("write succeeded, want lock error")
	}
	if !strings.Contains(err.Error(), "lock store file") {
		t.Fatalf("write error = %v, want wrapped lock failure", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("store file written despite lock failure: %v", statErr)
	}
}

// TestRecorderDegradesWhenLockFails proves a locking failure is contained: the
// observation is dropped from persistence but recording does not fail the run.
// The locker is swapped before any Record, so the writer goroutine only reads it
// after the happens-before edge of the first queued delta.
func TestRecorderDegradesWhenLockFails(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(TelemetryEnvVar, "")

	recorder := newTestRecorder(t, nil)
	recorder.store.locker = failingFileLocker{}

	recorder.Record(Observation{ProviderAlias: "p", ProviderType: "openai", BackendModelID: "m", Source: SourceParent})

	// Persistence is asynchronous: wait for the queued delta before checking the
	// file, otherwise the assertion races the writer.
	drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := recorder.Drain(drainCtx); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	if _, err := os.Stat(recorder.store.path); !os.IsNotExist(err) {
		t.Fatalf("store file written despite lock failure: %v", err)
	}
}
