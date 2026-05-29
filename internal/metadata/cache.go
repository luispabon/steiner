// Package metadata provides a local JSON cache of model metadata from models.dev.
//
// models.dev data is MIT licensed. See https://models.dev
package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	modelsDevURL  = "https://models.dev/api.json"
	cacheTTL      = 7 * 24 * time.Hour
	schemaVersion = "1"
	cacheFilename = "models.dev.json"
	metaFilename  = "models.dev.meta.json"
)

// CacheMetadata holds HTTP cache headers and freshness info.
type CacheMetadata struct {
	DownloadedAt  time.Time `json:"downloaded_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	ETag          string    `json:"etag,omitempty"`
	LastModified  string    `json:"last_modified,omitempty"`
	URL           string    `json:"url"`
	SchemaVersion string    `json:"schema_version,omitempty"`
}

// Cache manages the local models.dev JSON cache.
type Cache struct {
	// Dir is the directory where cache files live.
	Dir string
	// HTTPClient is the HTTP client used for fetching. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// DefaultCacheDir returns the default models.dev cache directory, honouring
// XDG_CACHE_HOME when set.
func DefaultCacheDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "steiner", "model-metadata")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "steiner", "model-metadata")
}

// CachePath returns the path to the main JSON cache file.
func (c *Cache) CachePath() string {
	return filepath.Join(c.Dir, cacheFilename)
}

// MetaPath returns the path to the cache metadata file.
func (c *Cache) MetaPath() string {
	return filepath.Join(c.Dir, metaFilename)
}

// IsFresh reports whether the cache is within its TTL.
func (c *Cache) IsFresh() bool {
	meta, err := c.LoadMetadata()
	if err != nil || meta.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Before(meta.ExpiresAt)
}

// Load loads the cached JSON data. Returns nil if cache is missing.
func (c *Cache) Load() ([]byte, error) {
	data, err := os.ReadFile(c.CachePath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cache: %w", err)
	}
	return data, nil
}

// LoadBestEffort refreshes stale metadata before loading cached JSON.
// Refresh is opportunistic and offline-safe: any refresh failure falls back to
// whatever cache data is already available on disk.
func (c *Cache) LoadBestEffort(ctx context.Context) ([]byte, error) {
	if !c.IsFresh() {
		if err := c.Refresh(ctx); err != nil {
			// Fall back to stale cache — the data file may exist even if
			// metadata save failed during a previous refresh.
			return c.Load()
		}
	}
	return c.Load()
}

// LoadMetadata loads the cache metadata. Returns zero CacheMetadata if missing.
func (c *Cache) LoadMetadata() (CacheMetadata, error) {
	data, err := os.ReadFile(c.MetaPath())
	if os.IsNotExist(err) {
		return CacheMetadata{}, nil
	}
	if err != nil {
		return CacheMetadata{}, fmt.Errorf("read metadata: %w", err)
	}
	var meta CacheMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return CacheMetadata{}, fmt.Errorf("parse metadata: %w", err)
	}
	return meta, nil
}

// Refresh fetches fresh data from models.dev (or returns cached if 304 Not Modified).
// Non-fatal: returns nil error on network failure when stale cache is available.
func (c *Cache) Refresh(ctx context.Context) error {
	existingMeta, _ := c.LoadMetadata()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	if existingMeta.ETag != "" {
		req.Header.Set("If-None-Match", existingMeta.ETag)
	}
	if existingMeta.LastModified != "" {
		req.Header.Set("If-Modified-Since", existingMeta.LastModified)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		// Network failure: stale cache is acceptable.
		return nil
	}
	defer resp.Body.Close() //nolint:errcheck

	switch resp.StatusCode {
	case http.StatusNotModified:
		// Cache is still valid; bump expires_at only.
		existingMeta.ExpiresAt = time.Now().Add(cacheTTL)
		return c.saveMetadata(existingMeta)

	case http.StatusOK:
		// Read and validate body.
		buf, err := io.ReadAll(resp.Body)
		if err != nil {
			// Non-fatal.
			return nil
		}
		// Validate JSON parse before writing.
		var check any
		if err := json.Unmarshal(buf, &check); err != nil {
			// Body not valid JSON; don't corrupt cache.
			return nil
		}
		if err := atomicWrite(c.CachePath(), buf); err != nil {
			return fmt.Errorf("atomic write cache: %w", err)
		}
		now := time.Now()
		meta := CacheMetadata{
			DownloadedAt:  now,
			ExpiresAt:     now.Add(cacheTTL),
			ETag:          resp.Header.Get("ETag"),
			LastModified:  resp.Header.Get("Last-Modified"),
			URL:           modelsDevURL,
			SchemaVersion: schemaVersion,
		}
		return c.saveMetadata(meta)

	default:
		// 4xx, 5xx — non-fatal; stale cache is acceptable.
		return nil
	}
}

// Clear removes both cache files.
func (c *Cache) Clear() error {
	var errs []error
	for _, p := range []string{c.CachePath(), c.MetaPath()} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("clear cache: %v", errs)
	}
	return nil
}

func (c *Cache) saveMetadata(meta CacheMetadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	return atomicWrite(c.MetaPath(), data)
}

// atomicWrite writes data to path via a temp file + rename to avoid partial writes.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-models-dev-*")
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
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("rename cache file: %w", err)
	}
	return nil
}
