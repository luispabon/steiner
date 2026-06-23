package tui

import (
	"io"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/muesli/termenv"
)

// testRenderer forces TrueColor output so lipgloss emits ANSI codes in tests
// (the default renderer detects no terminal and strips all colour).
var testRenderer = func() *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	return r
}()

// testSelStyle is used as the highlight style in applyScreenHighlight tests.
var testSelStyle = testRenderer.NewStyle().Background(lipgloss.Color("#3a4a5a"))

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
		{
			name:  "multi-byte characters use visual column positions",
			lines: []string{"│ The user just said hello"},
			state: selectionState{start: selectionPoint{0, 16}, end: selectionPoint{0, 20}},
			want:  "said",
		},
		{
			name:  "em dash is single visual column",
			lines: []string{"foo — bar"},
			state: selectionState{start: selectionPoint{0, 6}, end: selectionPoint{0, 9}},
			want:  "bar",
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

func TestApplyScreenHighlight(t *testing.T) {
	t.Parallel()

	frame := "hello world\nfoo bar baz\ngoodbye cruel"

	tests := []struct {
		name    string
		frame   string
		state   selectionState
		checkFn func(t *testing.T, lines []string)
	}{
		{
			name:  "no selection returns frame unchanged",
			frame: frame,
			state: selectionState{},
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				got := strings.Join(lines, "\n")
				if got != frame {
					t.Errorf("expected unchanged frame, got %q", got)
				}
			},
		},
		{
			name:  "full first line selected",
			frame: frame,
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 11}},
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				if hasNoANSI(lines[0]) {
					t.Errorf("line 0 expected ANSI highlight, got plain: %q", lines[0])
				}
				if lines[1] != "foo bar baz" {
					t.Errorf("line 1 should be unchanged, got %q", lines[1])
				}
			},
		},
		{
			name:  "partial line selection",
			frame: frame,
			state: selectionState{start: selectionPoint{1, 4}, end: selectionPoint{1, 7}},
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				if hasNoANSI(lines[1]) {
					t.Errorf("line 1 expected ANSI highlight, got plain: %q", lines[1])
				}
				if lines[0] != "hello world" {
					t.Errorf("line 0 should be unchanged, got %q", lines[0])
				}
			},
		},
		{
			name:  "multi-line selection",
			frame: frame,
			state: selectionState{start: selectionPoint{0, 6}, end: selectionPoint{1, 3}},
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
			name:  "reversed selection is normalised",
			frame: frame,
			state: selectionState{start: selectionPoint{1, 3}, end: selectionPoint{0, 6}},
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
			name:  "selection outside frame — no lines modified",
			frame: frame,
			state: selectionState{start: selectionPoint{10, 0}, end: selectionPoint{12, 5}},
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				got := strings.Join(lines, "\n")
				if got != frame {
					t.Errorf("expected unchanged frame, got %q", got)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := applyScreenHighlight(tc.frame, tc.state, testSelStyle)
			lines := strings.Split(result, "\n")
			tc.checkFn(t, lines)
		})
	}
}
