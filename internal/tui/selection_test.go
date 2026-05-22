package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// testRenderer forces TrueColor output so lipgloss emits ANSI codes in tests
// (the default renderer detects no terminal and strips all colour).
var testRenderer = func() *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	return r
}()

// testSelStyle is used as the highlight style in applyHighlight tests.
var testSelStyle = testRenderer.NewStyle().Background(lipgloss.Color("#3a4a5a"))

// ---------------------------------------------------------------------------
// TestTermToContent
// ---------------------------------------------------------------------------

func TestTermToContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		termX, termY    int
		yOffset         int
		sidebarVisible  bool
		sidebarPosition string
		want            selectionPoint
	}{
		{
			name:  "no sidebar, zero offset, origin",
			termX: 3, termY: 1,
			yOffset:         0,
			sidebarVisible:  false,
			sidebarPosition: "",
			want:            selectionPoint{line: 0, col: 0},
		},
		{
			name:  "no sidebar, zero offset, col 10",
			termX: 13, termY: 1,
			yOffset:         0,
			sidebarVisible:  false,
			sidebarPosition: "",
			want:            selectionPoint{line: 0, col: 10},
		},
		{
			name:  "no sidebar, scroll offset 5",
			termX: 3, termY: 3,
			yOffset:         5,
			sidebarVisible:  false,
			sidebarPosition: "",
			want:            selectionPoint{line: 7, col: 0},
		},
		{
			name:  "sidebar left, zero offset",
			termX: 3 + sidebarWidth + 1, termY: 1,
			yOffset:         0,
			sidebarVisible:  true,
			sidebarPosition: "left",
			want:            selectionPoint{line: 0, col: 0},
		},
		{
			name:  "sidebar left, col 5",
			termX: 3 + sidebarWidth + 1 + 5, termY: 2,
			yOffset:         0,
			sidebarVisible:  true,
			sidebarPosition: "left",
			want:            selectionPoint{line: 1, col: 5},
		},
		{
			name:  "sidebar right, zero offset — sidebar does not shift left pad",
			termX: 3, termY: 1,
			yOffset:         0,
			sidebarVisible:  true,
			sidebarPosition: "right",
			want:            selectionPoint{line: 0, col: 0},
		},
		{
			name:  "sidebar right, col 7",
			termX: 10, termY: 4,
			yOffset:         2,
			sidebarVisible:  true,
			sidebarPosition: "right",
			want:            selectionPoint{line: 5, col: 7},
		},
		{
			name:  "termX smaller than leftPad clamps col to 0",
			termX: 0, termY: 1,
			yOffset:         0,
			sidebarVisible:  false,
			sidebarPosition: "",
			want:            selectionPoint{line: 0, col: 0},
		},
		{
			name:  "termY 0 with zero offset gives line -1",
			termX: 3, termY: 0,
			yOffset:         0,
			sidebarVisible:  false,
			sidebarPosition: "",
			want:            selectionPoint{line: -1, col: 0},
		},
		{
			name:  "large scroll offset",
			termX: 5, termY: 2,
			yOffset:         100,
			sidebarVisible:  false,
			sidebarPosition: "",
			want:            selectionPoint{line: 101, col: 2},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := termToContent(tc.termX, tc.termY, tc.yOffset, tc.sidebarVisible, tc.sidebarPosition)
			if got != tc.want {
				t.Errorf("termToContent(%d, %d, %d, %v, %q) = %+v; want %+v",
					tc.termX, tc.termY, tc.yOffset, tc.sidebarVisible, tc.sidebarPosition,
					got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestExtractText
// ---------------------------------------------------------------------------

func TestExtractText(t *testing.T) {
	t.Parallel()
	plainLines := []string{
		"hello world",   // 0
		"foo bar baz",   // 1
		"goodbye cruel", // 2
	}
	ansiLines := []string{
		"\x1b[31mred text\x1b[0m",   // 0 — plain: "red text"
		"\x1b[32mgreen line\x1b[0m", // 1 — plain: "green line"
	}

	tests := []struct {
		name  string
		lines []string
		state selectionState
		want  string
	}{
		{
			name:  "empty selection returns empty string",
			lines: plainLines,
			state: selectionState{},
			want:  "",
		},
		{
			name:  "active flag but same point still returns empty (no content span)",
			lines: plainLines,
			state: selectionState{start: selectionPoint{0, 2}, end: selectionPoint{0, 2}, active: true},
			want:  "",
		},
		{
			name:  "single line partial",
			lines: plainLines,
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 5}},
			want:  "hello",
		},
		{
			name:  "single line full",
			lines: plainLines,
			state: selectionState{start: selectionPoint{1, 0}, end: selectionPoint{1, 11}},
			want:  "foo bar baz",
		},
		{
			name:  "multi-line",
			lines: plainLines,
			state: selectionState{start: selectionPoint{0, 6}, end: selectionPoint{1, 3}},
			want:  "world\nfoo",
		},
		{
			name:  "reversed start/end (end before start) is normalised",
			lines: plainLines,
			state: selectionState{start: selectionPoint{1, 3}, end: selectionPoint{0, 6}},
			want:  "world\nfoo",
		},
		{
			name:  "out-of-range lines are skipped",
			lines: plainLines,
			state: selectionState{start: selectionPoint{-1, 0}, end: selectionPoint{10, 5}},
			want:  "hello world\nfoo bar baz\ngoodbye cruel",
		},
		{
			name:  "col beyond line length clamps to end of line",
			lines: plainLines,
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 999}},
			want:  "hello world",
		},
		{
			name:  "startCol beyond line length returns empty segment",
			lines: plainLines,
			state: selectionState{start: selectionPoint{0, 999}, end: selectionPoint{0, 999}},
			want:  "",
		},
		{
			name:  "ANSI sequences are stripped from extracted text",
			lines: ansiLines,
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 8}},
			want:  "red text",
		},
		{
			name:  "ANSI multi-line extraction",
			lines: ansiLines,
			state: selectionState{start: selectionPoint{0, 4}, end: selectionPoint{1, 5}},
			want:  "text\ngreen",
		},
		{
			name:  "empty lines slice",
			lines: []string{},
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 5}},
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractText(tc.lines, tc.state)
			if got != tc.want {
				t.Errorf("extractText() = %q; want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestApplyHighlight
// ---------------------------------------------------------------------------

// stripANSI is a simple helper for tests: checks if a string has no ESC byte.
func hasNoANSI(s string) bool {
	return !strings.ContainsRune(s, '\x1b')
}

func TestApplyHighlight(t *testing.T) {
	t.Parallel()

	// Plain viewport output — three lines, no ANSI.
	viewportOutput := "hello world\nfoo bar baz\ngoodbye cruel"

	tests := []struct {
		name          string
		output        string
		yOffset       int
		state         selectionState
		viewportWidth int
		// checkFn is called with the result lines for custom assertions.
		checkFn func(t *testing.T, lines []string)
	}{
		{
			name:          "no selection returns output unchanged",
			output:        viewportOutput,
			yOffset:       0,
			state:         selectionState{},
			viewportWidth: 80,
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				got := strings.Join(lines, "\n")
				if got != viewportOutput {
					t.Errorf("expected unchanged output, got %q", got)
				}
			},
		},
		{
			name:          "full first line selected — ANSI present in line 0, others unchanged",
			output:        viewportOutput,
			yOffset:       0,
			state:         selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 11}},
			viewportWidth: 80,
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				// Line 0 should contain ANSI (from selStyle render).
				if hasNoANSI(lines[0]) {
					t.Errorf("line 0 expected ANSI highlight, got plain: %q", lines[0])
				}
				// Lines 1 and 2 are outside selection — should be unchanged.
				if lines[1] != "foo bar baz" {
					t.Errorf("line 1 should be unchanged, got %q", lines[1])
				}
				if lines[2] != "goodbye cruel" {
					t.Errorf("line 2 should be unchanged, got %q", lines[2])
				}
			},
		},
		{
			name:          "partial line selection",
			output:        viewportOutput,
			yOffset:       0,
			state:         selectionState{start: selectionPoint{1, 4}, end: selectionPoint{1, 7}},
			viewportWidth: 80,
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				// Line 1 should have highlight (contains ANSI).
				if hasNoANSI(lines[1]) {
					t.Errorf("line 1 expected ANSI highlight, got plain: %q", lines[1])
				}
				// Lines 0 and 2 unchanged.
				if lines[0] != "hello world" {
					t.Errorf("line 0 should be unchanged, got %q", lines[0])
				}
				if lines[2] != "goodbye cruel" {
					t.Errorf("line 2 should be unchanged, got %q", lines[2])
				}
			},
		},
		{
			name:          "multi-line selection covers lines 0 and 1",
			output:        viewportOutput,
			yOffset:       0,
			state:         selectionState{start: selectionPoint{0, 6}, end: selectionPoint{1, 3}},
			viewportWidth: 80,
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				if hasNoANSI(lines[0]) {
					t.Errorf("line 0 expected ANSI highlight")
				}
				if hasNoANSI(lines[1]) {
					t.Errorf("line 1 expected ANSI highlight")
				}
				if lines[2] != "goodbye cruel" {
					t.Errorf("line 2 should be unchanged, got %q", lines[2])
				}
			},
		},
		{
			name:          "reversed selection is normalised",
			output:        viewportOutput,
			yOffset:       0,
			state:         selectionState{start: selectionPoint{1, 3}, end: selectionPoint{0, 6}},
			viewportWidth: 80,
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				if hasNoANSI(lines[0]) {
					t.Errorf("line 0 expected ANSI highlight")
				}
				if hasNoANSI(lines[1]) {
					t.Errorf("line 1 expected ANSI highlight")
				}
			},
		},
		{
			name:    "selection with yOffset — only matching content lines highlighted",
			output:  viewportOutput,
			yOffset: 5,
			// Content lines 5, 6, 7 correspond to viewport lines 0, 1, 2.
			// Select content line 6 (viewport line 1).
			state:         selectionState{start: selectionPoint{6, 0}, end: selectionPoint{6, 11}},
			viewportWidth: 80,
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				if lines[0] != "hello world" {
					t.Errorf("line 0 should be unchanged, got %q", lines[0])
				}
				if hasNoANSI(lines[1]) {
					t.Errorf("line 1 expected ANSI highlight")
				}
				if lines[2] != "goodbye cruel" {
					t.Errorf("line 2 should be unchanged, got %q", lines[2])
				}
			},
		},
		{
			name:   "selection entirely outside viewport — no lines modified",
			output: viewportOutput,
			// yOffset 0, viewport lines 0-2 map to content lines 0-2.
			// Selection is at content lines 10-12.
			yOffset:       0,
			state:         selectionState{start: selectionPoint{10, 0}, end: selectionPoint{12, 5}},
			viewportWidth: 80,
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				got := strings.Join(lines, "\n")
				if got != viewportOutput {
					t.Errorf("expected unchanged output, got %q", got)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := applyHighlight(tc.output, tc.yOffset, tc.state, testSelStyle, tc.viewportWidth)
			lines := strings.Split(result, "\n")
			tc.checkFn(t, lines)
		})
	}
}
