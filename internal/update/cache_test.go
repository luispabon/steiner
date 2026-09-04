package update

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

func TestCheckCached_CacheHitSkipsNetworkCall(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(release{TagName: "v9.9.9"})
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	entry := checkCache{
		CurrentVersion: "v1.0.0",
		Channel:        "stable",
		LatestVersion:  "v1.2.0",
		NeedsUpdate:    true,
		CheckedAt:      time.Now(),
	}
	if err := saveCheckCache(cachePath, entry); err != nil {
		t.Fatalf("saveCheckCache: %v", err)
	}

	latest, needs, err := CheckCached(context.Background(), cachePath, time.Hour, "v1.0.0", "owner", "repo", "", "stable")
	if err != nil {
		t.Fatalf("CheckCached: %v", err)
	}
	if latest != "v1.2.0" || !needs {
		t.Errorf("CheckCached() = (%q, %v), want (%q, true)", latest, needs, "v1.2.0")
	}
	if calls != 0 {
		t.Errorf("expected no HTTP calls on cache hit, got %d", calls)
	}
}

func TestCheckCached_CacheMiss(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, cachePath string)
	}{
		{
			name: "missing file",
		},
		{
			name: "stale checked_at",
			setup: func(t *testing.T, cachePath string) {
				entry := checkCache{
					CurrentVersion: "v1.0.0",
					Channel:        "stable",
					LatestVersion:  "v1.1.0",
					NeedsUpdate:    true,
					CheckedAt:      time.Now().Add(-2 * time.Hour),
				}
				if err := saveCheckCache(cachePath, entry); err != nil {
					t.Fatalf("saveCheckCache: %v", err)
				}
			},
		},
		{
			name: "version mismatch",
			setup: func(t *testing.T, cachePath string) {
				entry := checkCache{
					CurrentVersion: "v0.9.0",
					Channel:        "stable",
					LatestVersion:  "v1.1.0",
					NeedsUpdate:    true,
					CheckedAt:      time.Now(),
				}
				if err := saveCheckCache(cachePath, entry); err != nil {
					t.Fatalf("saveCheckCache: %v", err)
				}
			},
		},
		{
			name: "channel mismatch",
			setup: func(t *testing.T, cachePath string) {
				entry := checkCache{
					CurrentVersion: "v1.0.0",
					Channel:        "dev",
					LatestVersion:  "v1.1.0",
					NeedsUpdate:    true,
					CheckedAt:      time.Now(),
				}
				if err := saveCheckCache(cachePath, entry); err != nil {
					t.Fatalf("saveCheckCache: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(release{TagName: "v1.2.0"})
			}))
			defer server.Close()

			defer saveHTTPClient()()
			httpClient = newTestClient(server.URL)

			cachePath := filepath.Join(t.TempDir(), "update-check.json")
			if tc.setup != nil {
				tc.setup(t, cachePath)
			}

			latest, needs, err := CheckCached(context.Background(), cachePath, time.Hour, "v1.0.0", "owner", "repo", "", "stable")
			if err != nil {
				t.Fatalf("CheckCached: %v", err)
			}
			if latest != "v1.2.0" || !needs {
				t.Errorf("CheckCached() = (%q, %v), want (%q, true)", latest, needs, "v1.2.0")
			}
			if calls != 1 {
				t.Errorf("expected exactly 1 HTTP call, got %d", calls)
			}

			data, err := os.ReadFile(cachePath)
			if err != nil {
				t.Fatalf("cache file not written: %v", err)
			}
			var entry checkCache
			if err := json.Unmarshal(data, &entry); err != nil {
				t.Fatalf("unmarshal cache: %v", err)
			}
			if entry.LatestVersion != "v1.2.0" || !entry.NeedsUpdate {
				t.Errorf("cache entry = %+v, want fresh result", entry)
			}
		})
	}
}

func TestCheckCached_FailedCheckStampsCache(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	cachePath := filepath.Join(t.TempDir(), "update-check.json")

	_, needs, err := CheckCached(context.Background(), cachePath, time.Hour, "v1.0.0", "owner", "repo", "", "stable")
	if err == nil {
		t.Fatal("expected error from failed check")
	}
	if needs {
		t.Errorf("needsUpdate = true on failure, want false")
	}
	if calls != 1 {
		t.Fatalf("expected 1 HTTP call after first CheckCached, got %d", calls)
	}

	_, needs2, err2 := CheckCached(context.Background(), cachePath, time.Hour, "v1.0.0", "owner", "repo", "", "stable")
	if err2 != nil {
		t.Fatalf("expected cached failure to be served as a cache hit with no error, got %v", err2)
	}
	if needs2 {
		t.Errorf("needsUpdate = true on cached failure, want false")
	}
	if calls != 1 {
		t.Errorf("expected still only 1 HTTP call after second CheckCached within interval, got %d", calls)
	}
}

func TestDefaultCachePath(t *testing.T) {
	t.Run("uses xdg cache home", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "xdg-cache"))
		want := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "steiner", "update-check.json")
		if got := DefaultCachePath(); got != want {
			t.Fatalf("DefaultCachePath() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to home cache", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", "")
		home := filepath.Join(t.TempDir(), "home")
		t.Setenv("HOME", home)
		want := filepath.Join(home, ".cache", "steiner", "update-check.json")
		if got := DefaultCachePath(); got != want {
			t.Fatalf("DefaultCachePath() = %q, want %q", got, want)
		}
	})
}
