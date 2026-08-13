package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
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
		styles:   testStyles(theme.AccentAmber),
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

// renderViewportView caches the rendered frame keyed on scrollY, contentWidth
// and hasScrollbar; the cache is cleared by syncViewport and must be bypassed
// whenever help is visible (the help overlay is composed on top of the cached
// frame, so serving the cache would show a stale frame without help). These
// tests would fail if a stale frame were served.

// TestViewportViewCacheServesStoredFrame proves the cache short-circuit is
// live: when the keys match, the stored string is returned without re-render.
func TestViewportViewCacheServesStoredFrame(t *testing.T) {
	t.Parallel()
	m := newModel(Config{Model: "cache-test"}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	contentWidth := m.contentWidth()
	m.vpViewCache = "SENTINEL-FRAME"
	m.vpViewCacheScrollY = m.viewport.YOffset()
	m.vpViewCacheWidth = contentWidth
	m.vpViewCacheHasScrollbar = m.renderScrollbar() != ""

	if got := m.renderViewportView(contentWidth); got != "SENTINEL-FRAME" {
		t.Fatalf("renderViewportView = %q, want cached sentinel frame", got)
	}
}

// TestViewportViewCacheBypassedWhenHelpVisible is the regression test for the
// help overlay: toggling helpVisible (the '?' key path) never calls
// syncViewport, so the only thing preventing a stale frame is the cache-hit
// guard. With help visible the frame must be re-rendered with the overlay.
func TestViewportViewCacheBypassedWhenHelpVisible(t *testing.T) {
	t.Parallel()
	m := newModel(Config{Model: "cache-test"}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.syncViewport()

	contentWidth := m.contentWidth()
	base := m.renderViewportView(contentWidth)
	if base == "" {
		t.Fatal("base frame empty; test setup broken")
	}

	// Simulate the '?' key handler: help toggles without syncViewport.
	m.helpVisible = true
	withHelp := m.renderViewportView(contentWidth)
	if withHelp == base {
		t.Fatal("stale viewport frame served when help visible; cache must be bypassed")
	}

	// Toggling help off again must return the (valid, pre-help) cached frame.
	m.helpVisible = false
	if got := m.renderViewportView(contentWidth); got != base {
		t.Fatal("frame changed after help dismissed; expected the cached pre-help frame")
	}
}

// TestViewportViewCacheRefreshedOnScroll proves scrolling invalidates the
// cached frame via the scrollY key: the frame rendered after scrollUp must
// show the scrolled content, not the cached top-of-content frame.
func TestViewportViewCacheRefreshedOnScroll(t *testing.T) {
	t.Parallel()
	m := &Model{
		viewport: viewport.New(),
		styles:   testStyles(theme.AccentAmber),
	}
	m.viewport.SetWidth(40)
	m.viewport.SetHeight(10)
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("content line %d", i))
	}
	m.setViewportContent(strings.Join(lines, "\n"))

	base := m.renderViewportView(40)
	m.scrollDown(3)
	if m.viewport.YOffset() == 0 {
		t.Fatal("scrollDown(3) did not move the viewport; test setup broken")
	}
	scrolled := m.renderViewportView(40)
	if scrolled == base {
		t.Fatal("stale viewport frame served after scroll; scrollY key must invalidate")
	}
}
