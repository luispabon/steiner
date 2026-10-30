package quota

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/luispabon/steiner/internal/fsatomic"
)

const (
	storeVersion = 1
	// retentionDays bounds how long snapshots are kept, measured from the
	// Append call that prunes them.
	retentionDays = 35
	// dedupeWindow is how recent an identical stored state must be for Append
	// to skip the write.
	dedupeWindow = time.Hour
)

// Store persists snapshot history in one JSON file. Appends take an exclusive
// lock on a sibling .lock file, merge with the current file contents, and
// replace the file atomically, so separate processes do not drop each other's
// snapshots. Reads take no lock; the atomic rename keeps them consistent.
type Store struct {
	path string
}

// storeFile is the on-disk format.
type storeFile struct {
	Version   int        `json:"version"`
	Snapshots []Snapshot `json:"snapshots"`
}

// NewStore returns a Store that persists to path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// DefaultStorePath returns $XDG_STATE_HOME/steiner/quota-history.json, or
// ~/.local/state/steiner/quota-history.json when XDG_STATE_HOME is unset.
func DefaultStorePath() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "steiner", "quota-history.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", "steiner", "quota-history.json"), nil
}

// Append persists snap. It skips the write when the newest stored snapshot for
// the same account has identical state (ignoring FetchedAt and Source, and
// comparing reset times within resetsAtTolerance) and was fetched less than an
// hour before now. Snapshots fetched more than 35 days before now are pruned.
func (s *Store) Append(snap Snapshot, now time.Time) error {
	_, err := s.append(snap, now)
	return err
}

// append is Append reporting whether it wrote the file: it returns false when
// the newest stored state already matches snap. Callers that track the persisted
// baseline need that distinction, because a skipped dedupe leaves the file's
// newest snapshot in place.
func (s *Store) append(snap Snapshot, now time.Time) (bool, error) {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("create quota store dir: %w", err)
	}
	// The lock is taken on a sibling file, never the data file: write replaces
	// the data file's inode on every write, so a lock on it would not exclude
	// a writer that opened the previous inode.
	lock, err := os.OpenFile(s.path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return false, fmt.Errorf("open quota store lock: %w", err)
	}
	defer lock.Close() //nolint:errcheck // Close only releases the lock file handle; nothing is written through it.
	// Tighten a lock file left by an older writer. Failing here stops the append
	// rather than writing history under a loose lock file.
	if err := lock.Chmod(0o600); err != nil {
		return false, fmt.Errorf("chmod quota store lock: %w", err)
	}
	if err := lockFile(lock.Fd()); err != nil {
		return false, fmt.Errorf("lock quota store: %w", err)
	}
	defer func() {
		_ = unlockFile(lock.Fd()) // Closing the handle also drops the lock.
	}()

	snaps, err := s.readAll()
	if err != nil {
		return false, err
	}
	if prev, ok := latestOf(snaps, snap.AccountKey); ok && sameState(prev, snap) && now.Sub(prev.FetchedAt) < dedupeWindow {
		return false, nil
	}

	cutoff := now.Add(-retentionDays * 24 * time.Hour)
	kept := snaps[:0]
	for _, old := range snaps {
		if !old.FetchedAt.Before(cutoff) {
			kept = append(kept, old)
		}
	}
	return true, s.write(append(kept, snap))
}

// Latest returns the newest stored snapshot for each account key.
func (s *Store) Latest() (map[string]Snapshot, error) {
	snaps, err := s.readAll()
	if err != nil {
		return nil, err
	}
	latest := make(map[string]Snapshot)
	for _, snap := range snaps {
		latest[snap.AccountKey] = snap
	}
	return latest, nil
}

// readAll returns every stored snapshot sorted oldest first. A missing file
// yields no snapshots. A file that fails to decode is an error, so Append never
// overwrites history it could not read.
func (s *Store) readAll() ([]Snapshot, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read quota store: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("decode quota store: %w", err)
	}
	if sf.Version != storeVersion {
		return nil, fmt.Errorf("decode quota store: unsupported version %d", sf.Version)
	}
	slices.SortStableFunc(sf.Snapshots, func(a, b Snapshot) int {
		return a.FetchedAt.Compare(b.FetchedAt)
	})
	return sf.Snapshots, nil
}

// write replaces the store file with snaps through a temp file and rename.
func (s *Store) write(snaps []Snapshot) error {
	data, err := json.Marshal(storeFile{Version: storeVersion, Snapshots: snaps})
	if err != nil {
		return fmt.Errorf("marshal quota store: %w", err)
	}
	if err := fsatomic.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write quota store: %w", err)
	}
	return nil
}

// latestOf returns the newest snapshot for key in snaps, which must be sorted
// oldest first.
func latestOf(snaps []Snapshot, key string) (Snapshot, bool) {
	for i := len(snaps) - 1; i >= 0; i-- {
		if snaps[i].AccountKey == key {
			return snaps[i], true
		}
	}
	return Snapshot{}, false
}
