package tui

import (
	"fmt"
	"os"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// testStyles builds a *theme.Styles for tests, matching the pointer-shared
// representation used by Model and its sub-components in production code.
func testStyles(accentHex string) *theme.Styles {
	s := theme.BuildStyles(accentHex)
	return &s
}

func TestMain(m *testing.M) {
	lipgloss.Writer.Profile = colorprofile.ASCII

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
	// Models built without an explicit WorkingDir fall back to getWorkingDir
	// and run git against it. The package dir sits inside the steiner checkout,
	// so every such model would shell out to git several times and observe the
	// developer's branch and dirty state. A non-repo dir keeps them hermetic;
	// tests that exercise git build their own repo and pass it as WorkingDir.
	getWorkingDir = func() (string, error) { return tmp, nil }
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
