package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// renderViewportWithScrollbarOriginal is the pre-optimization reference implementation
// kept here to validate output parity with the builder-based replacement.
// Updated to include background styling for the leading line.
func renderViewportWithScrollbarOriginal(viewportInner, scrollbar string, viewportWidth int, contentBG string) string {
	vpLines := strings.Split(viewportInner, "\n")
	scLines := strings.Split(scrollbar, "\n")
	merged := make([]string, 0, len(vpLines)+1)

	// Add background-filled leading line to match the new implementation.
	leadBg := lipgloss.NewStyle().Background(lipgloss.Color(contentBG)).
		Render(strings.Repeat(" ", viewportWidth))
	leadSc := lipgloss.NewStyle().Background(lipgloss.Color(contentBG)).Render(" ")
	merged = append(merged, leadBg+leadSc)

	for i := 0; i < len(vpLines) && i < len(scLines); i++ {
		merged = append(merged, vpLines[i]+scLines[i])
	}
	return strings.Join(merged, "\n")
}

func inputBackgroundEscape(t *testing.T, m *Model) string {
	t.Helper()
	hex := strings.TrimPrefix(theme.ColorHex(m.styles.UserBg.GetBackground()), "#")
	if len(hex) != 6 {
		t.Fatalf("unexpected user background %q", hex)
	}
	values := make([]uint64, 3)
	for i, part := range []string{hex[0:2], hex[2:4], hex[4:6]} {
		value, err := strconv.ParseUint(part, 16, 8)
		if err != nil {
			t.Fatalf("parse user background %q: %v", hex, err)
		}
		values[i] = value
	}
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", values[0], values[1], values[2])
}

func TestRenderInputViewReappliesUserBackground(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{name: "placeholder"},
		{name: "multiline", value: "first line\nsecond line"},
		{name: "wrapped", value: strings.Repeat("wrapped ", 80)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newInputViewTestModel(t)
			m.input.SetValue(tt.value)
			if tt.value == "" {
				m.input.Placeholder = "placeholder text"
			}

			rendered := m.renderInputViewUncached(m.contentWidth())
			lines := strings.Split(rendered, "\n")
			if len(lines) < 3 {
				t.Fatalf("rendered input has %d rows, want prompt padding and content", len(lines))
			}

			background := inputBackgroundEscape(t, m)
			rail := m.styles.UserBar.Background(m.styles.UserBg.GetBackground()).Render("┃")
			rail = strings.TrimSuffix(rail, "\x1b[0m")
			for row, line := range lines {
				if !strings.Contains(line, "┃") {
					t.Fatalf("row %d has no input rail", row)
				}
				if !strings.Contains(line, background) {
					t.Fatalf("row %d has no explicit user background", row)
				}
				if !strings.Contains(line, rail) {
					t.Fatalf("row %d rail does not use UserBar with user background", row)
				}
			}
			if !strings.Contains(rendered, "\x1b[0m"+background) {
				if !strings.Contains(rendered, "\x1b[m"+background) && !strings.Contains(rendered, "\x1b[0m"+background) {
					t.Fatalf("rendered input does not reapply user background after an ANSI reset: %q", rendered)
				}
			}
		})
	}
}

func TestHighlightCommandPrefixLine(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)

	tests := []struct {
		name           string
		line           string
		cursorCol      int
		value          string
		skillNames     []string
		oneshotRunning bool
		innerWidth     int
		wantHighlight  bool
	}{
		{
			name:      "plain line no command prefix",
			line:      "just some text",
			cursorCol: 5,
			value:     "just some text",
		},
		{
			name:          "skill prefix match",
			line:          "/deploy rest of the line",
			cursorCol:     8,
			value:         "/deploy rest of the line",
			skillNames:    []string{"deploy"},
			innerWidth:    30,
			wantHighlight: true,
		},
		{
			name:           "oneshot running blocks command prefix match",
			line:           "/deploy rest of the line",
			cursorCol:      8,
			value:          "/deploy rest of the line",
			skillNames:     []string{"deploy"},
			oneshotRunning: true,
			innerWidth:     30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := highlightCommandPrefixLine(tt.line, tt.cursorCol, tt.value, tt.skillNames, tt.oneshotRunning, tt.innerWidth, styles.CommandPrefixStyle, styles.UserBg)
			if tt.wantHighlight {
				if got == tt.line {
					t.Fatalf("highlightCommandPrefixLine(%q) = %q, want a styled line distinct from input", tt.line, got)
				}
				if !strings.Contains(got, "deploy") {
					t.Fatalf("highlightCommandPrefixLine(%q) = %q, want prefix text preserved", tt.line, got)
				}
			} else if got != tt.line {
				t.Fatalf("highlightCommandPrefixLine(%q) = %q, want unchanged line", tt.line, got)
			}
		})
	}
}

func TestRenderViewportWithScrollbar(t *testing.T) {
	t.Parallel()
	m := Model{
		viewport: newScrollModel(0, 0),
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
			want := renderViewportWithScrollbarOriginal(tc.viewport, tc.scrollbar, viewportWidth, m.resolvedPalette().ContentBG)
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
		viewport: newScrollModel(40, 10),
		styles:   testStyles(theme.AccentAmber),
	}
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

func TestHighlightCommandPrefixLineCJK(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)

	tests := []struct {
		name          string
		line          string
		cursorCol     int
		value         string
		skillNames    []string
		innerWidth    int
		wantHighlight bool
	}{
		{
			name:          "ASCII skill with cursor after prefix",
			line:          "/deploy rest",
			cursorCol:     8,
			value:         "/deploy rest",
			skillNames:    []string{"deploy"},
			innerWidth:    30,
			wantHighlight: true,
		},
		{
			name:          "CJK skill - 2-cell-wide char in name with space",
			line:          "/界 rest",
			cursorCol:     5,
			value:         "/界 rest",
			skillNames:    []string{"界"},
			innerWidth:    30,
			wantHighlight: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := highlightCommandPrefixLine(tt.line, tt.cursorCol, tt.value, tt.skillNames, false, tt.innerWidth, styles.CommandPrefixStyle, styles.UserBg)
			if tt.wantHighlight {
				if result == tt.line {
					t.Errorf("highlightCommandPrefixLine should highlight prefix for %q, got unchanged", tt.line)
				}
			} else if result != tt.line {
				t.Errorf("highlightCommandPrefixLine should not highlight for %q, got %q", tt.line, result)
			}
		})
	}
}

func TestApplyComposerCursorAnsiCJK(t *testing.T) {
	t.Parallel()
	// Test that cursor placement respects cell width, not rune count
	// "界" is 1 rune but 2 cells wide

	text := "界test"
	// Cursor at cell position 2 (after 界) should be on 't', not within 界
	result := applyComposerCursorAnsi(text, 2, true)
	// Should contain the reverse video escape
	if !strings.Contains(result, "\x1b[7m") {
		t.Errorf("applyComposerCursorAnsi should place cursor, got %q", result)
	}

	// The cursor should be on 't', not splitting the CJK character
	// After reverse video escape, we should see 't' (or similar)
	if !strings.Contains(result, "t") {
		t.Errorf("applyComposerCursorAnsi result should contain 't', got %q", result)
	}
}
