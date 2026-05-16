package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	dir := t.TempDir()
	return &Cache{Dir: dir}
}

func writeTestData(t *testing.T, c *Cache, data []byte) {
	t.Helper()
	if err := atomicWrite(c.CachePath(), data); err != nil {
		t.Fatalf("write test data: %v", err)
	}
}

func writeTestMeta(t *testing.T, c *Cache, meta CacheMetadata) {
	t.Helper()
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err := atomicWrite(c.MetaPath(), b); err != nil {
		t.Fatalf("write test meta: %v", err)
	}
}

func TestIsFresh_WithinTTL(t *testing.T) {
	c := newTestCache(t)
	meta := CacheMetadata{
		DownloadedAt: time.Now().Add(-time.Hour),
		ExpiresAt:    time.Now().Add(time.Hour),
		URL:          modelsDevURL,
	}
	writeTestMeta(t, c, meta)
	if !c.IsFresh() {
		t.Fatal("expected cache to be fresh")
	}
}

func TestIsFresh_Expired(t *testing.T) {
	c := newTestCache(t)
	meta := CacheMetadata{
		DownloadedAt: time.Now().Add(-8 * 24 * time.Hour),
		ExpiresAt:    time.Now().Add(-24 * time.Hour),
		URL:          modelsDevURL,
	}
	writeTestMeta(t, c, meta)
	if c.IsFresh() {
		t.Fatal("expected cache to be expired")
	}
}

func TestIsFresh_NoMeta(t *testing.T) {
	c := newTestCache(t)
	if c.IsFresh() {
		t.Fatal("expected IsFresh to return false when no metadata")
	}
}

func TestLoad_MissingCache(t *testing.T) {
	c := newTestCache(t)
	data, err := c.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != nil {
		t.Fatal("expected nil data for missing cache")
	}
}

