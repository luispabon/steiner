package tui

import (
	"fmt"
	"os"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// testStyles builds a *theme.Styles for tests, matching the pointer-shared
// representation used by Model and its sub-components in production code.
func testStyles(accentHex string) *theme.Styles {
	s := theme.BuildStyles(accentHex)
	return &s
}

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "steiner-tui-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir for tui tests: %v\n", err)
		os.Exit(1)
	}
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmp); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set HOME for tui tests: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if err := os.Setenv("HOME", oldHome); err != nil {
		fmt.Fprintf(os.Stderr, "failed to restore HOME for tui tests: %v\n", err)
		os.Exit(1)
	}
	if err := os.RemoveAll(tmp); err != nil {
		fmt.Fprintf(os.Stderr, "failed to remove temp dir for tui tests: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}
