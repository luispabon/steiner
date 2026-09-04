package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestSidebarStateBrandLines(t *testing.T) {
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
			wantContains:    []string{"v1.2.3", "steiner upgrade"},
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
			lines := s.brandLines(36)
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
				if got := lipgloss.Width(lines[len(lines)-1]); got != 36 {
					t.Errorf("last brand line width = %d, want %d (padded to full sidebar width)", got, 36)
				}
			}
		})
	}
}
