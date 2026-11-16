package provider

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// A cancelled delivery drains the interrupted CLI turn; tests that exercise
	// the fallback path would each wait out the full bound, so shorten it here.
	// A test that needs its own bound sets claudeSubDrainTimeout.
	claudeSubDrainTimeout = 50 * time.Millisecond
	tmp, err := os.MkdirTemp("", "steiner-provider-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	old, had := os.LookupEnv("XDG_CACHE_HOME")
	if err := os.Setenv("XDG_CACHE_HOME", tmp); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set XDG_CACHE_HOME: %v\n", err)
		os.Exit(1)
	}
	if err := seedModelsDevCacheDir(tmp, "{}"); err != nil {
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