func TestRefresh_200_AtomicWrite(t *testing.T) {
	payload := []byte(`{"models":{"gpt-4o":{"context":128000,"maxOutputTokens":16384}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Last-Modified", "Mon, 01 Jan 2024 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		w.Write(payload) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestCache(t)
	// Override URL by monkey-patching the constant is not possible, so use a custom client
	// and redirect via transport.
	c.HTTPClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
	}

	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}

	data, err := c.Load()
	if err != nil {
		t.Fatalf("load after refresh: %v", err)
	}
	if string(data) != string(payload) {
		t.Fatalf("expected %s, got %s", payload, data)
	}

	meta, err := c.LoadMetadata()
	if err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	if meta.ETag != `"abc123"` {
		t.Errorf("expected ETag abc123, got %q", meta.ETag)
	}
	if meta.ExpiresAt.IsZero() {
		t.Error("expected non-zero ExpiresAt")
	}
	if meta.URL != modelsDevURL {
		t.Errorf("expected URL %s, got %s", modelsDevURL, meta.URL)
	}
}

func TestRefresh_304_MetadataUpdated_DataUnchanged(t *testing.T) {
	originalData := []byte(`{"models":{"original":{"context":8192,"maxOutputTokens":1024}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"etag-v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestCache(t)
	c.HTTPClient = &http.Client{Transport: &redirectTransport{target: srv.URL}}

	// Seed existing cache.
	writeTestData(t, c, originalData)
	oldExpiry := time.Now().Add(-time.Hour)
	writeTestMeta(t, c, CacheMetadata{
		ETag:      `"etag-v1"`,
		ExpiresAt: oldExpiry,
		URL:       modelsDevURL,
	})

	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}

	// Data unchanged.
	data, err := c.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if string(data) != string(originalData) {
		t.Errorf("expected original data, got %s", data)
	}

	// ExpiresAt bumped.
	meta, err := c.LoadMetadata()
	if err != nil {
		t.Fatalf("load meta: %v", err)
	}
	if !meta.ExpiresAt.After(oldExpiry) {
		t.Error("expected ExpiresAt to be bumped after 304")
	}
}

func TestRefresh_ExpiredCacheFetchesFreshData(t *testing.T) {
	originalData := []byte(`{"models":{"old":{"context":8192,"maxOutputTokens":1024}}}`)
	freshData := []byte(`{"models":{"fresh":{"context":128000,"maxOutputTokens":16384}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(freshData)
	}))
	defer srv.Close()

	c := newTestCache(t)
	c.HTTPClient = &http.Client{Transport: &redirectTransport{target: srv.URL}}
	writeTestData(t, c, originalData)
	writeTestMeta(t, c, CacheMetadata{
		DownloadedAt: time.Now().Add(-8 * 24 * time.Hour),
		ExpiresAt:    time.Now().Add(-time.Hour),
		URL:          modelsDevURL,
	})

	if c.IsFresh() {
		t.Fatal("seeded cache unexpectedly fresh")
	}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}

	data, err := c.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if string(data) != string(freshData) {
		t.Fatalf("expected refreshed data, got %s", data)
	}
}

func TestRefresh_NetworkFailure_StaleDataStillLoadable(t *testing.T) {
	staleData := []byte(`{"models":{"stale-model":{"context":4096,"maxOutputTokens":512}}}`)

	c := newTestCache(t)
	writeTestData(t, c, staleData)
	// Use a client that always fails.
	c.HTTPClient = &http.Client{Transport: &alwaysFailTransport{}}

	// Refresh should return nil (non-fatal).
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("expected nil error on network failure, got: %v", err)
	}

	// Stale data still loadable.
	data, err := c.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if string(data) != string(staleData) {
		t.Errorf("expected stale data, got %s", data)
	}
}

func TestRefresh_NetworkFailureWithExpiredCacheKeepsStaleData(t *testing.T) {
	staleData := []byte(`{"models":{"stale-model":{"context":4096,"maxOutputTokens":512}}}`)

	c := newTestCache(t)
	writeTestData(t, c, staleData)
	writeTestMeta(t, c, CacheMetadata{
		DownloadedAt: time.Now().Add(-8 * 24 * time.Hour),
		ExpiresAt:    time.Now().Add(-time.Hour),
		URL:          modelsDevURL,
	})
	c.HTTPClient = &http.Client{Transport: &alwaysFailTransport{}}

	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("expected nil error on network failure, got: %v", err)
	}

	data, err := c.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if string(data) != string(staleData) {
		t.Fatalf("expected stale data, got %s", data)
	}
}

func TestRefresh_OfflineWithoutCacheIsNonFatal(t *testing.T) {
	c := newTestCache(t)
	c.HTTPClient = &http.Client{Transport: &alwaysFailTransport{}}

	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v, want nil in offline mode", err)
	}

	data, err := c.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if data != nil {
		t.Fatalf("Load() = %s, want nil without cache", data)
	}
}

func TestRefresh_InvalidJSON_DoesNotCorruptCache(t *testing.T) {
	originalData := []byte(`{"models":{"good":{"context":8192}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not valid json`)) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestCache(t)
	c.HTTPClient = &http.Client{Transport: &redirectTransport{target: srv.URL}}
	writeTestData(t, c, originalData)

	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Original data still intact.
	data, err := c.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if string(data) != string(originalData) {
		t.Errorf("expected original data intact, got %s", data)
	}
}

func TestDefaultCacheDir(t *testing.T) {
	t.Run("uses xdg cache home", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "xdg-cache"))
		if got := DefaultCacheDir(); got != filepath.Join(os.Getenv("XDG_CACHE_HOME"), "steiner", "model-metadata") {
			t.Fatalf("DefaultCacheDir() = %q", got)
		}
	})

	t.Run("falls back to home cache", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", "")
		home := filepath.Join(t.TempDir(), "home")
		t.Setenv("HOME", home)
		if got := DefaultCacheDir(); got != filepath.Join(home, ".cache", "steiner", "model-metadata") {
			t.Fatalf("DefaultCacheDir() = %q", got)
		}
	})
}

func TestClear(t *testing.T) {
	c := newTestCache(t)
	writeTestData(t, c, []byte(`{}`))
	writeTestMeta(t, c, CacheMetadata{URL: modelsDevURL})

	if err := c.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if _, err := os.Stat(c.CachePath()); !os.IsNotExist(err) {
		t.Error("expected cache file to be removed")
	}
	if _, err := os.Stat(c.MetaPath()); !os.IsNotExist(err) {
		t.Error("expected meta file to be removed")
	}
}

func TestClearIdempotent(t *testing.T) {
	c := newTestCache(t)
	if err := c.Clear(); err != nil {
		t.Fatalf("clear on empty dir: %v", err)
	}
}

func TestCachePaths(t *testing.T) {
	c := &Cache{Dir: "/some/dir"}
	if want := filepath.Join("/some/dir", cacheFilename); c.CachePath() != want {
		t.Errorf("CachePath: got %s, want %s", c.CachePath(), want)
	}
	if want := filepath.Join("/some/dir", metaFilename); c.MetaPath() != want {
		t.Errorf("MetaPath: got %s, want %s", c.MetaPath(), want)
	}
}

// redirectTransport rewrites all requests to point at target server.
type redirectTransport struct {
	target string
	base   http.RoundTripper
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = "http"
	cloned.URL.Host = t.target[len("http://"):]
	rt := t.base
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(cloned)
}

// alwaysFailTransport always returns a connection error.
type alwaysFailTransport struct{}

func (t *alwaysFailTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, &networkError{msg: "simulated network failure"}
}

type networkError struct{ msg string }

func (e *networkError) Error() string   { return e.msg }
func (e *networkError) Timeout() bool   { return false }
func (e *networkError) Temporary() bool { return true }
