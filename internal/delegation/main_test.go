package delegation

import (
	"fmt"
	"os"
	"testing"

	"github.com/luispabon/steiner/internal/metadata"
)

// seedModelsDevCacheDir seeds an empty-but-fresh models.dev cache in the
// metadata cache dir derived from the current XDG_CACHE_HOME, so metadata
// loading never hits the network. Callers must set XDG_CACHE_HOME first.
func seedModelsDevCacheDir() error {
	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	if err := os.WriteFile(cache.CachePath(), []byte("{}"), 0o644); err != nil {
		return fmt.Errorf("write cache: %w", err)
	}
	meta := `{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json"}`
	if err := os.WriteFile(cache.MetaPath(), []byte(meta), 0o644); err != nil {
		return fmt.Errorf("write cache meta: %w", err)
	}
	return nil
}

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "steiner-delegation-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	old, had := os.LookupEnv("XDG_CACHE_HOME")
	if err := os.Setenv("XDG_CACHE_HOME", tmp); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set XDG_CACHE_HOME: %v\n", err)
		os.Exit(1)
	}
	if err := seedModelsDevCacheDir(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to seed models.dev cache: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if had {
		err = os.Setenv("XDG_CACHE_HOME", old)
	} else {
		err = os.Unsetenv("XDG_CACHE_HOME")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to restore XDG_CACHE_HOME: %v\n", err)
		os.Exit(1)
	}
	if err := os.RemoveAll(tmp); err != nil {
		fmt.Fprintf(os.Stderr, "failed to remove temp dir: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}
