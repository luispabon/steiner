package tui

import (
	"fmt"
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

func TestPlaceBottomAnchoredAtPosition(t *testing.T) {
	// Build a 20-row base with distinguishable lines.
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("row-%02d", i)
	}
	base := strings.Join(lines, "\n")

	// 4-line overlay.
	overlay := strings.Join([]string{
		"===OVERLAY-TOP===",
		"OVERLAY-CONTENT",
		"OVERLAY-CONTENT",
		"===OVERLAY-BTM===",
	}, "\n")

	height := 20
	shell := OverlayShell{width: 80, height: height}
	inputHeight := 6 // bottom chrome rows
	result := shell.PlaceBottomAnchoredAt(base, overlay, inputHeight, 0)
	resultLines := strings.Split(result, "\n")

	if len(resultLines) != len(lines) {
		t.Fatalf("got %d lines, want %d", len(resultLines), len(lines))
	}

	// Expected startY: height - len(overlay) - inputHeight - 1 = 20 - 4 - 6 - 1 = 9
	wantStart := height - 4 - inputHeight - 1 // = 9

	// Lines before start must be unchanged.
	for i := 0; i < wantStart; i++ {
		if resultLines[i] != lines[i] {
			t.Errorf("line %d before overlay: expected %q, got %q", i, lines[i], resultLines[i])
		}
	}

	// Overlay must appear at the expected starting row.
	if !strings.Contains(resultLines[wantStart], "===OVERLAY-TOP===") {
		t.Errorf("line %d should start overlay, got %q", wantStart, resultLines[wantStart])
	}
	if !strings.Contains(resultLines[wantStart+1], "OVERLAY-CONTENT") {
		t.Errorf("line %d should contain overlay content, got %q", wantStart+1, resultLines[wantStart+1])
	}

	// Lines after overlay must be unchanged.
	for i := wantStart + 4; i < len(lines); i++ {
		if resultLines[i] != lines[i] {
			t.Errorf("line %d after overlay: expected %q, got %q", i, lines[i], resultLines[i])
		}
	}
}

func TestOverlayShellOverlayWidth(t *testing.T) {
	tests := []struct {
		name      string
		preferred int
		termWidth int
		wantWidth int
		wantInner int
	}{
		{name: "dynamic (no preferred)", preferred: 0, termWidth: 120, wantWidth: 116, wantInner: 112},
		{name: "preferred width", preferred: 60, termWidth: 120, wantWidth: 60, wantInner: 56},
		{name: "clamped-small", preferred: 10, termWidth: 120, wantWidth: 40, wantInner: 36},
		{name: "clamped-large", preferred: 200, termWidth: 120, wantWidth: 116, wantInner: 112},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shell := OverlayShell{width: tt.termWidth, preferredWidth: tt.preferred}
			if got := shell.overlayWidth(); got != tt.wantWidth {
				t.Errorf("overlayWidth() = %d, want %d", got, tt.wantWidth)
			}
			if got := shell.InnerWidth(); got != tt.wantInner {
				t.Errorf("InnerWidth() = %d, want %d", got, tt.wantInner)
			}
		})
	}
}
