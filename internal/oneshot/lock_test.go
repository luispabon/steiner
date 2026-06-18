package oneshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireRunLockBlocksSecondHolder(t *testing.T) {
	dir := t.TempDir()
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}

	lock, err := acquireRunLock(dir, identity, time.Hour)
	if err != nil {
		t.Fatalf("acquireRunLock failed: %v", err)
	}
	t.Cleanup(func() {
		_ = lock.Release()
	})

	if _, err := acquireRunLock(dir, identity, time.Hour); err != errLockHeld {
		t.Fatalf("second acquire error = %v, want errLockHeld", err)
	}
}

func TestAcquireRunLockReclaimsStaleLock(t *testing.T) {
	dir := t.TempDir()
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	path := identity.LockPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	stale := LockRecord{
		Owner:      "stale-owner",
		AcquiredAt: time.Now().Add(-2 * time.Hour).UTC(),
		UpdatedAt:  time.Now().Add(-2 * time.Hour).UTC(),
	}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal stale lock: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes stale lock: %v", err)
	}

	lock, err := acquireRunLock(dir, identity, time.Minute)
	if err != nil {
		t.Fatalf("reclaim stale lock failed: %v", err)
	}
	t.Cleanup(func() {
		_ = lock.Release()
	})

	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read reclaimed lock: %v", err)
	}
	var record LockRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("unmarshal reclaimed lock: %v", err)
	}
	if record.Owner != identity.ID {
		t.Fatalf("record.Owner = %q, want %q", record.Owner, identity.ID)
	}
}
