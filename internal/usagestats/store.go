package usagestats

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	retentionDays = 8
	schemaVersion = 1
)

var storePath = defaultStorePath

func defaultStorePath() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "steiner", "cache-stats.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "steiner", "cache-stats.json")
}

// storeEntry is the on-disk representation of a bucket.
type storeEntry struct {
	Provider          string `json:"provider"`
	ProviderType      string `json:"provider_type"`
	Model             string `json:"model"`
	HourUnix          int64  `json:"hour_unix"`
	Requests          int    `json:"requests"`
	InputTokens       int    `json:"input_tokens"`
	CacheReadTokens   int    `json:"cache_read_tokens"`
	CacheCreateTokens int    `json:"cache_create_tokens"`
	CompletionTokens  int    `json:"completion_tokens"`
}

// storeFile is the on-disk format.
type storeFile struct {
	SchemaVersion int          `json:"schema_version"`
	Entries       []storeEntry `json:"entries"`
}

// fileLocker abstracts platform-specific file locking.
type fileLocker interface {
	lock(fd uintptr) error
	unlock(fd uintptr) error
}

// store manages durable persistence of cache stats.
type store struct {
	path   string
	locker fileLocker
	clock  func() time.Time
}

// newStore creates a new store. If clock is nil, time.Now is used.
func newStore(clock func() time.Time) *store {
	if clock == nil {
		clock = time.Now
	}
	return &store{
		path:   storePath(),
		locker: newFileLocker(),
		clock:  clock,
	}
}

// load reads the on-disk store and populates the buckets map.
// Missing file, corrupt JSON, or unknown schema → empty map, no error.
func (s *store) load() map[bucketKey]*bucket {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[bucketKey]*bucket)
		}
		// Read error: start empty, no error surfaced.
		return make(map[bucketKey]*bucket)
	}

	return s.decodeBuckets(data)
}

// decodeBuckets decodes entries from JSON data, applies pruning, and returns a bucket map.
func (s *store) decodeBuckets(data []byte) map[bucketKey]*bucket {
	buckets := make(map[bucketKey]*bucket)
	if len(data) == 0 {
		return buckets
	}

	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil || sf.SchemaVersion != schemaVersion {
		return buckets
	}

	cutoff := s.clock().Add(-time.Duration(retentionDays) * 24 * time.Hour).Unix()
	for _, ent := range sf.Entries {
		if ent.HourUnix <= cutoff {
			continue
		}
		key := bucketKey{
			providerAlias:  ent.Provider,
			providerType:   ent.ProviderType,
			backendModelID: ent.Model,
			hourUnix:       ent.HourUnix,
		}
		buckets[key] = &bucket{
			Requests:          ent.Requests,
			InputTokens:       ent.InputTokens,
			CacheReadTokens:   ent.CacheReadTokens,
			CacheCreateTokens: ent.CacheCreateTokens,
			CompletionTokens:  ent.CompletionTokens,
		}
	}

	return buckets
}

// atomicWrite marshals buckets to JSON and writes to disk with temp+rename.
func (s *store) atomicWrite(dir string, buckets map[bucketKey]*bucket) error {
	entries := make([]storeEntry, 0, len(buckets))
	for key, b := range buckets {
		entries = append(entries, storeEntry{
			Provider:          key.providerAlias,
			ProviderType:      key.providerType,
			Model:             key.backendModelID,
			HourUnix:          key.hourUnix,
			Requests:          b.Requests,
			InputTokens:       b.InputTokens,
			CacheReadTokens:   b.CacheReadTokens,
			CacheCreateTokens: b.CacheCreateTokens,
			CompletionTokens:  b.CompletionTokens,
		})
	}

	data, err := json.Marshal(storeFile{
		SchemaVersion: schemaVersion,
		Entries:       entries,
	})
	if err != nil {
		return fmt.Errorf("marshal store: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-cache-stats-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("rename store file: %w", err)
	}

	return nil
}

// write persists the delta to disk with additive-delta merge under lock.
func (s *store) write(delta *bucket, deltaKey bucketKey) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("open store file: %w", err)
	}
	defer f.Close() //nolint:errcheck

	if err := s.locker.lock(f.Fd()); err != nil {
		return fmt.Errorf("lock store file: %w", err)
	}
	defer func() {
		_ = s.locker.unlock(f.Fd())
	}()

	currentData, err := os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read current store: %w", err)
	}

	diskBuckets := s.decodeBuckets(currentData)

	if _, ok := diskBuckets[deltaKey]; !ok {
		diskBuckets[deltaKey] = &bucket{}
	}
	diskBuckets[deltaKey].Requests += delta.Requests
	diskBuckets[deltaKey].InputTokens += delta.InputTokens
	diskBuckets[deltaKey].CacheReadTokens += delta.CacheReadTokens
	diskBuckets[deltaKey].CacheCreateTokens += delta.CacheCreateTokens
	diskBuckets[deltaKey].CompletionTokens += delta.CompletionTokens

	cutoff := s.clock().Add(-time.Duration(retentionDays) * 24 * time.Hour).Unix()
	for key := range diskBuckets {
		if key.hourUnix <= cutoff {
			delete(diskBuckets, key)
		}
	}

	return s.atomicWrite(dir, diskBuckets)
}
