package usagestats

import (
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

	err := s.write(&bucket{Requests: 1}, bucketKey{providerAlias: "p", providerType: "openai", backendModelID: "m"})
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
func TestRecorderDegradesWhenLockFails(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(TelemetryEnvVar, "")

	recorder := New(nil)
	recorder.store.locker = failingFileLocker{}

	recorder.Record(Observation{ProviderAlias: "p", ProviderType: "openai", BackendModelID: "m", Source: SourceParent})

	if _, err := os.Stat(recorder.store.path); !os.IsNotExist(err) {
		t.Fatalf("store file written despite lock failure: %v", err)
	}
}
