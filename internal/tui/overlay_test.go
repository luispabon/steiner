package tui

import (
	"strings"
	"testing"
)

func TestComposeCenteredOverlayKeepsBaseContentOutsideOverlay(t *testing.T) {
	base := strings.Join([]string{
		"abcdefghijkl",
		"mnopqrstuvwx",
		"yz0123456789",
		"ABCDEFGHIJKL",
		"MNOPQRSTUVWX",
		"YZ!@#$%^&*()",
	}, "\n")
	overlay := strings.Join([]string{
		"++++",
		"|OK|",
		"++++",
	}, "\n")

	got := composeCenteredOverlay(base, overlay, 12, 6)
	lines := strings.Split(got, "\n")
	if len(lines) != 6 {
		t.Fatalf("line count = %d, want 6", len(lines))
	}

	if lines[0] != "abcdefghijkl" {
		t.Fatalf("line 0 = %q, want base content preserved", lines[0])
	}
	if lines[5] != "YZ!@#$%^&*()" {
		t.Fatalf("line 5 = %q, want base content preserved", lines[5])
	}
	if lines[1] != "mnop++++uvwx" {
		t.Fatalf("line 1 = %q, want centered overlay without clearing sides", lines[1])
	}
	if lines[2] != "yz01|OK|6789" {
		t.Fatalf("line 2 = %q, want centered overlay without clearing sides", lines[2])
	}
	if lines[3] != "ABCD++++IJKL" {
		t.Fatalf("line 3 = %q, want centered overlay without clearing sides", lines[3])
	}
}
