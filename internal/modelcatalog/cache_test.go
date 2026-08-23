package modelcatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestCacheRoundTripAndFilename(t *testing.T) {
	cache := NewCache(t.TempDir())
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	cache.now = func() time.Time { return now }
	models := []DiscoveredModel{{
		ProviderAlias: "openai",
		ProviderType:  "openai",
		ID:            "gpt-4.1",
		DisplayName:   "GPT-4.1",
		ContextLength: 128000,
	}}

	if err := cache.SaveAtomic("fixed-alias", CacheEnvelope{
		Fingerprint: CacheFingerprint{ProviderType: "openai", BaseURL: "https://api.openai.com/v1"},
		ETag:        "etag-1",
		Models:      models,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, found, err := cache.Load("fixed-alias", "openai", "https://api.openai.com/v1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !found || !reflect.DeepEqual(got, models) {
		t.Fatalf("load: found=%v models=%+v, want found=true models=%+v", found, got, models)
	}
	envelope, err := cache.readEnvelope("fixed-alias")
	if err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	if envelope.ETag != "etag-1" {
		t.Fatalf("etag: got %q, want %q", envelope.ETag, "etag-1")
	}
	if envelope.SchemaVersion != CacheSchemaVersion || !envelope.FetchedAt.Equal(now) || !envelope.ExpiresAt.Equal(now.Add(CacheTTL)) {
		t.Fatalf("timestamps: got %+v", envelope)
	}

	digest := sha256.Sum256([]byte("fixed-alias"))
	wantName := hex.EncodeToString(digest[:])[:16] + ".json"
	entries, err := os.ReadDir(cache.Dir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	foundName := false
	for _, entry := range entries {
		if entry.Name() == wantName {
			foundName = true
		}
	}
	if !foundName {
		t.Fatalf("cache directory lacks %q", wantName)
	}
}

func TestDefaultCacheDirHomeFallbacks(t *testing.T) {
	tests := []struct {
		name string
		home string
		want string
	}{
		{
			name: "home",
			home: filepath.Join(t.TempDir(), "home"),
		},
		{
			name: "temp when home is unavailable",
			want: filepath.Join(os.TempDir(), ".cache", "steiner", "provider-models"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", "")
			t.Setenv("HOME", tt.home)
			want := tt.want
			if want == "" {
				want = filepath.Join(tt.home, ".cache", "steiner", "provider-models")
			}
			if got := DefaultCacheDir(); got != want {
				t.Fatalf("DefaultCacheDir() = %q, want %q", got, want)
			}
		})
	}
}

func TestCacheTTLAndStaleLoad(t *testing.T) {
	cache := NewCache(t.TempDir())
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	cache.now = func() time.Time { return now }
	if err := cache.SaveAtomic("alias", CacheEnvelope{
		Fingerprint: CacheFingerprint{ProviderType: "ollama", BaseURL: "http://localhost:11434"},
		Models:      []DiscoveredModel{{ID: "llama3"}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !cache.IsFresh("alias", "ollama", "http://localhost:11434") {
		t.Fatal("cache should be fresh before expiry")
	}

	now = now.Add(CacheTTL)
	if cache.IsFresh("alias", "ollama", "http://localhost:11434") {
		t.Fatal("cache should be stale at expiry")
	}
	models, found, err := cache.Load("alias", "ollama", "http://localhost:11434")
	if err != nil {
		t.Fatalf("stale load: %v", err)
	}
	if !found || len(models) != 1 || models[0].ID != "llama3" {
		t.Fatalf("stale load: found=%v models=%+v", found, models)
	}
}

func TestCacheFingerprintMismatchIsMiss(t *testing.T) {
	cache := NewCache(t.TempDir())
	if err := cache.SaveAtomic("alias", CacheEnvelope{
		Fingerprint: CacheFingerprint{ProviderType: "openai", BaseURL: "https://one.example"},
		Models:      []DiscoveredModel{{ID: "model"}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	tests := []struct {
		name         string
		providerType string
		baseURL      string
	}{
		{name: "different type", providerType: "anthropic", baseURL: "https://one.example"},
		{name: "different base URL", providerType: "openai", baseURL: "https://two.example"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			models, found, err := cache.Load("alias", test.providerType, test.baseURL)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if found || models != nil || cache.IsFresh("alias", test.providerType, test.baseURL) {
				t.Fatalf("got found=%v models=%+v, want miss", found, models)
			}
		})
	}
}

func TestCacheCorruptJSONIsMiss(t *testing.T) {
	cache := NewCache(t.TempDir())
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("create cache dir: %v", err)
	}
	if err := os.WriteFile(cache.path("alias"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}

	models, found, err := cache.Load("alias", "openai", "https://example.com")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if found || models != nil || cache.IsFresh("alias", "openai", "https://example.com") {
		t.Fatalf("got found=%v models=%+v, want miss", found, models)
	}
}

func TestCacheExtendFreshness(t *testing.T) {
	cache := NewCache(t.TempDir())
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	cache.now = func() time.Time { return now }
	if err := cache.SaveAtomic("alias", CacheEnvelope{
		Fingerprint: CacheFingerprint{ProviderType: "codex", BaseURL: "https://example.com"},
		ETag:        "etag-1",
		Models:      []DiscoveredModel{{ID: "model"}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	before, err := os.ReadFile(cache.path("alias"))
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	now = now.Add(time.Hour)
	if !cache.ExtendFreshness("alias", "wrong") {
		if after, readErr := os.ReadFile(cache.path("alias")); readErr != nil || !reflect.DeepEqual(after, before) {
			t.Fatalf("mismatched etag changed cache: %v", readErr)
		}
	} else {
		t.Fatal("mismatched etag unexpectedly extended cache")
	}
	if !cache.ExtendFreshness("alias", "etag-1") {
		t.Fatal("matching etag did not extend cache")
	}
	envelope, err := cache.readEnvelope("alias")
	if err != nil {
		t.Fatalf("read after extension: %v", err)
	}
	if !envelope.ExpiresAt.Equal(now.Add(CacheTTL)) {
		t.Fatalf("expiry: got %s, want %s", envelope.ExpiresAt, now.Add(CacheTTL))
	}
}

func TestCacheConcurrentSaveAtomic(t *testing.T) {
	cache := NewCache(t.TempDir())
	const writers = 32
	var wait sync.WaitGroup
	errs := make(chan error, writers)
	wait.Add(writers)
	for range writers {
		go func() {
			defer wait.Done()
			errs <- cache.SaveAtomic("alias", CacheEnvelope{
				Fingerprint: CacheFingerprint{ProviderType: "openai", BaseURL: "https://example.com"},
				Models:      []DiscoveredModel{{ID: "model"}},
			})
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent save: %v", err)
		}
	}

	entries, err := os.ReadDir(cache.Dir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	jsonFiles := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".json" {
			jsonFiles++
		}
		if len(entry.Name()) >= len(".tmp-provider-models-") && entry.Name()[:len(".tmp-provider-models-")] == ".tmp-provider-models-" {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
	if jsonFiles != 1 {
		t.Fatalf("json files: got %d, want 1", jsonFiles)
	}
	data, err := os.ReadFile(cache.path("alias"))
	if err != nil {
		t.Fatalf("read final cache: %v", err)
	}
	var envelope CacheEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("parse final cache: %v", err)
	}
	if envelope.SchemaVersion != CacheSchemaVersion || len(envelope.Models) != 1 {
		t.Fatalf("final envelope: %+v", envelope)
	}
}
