package modelcatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// CacheSchemaVersion is the version of the provider model cache envelope.
	CacheSchemaVersion = 1
	// CacheTTL is the time a saved provider model cache remains fresh.
	CacheTTL = 7 * 24 * time.Hour
)

// CacheFingerprint identifies the provider configuration used to discover models.
type CacheFingerprint struct {
	ProviderType string `json:"provider_type"`
	BaseURL      string `json:"base_url"`
}

// CacheEnvelope is the persistent provider model cache format.
type CacheEnvelope struct {
	SchemaVersion int               `json:"schema_version"`
	Fingerprint   CacheFingerprint  `json:"fingerprint"`
	FetchedAt     time.Time         `json:"fetched_at"`
	ExpiresAt     time.Time         `json:"expires_at"`
	ETag          string            `json:"etag,omitempty"`
	Models        []DiscoveredModel `json:"models"`
}

// Cache stores one persistent model discovery envelope per provider alias.
type Cache struct {
	Dir string
	now func() time.Time
}

// DefaultCacheDir returns the default provider model cache directory, honoring
// XDG_CACHE_HOME when set.
func DefaultCacheDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "steiner", "provider-models")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "steiner", "provider-models")
}

// NewCache creates a provider model cache. An empty dir uses DefaultCacheDir.
func NewCache(dir string) *Cache {
	if dir == "" {
		dir = DefaultCacheDir()
	}
	return &Cache{Dir: dir, now: time.Now}
}

// Load returns cached models and whether a matching envelope was found. Stale
// envelopes are returned because discovery callers may use them as a fallback.
// Missing, corrupt, or fingerprint-mismatched envelopes return found=false.
func (c *Cache) Load(alias, providerType, baseURL string) ([]DiscoveredModel, bool, error) {
	release, err := c.lock(alias)
	if err != nil {
		return nil, false, fmt.Errorf("acquire cache lock: %w", err)
	}
	defer release()

	envelope, found, err := c.loadEnvelope(alias, providerType, baseURL)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	return envelope.Models, true, nil
}

// IsFresh reports whether a matching provider envelope exists and has not
// expired. Fingerprint mismatches and unreadable envelopes are not fresh.
func (c *Cache) IsFresh(alias, providerType, baseURL string) bool {
	release, err := c.lock(alias)
	if err != nil {
		return false
	}
	defer release()

	envelope, found, err := c.loadEnvelope(alias, providerType, baseURL)
	return err == nil && found && c.now().Before(envelope.ExpiresAt)
}

// SaveAtomic writes envelope for alias using a temporary file and rename. It
// sets schema and freshness timestamps from the cache clock, then serializes
// the result while holding the provider's exclusive cache lock.
func (c *Cache) SaveAtomic(alias string, envelope CacheEnvelope) error {
	now := c.now().UTC()
	envelope.SchemaVersion = CacheSchemaVersion
	envelope.FetchedAt = now
	envelope.ExpiresAt = now.Add(CacheTTL)

	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	release, err := c.lock(alias)
	if err != nil {
		return fmt.Errorf("acquire cache lock: %w", err)
	}
	defer release()

	data, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal cache envelope: %w", err)
	}
	if err := c.writeAtomic(alias, data); err != nil {
		return err
	}
	return nil
}

// ExtendFreshness extends a cached envelope only when its stored ETag matches
// etag. It returns false for a missing, corrupt, or differently tagged cache.
func (c *Cache) ExtendFreshness(alias, etag string) bool {
	release, err := c.lock(alias)
	if err != nil {
		return false
	}
	defer release()

	envelope, err := c.readEnvelope(alias)
	if err != nil || etag == "" || envelope.ETag != etag || !validEnvelope(envelope) {
		return false
	}
	envelope.ExpiresAt = c.now().UTC().Add(CacheTTL)
	data, err := json.Marshal(envelope)
	if err != nil {
		return false
	}
	return c.writeAtomic(alias, data) == nil
}

func (c *Cache) loadEnvelope(alias, providerType, baseURL string) (CacheEnvelope, bool, error) {
	envelope, err := c.readEnvelope(alias)
	if os.IsNotExist(err) {
		return CacheEnvelope{}, false, nil
	}
	if err != nil {
		if isCorruptCacheError(err) {
			return CacheEnvelope{}, false, nil
		}
		return CacheEnvelope{}, false, err
	}
	if !validEnvelope(envelope) ||
		envelope.Fingerprint.ProviderType != providerType ||
		envelope.Fingerprint.BaseURL != baseURL {
		return CacheEnvelope{}, false, nil
	}
	return envelope, true, nil
}

func validEnvelope(envelope CacheEnvelope) bool {
	return envelope.SchemaVersion == CacheSchemaVersion &&
		!envelope.FetchedAt.IsZero() && !envelope.ExpiresAt.IsZero()
}

func (c *Cache) readEnvelope(alias string) (CacheEnvelope, error) {
	data, err := os.ReadFile(c.path(alias))
	if err != nil {
		if os.IsNotExist(err) {
			return CacheEnvelope{}, err
		}
		return CacheEnvelope{}, fmt.Errorf("read cache envelope: %w", err)
	}
	var envelope CacheEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return CacheEnvelope{}, corruptCacheError{err: err}
	}
	return envelope, nil
}

func (c *Cache) writeAtomic(alias string, data []byte) error {
	tmp, err := os.CreateTemp(c.Dir, ".tmp-provider-models-*")
	if err != nil {
		return fmt.Errorf("create cache temp file: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write cache temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close cache temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod cache temp file: %w", err)
	}
	if err := os.Rename(tmpName, c.path(alias)); err != nil {
		return fmt.Errorf("rename cache envelope: %w", err)
	}
	removeTemp = false
	return nil
}

func (c *Cache) lock(alias string) (func(), error) {
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	release, err := acquireFileLock(c.lockPath(alias))
	if err != nil {
		return nil, err
	}
	return release, nil
}

func (c *Cache) path(alias string) string {
	return filepath.Join(c.Dir, cacheFilename(alias))
}

func (c *Cache) lockPath(alias string) string {
	digest := sha256.Sum256([]byte(alias))
	return filepath.Join(c.Dir, hex.EncodeToString(digest[:])[:16]+".lock")
}

func cacheFilename(alias string) string {
	digest := sha256.Sum256([]byte(alias))
	return hex.EncodeToString(digest[:])[:16] + ".json"
}

type corruptCacheError struct {
	err error
}

func (e corruptCacheError) Error() string {
	return e.err.Error()
}

func (e corruptCacheError) Unwrap() error {
	return e.err
}

func isCorruptCacheError(err error) bool {
	var corrupt corruptCacheError
	return errors.As(err, &corrupt)
}
