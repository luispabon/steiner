package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// renderViewportWithScrollbarOriginal is the pre-optimization reference implementation
// kept here to validate output parity with the builder-based replacement.
// Updated to include background styling for the leading line.
func renderViewportWithScrollbarOriginal(viewportInner, scrollbar string, viewportWidth int) string {
	vpLines := strings.Split(viewportInner, "\n")
	scLines := strings.Split(scrollbar, "\n")
	merged := make([]string, 0, len(vpLines)+1)

	// Add background-filled leading line to match the new implementation.
	leadBg := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).
		Render(strings.Repeat(" ", viewportWidth))
	leadSc := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).Render(" ")
	merged = append(merged, leadBg+leadSc)

	for i := 0; i < len(vpLines) && i < len(scLines); i++ {
		merged = append(merged, vpLines[i]+scLines[i])
	}
	return strings.Join(merged, "\n")
}

func TestRenderViewportWithScrollbar(t *testing.T) {
	t.Parallel()
	m := Model{
		viewport: viewport.New(),
		styles:   theme.BuildStyles(theme.AccentAmber),
	}

	cases := []struct {
		name      string
		viewport  string
		scrollbar string
	}{
		{
			name:      "empty both",
			viewport:  "",
			scrollbar: "",
		},
		{
			name:      "single line equal length",
			viewport:  "hello",
			scrollbar: "|",
		},
		{
			name:      "multiline equal",
			viewport:  "line1\nline2\nline3",
			scrollbar: "a\nb\nc",
		},
		{
			name:      "scrollbar shorter than viewport",
			viewport:  "line1\nline2\nline3",
			scrollbar: "a\nb",
		},
		{
			name:      "scrollbar longer than viewport",
			viewport:  "line1\nline2",
			scrollbar: "a\nb\nc",
		},
		{
			name:      "viewport empty scrollbar non-empty",
			viewport:  "",
			scrollbar: "bar",
		},
		{
			name:      "ansi escape sequences",
			viewport:  "\x1b[31mred\x1b[0m\nplain",
			scrollbar: "\x1b[32m|\x1b[0m\n ",
		},
		{
			name:      "trailing newline in viewport",
			viewport:  "a\nb\n",
			scrollbar: "x\ny\nz",
		},
		{
			name:      "trailing newline in both",
			viewport:  "a\nb\n",
			scrollbar: "x\ny\n",
		},
		{
			name:      "single line viewport no scrollbar",
			viewport:  "onlyme",
			scrollbar: "",
		},
		{
			name:      "many lines",
			viewport:  strings.Repeat("content line\n", 50),
			scrollbar: strings.Repeat("│\n", 50),
		},
	}

	// Use a fixed viewport width for testing.
	viewportWidth := 20
	m.viewport.SetWidth(viewportWidth)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := renderViewportWithScrollbarOriginal(tc.viewport, tc.scrollbar, viewportWidth)
			got := m.renderViewportWithScrollbar(tc.viewport, tc.scrollbar)
			if got != want {
				t.Errorf("output mismatch\nwant: %q\n got: %q", want, got)
			}
		})
	}
}
