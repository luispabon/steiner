package tui

import (
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// brandLinesTestWidth mirrors the inner width View actually passes to
// brandLines (sidebarWidth minus horizontal padding on both sides), so tests
// exercise real production wrapping/truncation/padding behaviour.
const brandLinesTestWidth = sidebarWidth - sidebarPadH*2

func TestSidebarStateBrandLines(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)

	tests := []struct {
		name            string
		updateAvailable bool
		latestVersion   string
		wantLines       int
		wantContains    []string
	}{
		{
			name:            "no update available",
			updateAvailable: false,
			latestVersion:   "",
			wantLines:       3,
		},
		{
			name:            "update available with version",
			updateAvailable: true,
			latestVersion:   "v1.2.3",
			wantLines:       4,
			wantContains:    []string{"v1.2.3", "upgrade"},
		},
		{
			name:            "update available with long dev channel tag",
			updateAvailable: true,
			latestVersion:   "dev-8-g8bd663f",
			wantLines:       4,
			wantContains:    []string{"dev-8-g8bd663f", "upgrade"},
		},
		{
			name:            "update available but empty version guards output",
			updateAvailable: true,
			latestVersion:   "",
			wantLines:       3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := sidebarState{
				styles:          styles,
				version:         "1.0.0",
				updateAvailable: tc.updateAvailable,
				latestVersion:   tc.latestVersion,
			}
			lines := s.brandLines(brandLinesTestWidth)
			if len(lines) != tc.wantLines {
				t.Fatalf("brandLines() returned %d lines, want %d: %#v", len(lines), tc.wantLines, lines)
			}
			if len(tc.wantContains) > 0 {
				last := stripANSI(lines[len(lines)-1])
				for _, want := range tc.wantContains {
					if !strings.Contains(last, want) {
						t.Errorf("last brand line %q does not contain %q", last, want)
					}
				}
				if got := lipgloss.Width(lines[len(lines)-1]); got != brandLinesTestWidth {
					t.Errorf("last brand line width = %d, want %d (padded to full sidebar width)", got, brandLinesTestWidth)
				}
			}
		})
	}
}

func TestSidebarStateBrandLinesWrapsLongVersion(t *testing.T) {
	t.Parallel()
	const version = "0.27.0-8-ga17d550e-dirty-with-a-very-long-local-suffix"
	s := sidebarState{styles: testStyles(theme.AccentAmber), version: version}
	lines := s.brandLines(brandLinesTestWidth)
	if len(lines) <= 3 {
		t.Fatalf("brandLines() returned %d lines, want the version wrapped past the logo", len(lines))
	}
	var text strings.Builder
	for i, line := range lines {
		if got := lipgloss.Width(line); got > brandLinesTestWidth {
			t.Errorf("line %d width = %d, want <= %d", i, got, brandLinesTestWidth)
		}
		if i >= 2 {
			text.WriteString(strings.TrimSpace(ansi.Strip(line)))
		}
	}
	if got := text.String(); !strings.HasSuffix(got, version) {
		t.Errorf("wrapped version text = %q, want it to end with %q", got, version)
	}
}

func TestWrapRunes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text        string
		first, rest int
		want        []string
	}{
		{"", 3, 5, []string{""}},
		{"abc", 3, 5, []string{"abc"}},
		{"abcdefghij", 3, 5, []string{"abc", "defgh", "ij"}},
		{"ab", 1, 1, []string{"a", "b"}},
	}
	for _, tc := range tests {
		if got := wrapRunes(tc.text, tc.first, tc.rest); !slices.Equal(got, tc.want) {
			t.Errorf("wrapRunes(%q, %d, %d) = %q, want %q", tc.text, tc.first, tc.rest, got, tc.want)
		}
	}
}
