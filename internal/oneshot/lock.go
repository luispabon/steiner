package oneshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const defaultLockStaleAfter = 15 * time.Minute

var errLockHeld = errors.New("oneshot run lock is held")

// LockRecord is the durable metadata stored in the lock file.
type LockRecord struct {
	Owner      string    `json:"owner"`
	AcquiredAt time.Time `json:"acquired_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Generation string    `json:"generation,omitempty"`
}

// RunLock manages the lifecycle of a per-run lock file.
type RunLock struct {
	path   string
	mu     sync.Mutex
	record LockRecord
}

// AcquireRunLock atomically acquires the lock or reclaims a stale one.
func AcquireRunLock(projectRoot string, identity RunIdentity) (*RunLock, error) {
	return acquireRunLock(projectRoot, identity, defaultLockStaleAfter)
}

func acquireRunLock(projectRoot string, identity RunIdentity, staleAfter time.Duration) (*RunLock, error) {
	path := identity.LockPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}

	record := newLockRecord(identity.ID)
	if err := writeLockExclusive(path, record); err == nil {
		return &RunLock{path: path, record: record}, nil
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}

	existing, meta, err := readLockRecord(path)
	if err != nil {
		return nil, err
	}
	if !isLockStale(existing, meta, staleAfter) {
		return nil, errLockHeld
	}

	for attempts := 0; attempts < 3; attempts++ {
		current, currentMeta, err := readLockRecord(path)
		if err != nil {
			return nil, err
		}
		if current.Owner != existing.Owner || !current.UpdatedAt.Equal(existing.UpdatedAt) || !currentMeta.ModTime().Equal(meta.ModTime()) {
			return nil, errLockHeld
		}
		if err := os.Remove(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("remove stale lock: %w", err)
		}
		if err := writeLockExclusive(path, record); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return nil, err
		}
		return &RunLock{path: path, record: record}, nil
	}

	return nil, errLockHeld
}

// Heartbeat updates the lock file mtime to prevent staleness detection.
func (l *RunLock) Heartbeat() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = os.Chtimes(l.path, time.Now(), time.Now())
}

// Release removes the lock file if it is still present.
func (l *RunLock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove lock: %w", err)
	}
	return nil
}

func newLockRecord(owner string) LockRecord {
	now := time.Now().UTC()
	return LockRecord{
		Owner:      owner,
		AcquiredAt: now,
		UpdatedAt:  now,
	}
}

func writeLockExclusive(path string, record LockRecord) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	data, err := json.Marshal(record)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("marshal lock record: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write lock record: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("sync lock record: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close lock record: %w", err)
	}
	if err := os.Chtimes(path, record.UpdatedAt, record.UpdatedAt); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("stamp lock record: %w", err)
	}
	return nil
}

func readLockRecord(path string) (LockRecord, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return LockRecord{}, nil, fmt.Errorf("stat lock: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return LockRecord{}, nil, fmt.Errorf("read lock: %w", err)
	}
	var record LockRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return LockRecord{}, nil, fmt.Errorf("parse lock record: %w", err)
	}
	return record, info, nil
}

func isLockStale(record LockRecord, info os.FileInfo, staleAfter time.Duration) bool {
	if staleAfter <= 0 {
		return false
	}
	if record.UpdatedAt.IsZero() {
		return true
	}
	return time.Since(info.ModTime()) > staleAfter
}
