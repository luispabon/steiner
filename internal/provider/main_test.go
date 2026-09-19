package provider

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
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
