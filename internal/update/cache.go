package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// checkCache holds the persisted result of the most recent passive update
// check, used to avoid hitting GitHub's API on every launch.
type checkCache struct {
	CurrentVersion string    `json:"current_version"`
	Channel        string    `json:"channel"`
	LatestVersion  string    `json:"latest_version"`
	NeedsUpdate    bool      `json:"needs_update"`
	CheckedAt      time.Time `json:"checked_at"`
}

// DefaultCachePath returns the default path for the update-check cache file,
// honouring XDG_CACHE_HOME when set.
func DefaultCachePath() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "steiner", "update-check.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "steiner", "update-check.json")
}

// CheckCached reports whether a newer steiner release is available, using an
// on-disk TTL cache at cachePath to avoid repeated GitHub API calls within
// interval. If the cached entry matches currentVersion and channel and is
// within interval, it is returned without a network call. Otherwise a fresh
// check is performed via Check and the result (success or failure) is cached
// so a failing check is not retried until the next interval.
func CheckCached(ctx context.Context, cachePath string, interval time.Duration, currentVersion, owner, repo, token, channel string) (latestVersion string, needsUpdate bool, err error) {
	if cached, ok := loadCheckCache(cachePath); ok {
		if cached.CurrentVersion == currentVersion && cached.Channel == channel && time.Since(cached.CheckedAt) < interval {
			return cached.LatestVersion, cached.NeedsUpdate, nil
		}
	}

	latestVersion, needsUpdate, err = Check(ctx, currentVersion, owner, repo, token, channel, "")

	entry := checkCache{
		CurrentVersion: currentVersion,
		Channel:        channel,
		LatestVersion:  latestVersion,
		NeedsUpdate:    needsUpdate,
		CheckedAt:      time.Now(),
	}
	// Best-effort write: a cache write failure (e.g. read-only filesystem)
	// must not fail the caller; only the network result matters.
	_ = saveCheckCache(cachePath, entry)

	return latestVersion, needsUpdate, err
}

// loadCheckCache loads the cache entry at path. A missing file or corrupt
// contents are treated as a cache miss, not an error.
func loadCheckCache(path string) (checkCache, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return checkCache{}, false
	}
	var entry checkCache
	if err := json.Unmarshal(data, &entry); err != nil {
		return checkCache{}, false
	}
	return entry, true
}

// saveCheckCache writes entry to path, creating the parent directory if
// needed.
func saveCheckCache(path string, entry checkCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
