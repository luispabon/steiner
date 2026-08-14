package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// testSelStyle is used as the highlight style in applyScreenHighlight tests.
var testSelStyle = lipgloss.NewStyle().Background(lipgloss.Color("#3a4a5a"))

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
		name        string
		lines       []string
		state       selectionState
		regionLeft  int
		regionRight int
		want        string
	}{
		{
			name:        "empty selection returns empty string",
			lines:       plainLines,
			state:       selectionState{},
			regionLeft:  0,
			regionRight: 0,
			want:        "",
		},
		{
			name:        "active flag but same point still returns empty (no content span)",
			lines:       plainLines,
			state:       selectionState{start: selectionPoint{0, 2}, end: selectionPoint{0, 2}, active: true},
			regionLeft:  0,
			regionRight: 0,
			want:        "",
		},
		{
			name:        "single line partial",
			lines:       plainLines,
			state:       selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 5}},
			regionLeft:  0,
			regionRight: 0,
			want:        "hello",
		},
		{
			name:        "single line full",
			lines:       plainLines,
			state:       selectionState{start: selectionPoint{1, 0}, end: selectionPoint{1, 11}},
			regionLeft:  0,
			regionRight: 0,
			want:        "foo bar baz",
		},
		{
			name:        "multi-line",
			lines:       plainLines,
			state:       selectionState{start: selectionPoint{0, 6}, end: selectionPoint{1, 3}},
			regionLeft:  0,
			regionRight: 0,
			want:        "world\nfoo",
		},
		{
			name:        "reversed start/end (end before start) is normalised",
			lines:       plainLines,
			state:       selectionState{start: selectionPoint{1, 3}, end: selectionPoint{0, 6}},
			regionLeft:  0,
			regionRight: 0,
			want:        "world\nfoo",
		},
		{
			name:        "out-of-range lines are skipped",
			lines:       plainLines,
			state:       selectionState{start: selectionPoint{-1, 0}, end: selectionPoint{10, 5}},
			regionLeft:  0,
			regionRight: 0,
			want:        "hello world\nfoo bar baz\ngoodbye cruel",
		},
		{
			name:        "col beyond line length clamps to end of line",
			lines:       plainLines,
			state:       selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 999}},
			regionLeft:  0,
			regionRight: 0,
			want:        "hello world",
		},
		{
			name:        "startCol beyond line length returns empty segment",
			lines:       plainLines,
			state:       selectionState{start: selectionPoint{0, 999}, end: selectionPoint{0, 999}},
			regionLeft:  0,
			regionRight: 0,
			want:        "",
		},
		{
			name:        "ANSI sequences are stripped from extracted text",
			lines:       ansiLines,
			state:       selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 8}},
			regionLeft:  0,
			regionRight: 0,
			want:        "red text",
		},
		{
			name:        "ANSI multi-line extraction",
			lines:       ansiLines,
			state:       selectionState{start: selectionPoint{0, 4}, end: selectionPoint{1, 5}},
			regionLeft:  0,
			regionRight: 0,
			want:        "text\ngreen",
		},
		{
			name:        "empty lines slice",
			lines:       []string{},
			state:       selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 5}},
			regionLeft:  0,
			regionRight: 0,
			want:        "",
		},
		{
			name:        "multi-byte characters use visual column positions",
			lines:       []string{"│ The user just said hello"},
			state:       selectionState{start: selectionPoint{0, 16}, end: selectionPoint{0, 20}},
			regionLeft:  0,
			regionRight: 0,
			want:        "said",
		},
		{
			name:        "em dash is single visual column",
			lines:       []string{"foo — bar"},
			state:       selectionState{start: selectionPoint{0, 6}, end: selectionPoint{0, 9}},
			regionLeft:  0,
			regionRight: 0,
			want:        "bar",
		},
		{
			name: "intermediate lines clamped to region bounds",
			lines: []string{
				"   hello world   ",   // 0
				"   foo bar baz   ",   // 1
				"   goodbye cruel   ", // 2
			},
			state:       selectionState{start: selectionPoint{0, 3}, end: selectionPoint{2, 16}},
			regionLeft:  3,
			regionRight: 16,
			want:        "hello world\nfoo bar baz\ngoodbye cruel",
		},
		{
			name:  "box chrome stripped from tool output",
			lines: []string{"│ content │"},
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 11}},
			want:  "content",
		},
		{
			name:  "nested box borders stripped",
			lines: []string{"│ ─── │"},
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 7}},
			want:  "",
		},
		{
			name:  "intentional indentation preserved",
			lines: []string{"│   indented code │"},
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 19}},
			want:  "  indented code",
		},
		{
			name:  "trailing space after box border stripped",
			lines: []string{"│ content │ "},
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 12}},
			want:  "content",
		},
		{
			name: "multi-line box with trailing spaces",
			lines: []string{
				"   │ line one │    ",
				"   │ line two │    ",
			},
			state:       selectionState{start: selectionPoint{0, 3}, end: selectionPoint{1, 15}},
			regionLeft:  3,
			regionRight: 15,
			want:        "line one\nline two",
		},
		{
			name:        "region bounds zero disables clamping",
			lines:       []string{"   hello world   "},
			state:       selectionState{start: selectionPoint{0, 3}, end: selectionPoint{0, 14}},
			regionLeft:  0,
			regionRight: 0,
			want:        "hello world",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractText(tc.lines, tc.state, tc.regionLeft, tc.regionRight)
			if got != tc.want {
				t.Errorf("extractText() = %q; want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestStripBoxChrome
// ---------------------------------------------------------------------------

func TestStripBoxChrome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		s    string
		want string
	}{
		{
			name: "plain text unchanged",
			s:    "hello world",
			want: "hello world",
		},
		{
			name: "leading border and padding stripped",
			s:    "│ content",
			want: "content",
		},
		{
			name: "trailing border and padding stripped",
			s:    "content │",
			want: "content",
		},
		{
			name: "both sides stripped",
			s:    "│ content │",
			want: "content",
		},
		{
			name: "empty string",
			s:    "",
			want: "",
		},
		{
			name: "only borders returns empty",
			s:    "│ │",
			want: "",
		},
		{
			name: "CJK content preserved",
			s:    "│ 你好 │",
			want: "你好",
		},
		{
			name: "intentional leading spaces preserved",
			s:    "│   indented code │",
			want: "  indented code",
		},
		{
			name: "mixed box characters",
			s:    "┌─ content ─┐",
			want: "content",
		},
		{
			name: "multiple border runs at edges only",
			s:    "││ content ││",
			want: "content",
		},
		{
			name: "spaces in middle preserved",
			s:    "│ hello   world │",
			want: "hello   world",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := stripBoxChrome(tc.s)
			if got != tc.want {
				t.Errorf("stripBoxChrome(%q) = %q; want %q", tc.s, got, tc.want)
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

// highlightVisualRange locates the single testSelStyle-painted span in line
// and returns its visual column range [start, end). If no visible characters
// carry the highlight, ok is false — this distinguishes an actually-painted
// span from a bare zero-width style transition that ansi.Cut can leave at a
// slice boundary.
func highlightVisualRange(line string) (start, end int, ok bool) {
	const openSeq = "\x1b[48;2;58;74;90m"
	const closeSeq = "\x1b[m"
	openIdx := strings.Index(line, openSeq)
	if openIdx == -1 {
		return 0, 0, false
	}
	contentStart := openIdx + len(openSeq)
	closeIdx := strings.Index(line[contentStart:], closeSeq)
	if closeIdx == -1 {
		return 0, 0, false
	}
	content := line[contentStart : contentStart+closeIdx]
	if content == "" {
		return 0, 0, false
	}
	start = ansi.StringWidth(line[:openIdx])
	end = start + ansi.StringWidth(content)
	return start, end, true
}

func TestApplyScreenHighlight(t *testing.T) {
	frame := "hello world\nfoo bar baz\ngoodbye cruel"

	tests := []struct {
		name        string
		frame       string
		state       selectionState
		regionLeft  int
		regionRight int
		checkFn     func(t *testing.T, lines []string)
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
		{
			name: "intermediate lines constrained to region bounds",
			// Each line simulates [sidebar][divider][pad][content][pad] laid
			// out across the full terminal width; only columns [40, 100)
			// belong to the viewport content region.
			frame: strings.Join([]string{
				strings.Repeat("s", 40) + "|" + strings.Repeat("c", 59) + strings.Repeat("p", 20),
				strings.Repeat("s", 40) + "|" + strings.Repeat("c", 59) + strings.Repeat("p", 20),
				strings.Repeat("s", 40) + "|" + strings.Repeat("c", 59) + strings.Repeat("p", 20),
			}, "\n"),
			state:       selectionState{start: selectionPoint{0, 45}, end: selectionPoint{2, 50}},
			regionLeft:  40,
			regionRight: 100,
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				// The intermediate line (index 1) is fully selected by
				// canonical range, so its highlight must be clamped to
				// [40, 100).
				mid := lines[1]
				start, end, ok := highlightVisualRange(mid)
				if !ok {
					t.Fatalf("expected a highlighted span in intermediate line, got %q", mid)
				}
				if start != 40 || end != 100 {
					t.Errorf("intermediate line highlight range = [%d, %d); want [40, 100)", start, end)
				}
			},
		},
		{
			name: "end line constrained to region right bound",
			frame: strings.Join([]string{
				strings.Repeat("s", 40) + "|" + strings.Repeat("c", 59) + strings.Repeat("p", 20),
				strings.Repeat("s", 40) + "|" + strings.Repeat("c", 59) + strings.Repeat("p", 20),
			}, "\n"),
			state:       selectionState{start: selectionPoint{0, 45}, end: selectionPoint{1, 999}},
			regionLeft:  40,
			regionRight: 100,
			checkFn: func(t *testing.T, lines []string) {
				t.Helper()
				last := lines[1]
				start, end, ok := highlightVisualRange(last)
				if !ok {
					t.Fatalf("expected a highlighted span in end line, got %q", last)
				}
				if start != 40 || end != 100 {
					t.Errorf("end line highlight range = [%d, %d); want [40, 100) (clamped to regionRight)", start, end)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useTrueColor(t)
			result := applyScreenHighlight(tc.frame, tc.state, testSelStyle, tc.regionLeft, tc.regionRight)
			lines := strings.Split(result, "\n")
			tc.checkFn(t, lines)
		})
	}
}

// ---------------------------------------------------------------------------
// TestWordBoundsAt
// ---------------------------------------------------------------------------

func TestWordBoundsAt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		line         string
		col          int
		wantStartCol int
		wantEndCol   int
	}{
		{
			name:         "middle of an ASCII word",
			line:         "hello world",
			col:          2,
			wantStartCol: 0,
			wantEndCol:   5,
		},
		{
			name:         "start of a word",
			line:         "hello world",
			col:          0,
			wantStartCol: 0,
			wantEndCol:   5,
		},
		{
			name:         "end of a word (last char index, not one-past)",
			line:         "hello world",
			col:          4,
			wantStartCol: 0,
			wantEndCol:   5,
		},
		{
			name:         "second word",
			line:         "hello world",
			col:          8,
			wantStartCol: 6,
			wantEndCol:   11,
		},
		{
			name:         "underscore is a word character",
			line:         "foo_bar baz",
			col:          0,
			wantStartCol: 0,
			wantEndCol:   7,
		},
		{
			name:         "digits are word characters",
			line:         "abc123 def",
			col:          4,
			wantStartCol: 0,
			wantEndCol:   6,
		},
		{
			name:         "kebab-case identifier (start)",
			line:         "foo-bar baz",
			col:          0,
			wantStartCol: 0,
			wantEndCol:   7,
		},
		{
			name:         "kebab-case identifier (middle)",
			line:         "foo-bar",
			col:          2,
			wantStartCol: 0,
			wantEndCol:   7,
		},
		{
			name:         "kebab-case identifier (at hyphen)",
			line:         "foo-bar",
			col:          3,
			wantStartCol: 0,
			wantEndCol:   7,
		},
		{
			name:         "word starting with hyphen",
			line:         "-flag",
			col:          0,
			wantStartCol: 0,
			wantEndCol:   5,
		},
		{
			name:         "col on punctuation selects only that character",
			line:         "foo, bar",
			col:          3,
			wantStartCol: 3,
			wantEndCol:   4,
		},
		{
			name:         "col on space selects only that character",
			line:         "foo bar",
			col:          3,
			wantStartCol: 3,
			wantEndCol:   4,
		},
		{
			name:         "empty line",
			line:         "",
			col:          0,
			wantStartCol: 0,
			wantEndCol:   0,
		},
		{
			name:         "col beyond line length clamps to line width",
			line:         "hi",
			col:          50,
			wantStartCol: 2,
			wantEndCol:   2,
		},
		{
			name:         "negative col clamps to zero",
			line:         "hi",
			col:          -3,
			wantStartCol: 0,
			wantEndCol:   2,
		},
		{
			name:         "CJK wide characters counted by visual width",
			line:         "你好 world",
			col:          0,
			wantStartCol: 0,
			wantEndCol:   4,
		},
		{
			name:         "col inside a CJK word (second wide char)",
			line:         "你好 world",
			col:          2,
			wantStartCol: 0,
			wantEndCol:   4,
		},
		{
			name:         "col in ASCII word following CJK",
			line:         "你好 world",
			col:          5,
			wantStartCol: 5,
			wantEndCol:   10,
		},
		{
			name:         "double-click URL selects full URL",
			line:         "see http://example.com/path here",
			col:          8,
			wantStartCol: 4,
			wantEndCol:   27,
		},
		{
			name:         "double-click absolute path selects full path",
			line:         "edit /home/user/file.txt now",
			col:          8,
			wantStartCol: 5,
			wantEndCol:   24,
		},
		{
			name:         "double-click tilde path selects full path",
			line:         "cat ~/docs/readme.md",
			col:          7,
			wantStartCol: 4,
			wantEndCol:   20,
		},
		{
			name:         "double-click relative path selects full path",
			line:         "run ./scripts/build.sh",
			col:          8,
			wantStartCol: 4,
			wantEndCol:   22,
		},
		{
			name:         "double-click hidden folder path selects full path",
			line:         "run .project_planning/2026-08-05_mcp-walking-skeleton",
			col:          6,
			wantStartCol: 4,
			wantEndCol:   53,
		},
		{
			name:         "double-click branch name selects full name",
			line:         "run cl/2026-08-05_mcp_approval_model",
			col:          8,
			wantStartCol: 4,
			wantEndCol:   36,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotStart, gotEnd := wordBoundsAt(tc.line, tc.col)
			if gotStart != tc.wantStartCol || gotEnd != tc.wantEndCol {
				t.Errorf("wordBoundsAt(%q, %d) = (%d, %d); want (%d, %d)",
					tc.line, tc.col, gotStart, gotEnd, tc.wantStartCol, tc.wantEndCol)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestURLBoundsAt
// ---------------------------------------------------------------------------

func TestURLBoundsAt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		line         string
		col          int
		wantStartCol int
		wantEndCol   int
		wantOk       bool
	}{
		{
			name:         "simple http URL",
			line:         "visit http://example.com now",
			col:          10,
			wantStartCol: 6,
			wantEndCol:   24,
			wantOk:       true,
		},
		{
			name:         "https URL with path",
			line:         "see https://example.com/path?q=1",
			col:          8,
			wantStartCol: 4,
			wantEndCol:   32,
			wantOk:       true,
		},
		{
			name:         "URL at start of line",
			line:         "http://example.com is here",
			col:          0,
			wantStartCol: 0,
			wantEndCol:   18,
			wantOk:       true,
		},
		{
			name:         "URL at end of line",
			line:         "go to http://example.com",
			col:          16,
			wantStartCol: 6,
			wantEndCol:   24,
			wantOk:       true,
		},
		{
			name:         "cursor not in URL returns false",
			line:         "text http://x.com more",
			col:          1,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "ftp URL",
			line:         "download ftp://files.example.com/foo",
			col:          14,
			wantStartCol: 9,
			wantEndCol:   36,
			wantOk:       true,
		},
		{
			name:         "URL followed by comma",
			line:         "see http://example.com, ok",
			col:          10,
			wantStartCol: 4,
			wantEndCol:   22,
			wantOk:       true,
		},
		{
			name:         "URL followed by period",
			line:         "see http://example.com.",
			col:          10,
			wantStartCol: 4,
			wantEndCol:   22,
			wantOk:       true,
		},
		{
			name:         "URL in parentheses",
			line:         "(http://example.com/path)",
			col:          5,
			wantStartCol: 1,
			wantEndCol:   24,
			wantOk:       true,
		},
		{
			name:         "no URL in line",
			line:         "hello world",
			col:          2,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "empty line",
			line:         "",
			col:          0,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "col beyond line",
			line:         "hi",
			col:          50,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotStart, gotEnd, gotOk := urlBoundsAt(tc.line, tc.col)
			if gotStart != tc.wantStartCol || gotEnd != tc.wantEndCol || gotOk != tc.wantOk {
				t.Errorf("urlBoundsAt(%q, %d) = (%d, %d, %v); want (%d, %d, %v)",
					tc.line, tc.col, gotStart, gotEnd, gotOk, tc.wantStartCol, tc.wantEndCol, tc.wantOk)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestPathBoundsAt
// ---------------------------------------------------------------------------

func TestPathBoundsAt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		line         string
		col          int
		wantStartCol int
		wantEndCol   int
		wantOk       bool
	}{
		{
			name:         "absolute path",
			line:         "edit /home/user/file.txt now",
			col:          10,
			wantStartCol: 5,
			wantEndCol:   24,
			wantOk:       true,
		},
		{
			name:         "absolute path at start",
			line:         "/usr/bin/bash",
			col:          2,
			wantStartCol: 0,
			wantEndCol:   13,
			wantOk:       true,
		},
		{
			name:         "absolute path with trailing slash",
			line:         "ls /home/user/",
			col:          8,
			wantStartCol: 3,
			wantEndCol:   14,
			wantOk:       true,
		},
		{
			name:         "tilde home path",
			line:         "cat ~/docs/readme.md",
			col:          7,
			wantStartCol: 4,
			wantEndCol:   20,
			wantOk:       true,
		},
		{
			name:         "tilde alone",
			line:         "cd ~",
			col:          3,
			wantStartCol: 3,
			wantEndCol:   4,
			wantOk:       true,
		},
		{
			name:         "relative path dot-slash",
			line:         "run ./scripts/build.sh",
			col:          8,
			wantStartCol: 4,
			wantEndCol:   22,
			wantOk:       true,
		},
		{
			name:         "relative path dot-dot-slash",
			line:         "cd ../parent/dir",
			col:          5,
			wantStartCol: 3,
			wantEndCol:   16,
			wantOk:       true,
		},
		{
			name:         "dot in prose not a path",
			line:         "the foo.bar is",
			col:          6,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "colon not a URL",
			line:         "x:y in code",
			col:          1,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "plain text",
			line:         "hello world",
			col:          2,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "empty line",
			line:         "",
			col:          0,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "col beyond line",
			line:         "hi",
			col:          50,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "bare relative path, click at start",
			line:         "Source: internal/tui/selection.go",
			col:          8,
			wantStartCol: 8,
			wantEndCol:   33,
			wantOk:       true,
		},
		{
			name:         "bare relative path, click at middle segment",
			line:         "Source: internal/tui/selection.go",
			col:          13,
			wantStartCol: 8,
			wantEndCol:   33,
			wantOk:       true,
		},
		{
			name:         "bare relative path, click at last segment",
			line:         "Source: internal/tui/selection.go",
			col:          20,
			wantStartCol: 8,
			wantEndCol:   33,
			wantOk:       true,
		},
		{
			name:         "bare relative path, two segments",
			line:         "Path: cmd/steiner/main.go",
			col:          10,
			wantStartCol: 6,
			wantEndCol:   25,
			wantOk:       true,
		},
		{
			name:         "bare relative path, single separator with extension",
			line:         "See docs/oneshot.md here",
			col:          6,
			wantStartCol: 4,
			wantEndCol:   19,
			wantOk:       true,
		},
		{
			name:         "line ref on bare relative path",
			line:         "Source: internal/tui/selection.go:363",
			col:          20,
			wantStartCol: 8,
			wantEndCol:   37,
			wantOk:       true,
		},
		{
			name:         "line and column ref",
			line:         "See cmd/main.go:12:5 too",
			col:          6,
			wantStartCol: 4,
			wantEndCol:   20,
			wantOk:       true,
		},
		{
			name:         "line ref on absolute path",
			line:         "Open /abs/path.go:99 now",
			col:          8,
			wantStartCol: 5,
			wantEndCol:   20,
			wantOk:       true,
		},
		{
			name:         "line ref on tilde path",
			line:         "cd ~/x/y.go:1 now",
			col:          5,
			wantStartCol: 3,
			wantEndCol:   13,
			wantOk:       true,
		},
		{
			name:         "and/or is not a path",
			line:         "and/or is fine",
			col:          1,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "TCP/IP is not a path",
			line:         "over TCP/IP now",
			col:          6,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "24/7 is not a path",
			line:         "open 24/7 always",
			col:          6,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "12/25 is not a path",
			line:         "due 12/25 soon",
			col:          5,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "he/she is not a path",
			line:         "he/she left",
			col:          1,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "path preceded by box-drawing rune",
			line:         "│internal/tui/foo.go",
			col:          5,
			wantStartCol: 1,
			wantEndCol:   20,
			wantOk:       true,
		},
		{
			name:         "path preceded by open paren",
			line:         "(cmd/steiner/main.go)",
			col:          5,
			wantStartCol: 1,
			wantEndCol:   20,
			wantOk:       true,
		},
		{
			name:         "path preceded by equals",
			line:         "path=cmd/steiner/main.go",
			col:          10,
			wantStartCol: 5,
			wantEndCol:   24,
			wantOk:       true,
		},
		{
			name:         "path at start of line",
			line:         "internal/tui/selection.go is the file",
			col:          0,
			wantStartCol: 0,
			wantEndCol:   25,
			wantOk:       true,
		},
		{
			name:         "path preceded by a letter must not match",
			line:         "Isaw/etc for now",
			col:          5,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "trailing period trimmed",
			line:         "see docs/oneshot.md.",
			col:          6,
			wantStartCol: 4,
			wantEndCol:   19,
			wantOk:       true,
		},
		{
			name:         "trailing close-paren excluded",
			line:         "(see cmd/steiner/main.go)",
			col:          8,
			wantStartCol: 5,
			wantEndCol:   24,
			wantOk:       true,
		},
		{
			// Accepted trade-off: a full date has two separators, which
			// satisfies the same bare-relative heuristic as a real path.
			// 12/25 alone (one separator) is correctly rejected above.
			name:         "full date matches bare-relative heuristic",
			line:         "due 12/25/2026 soon",
			col:          5,
			wantStartCol: 4,
			wantEndCol:   14,
			wantOk:       true,
		},
		{
			// A tab has zero visual width in runewidth, so it doesn't shift
			// the path's start column, but it must still count as a
			// boundary rune (real bash tool output can indent with tabs).
			name:         "path preceded by a tab is a boundary",
			line:         "\tinternal/tui/foo.go",
			col:          5,
			wantStartCol: 0,
			wantEndCol:   19,
			wantOk:       true,
		},
		{
			// Accepted trade-off: the only alternative that can match here
			// starts at "com" (bare-relative, ending in .html), but "." is
			// not a boundary rune, so the whole thing is rejected rather
			// than partially selecting "com/index.html".
			name:         "dotted first segment is rejected",
			line:         "see example.com/index.html now",
			col:          17,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "hidden folder path",
			line:         "see .project_planning/2026-08-05_mcp-walking-skeleton now",
			col:          20,
			wantStartCol: 4,
			wantEndCol:   53,
			wantOk:       true,
		},
		{
			name:         "hidden folder path at start",
			line:         ".project_planning/2026-08-05_mcp-walking-skeleton",
			col:          0,
			wantStartCol: 0,
			wantEndCol:   49,
			wantOk:       true,
		},
		{
			name:         "hidden file alone",
			line:         "edit .env now",
			col:          6,
			wantStartCol: 5,
			wantEndCol:   9,
			wantOk:       true,
		},
		{
			name:         "hidden path with line ref",
			line:         "Source: .project_planning/x:12",
			col:          10,
			wantStartCol: 8,
			wantEndCol:   30,
			wantOk:       true,
		},
		{
			name:         "hidden path trailing period trimmed",
			line:         "see .project_planning/foo.",
			col:          6,
			wantStartCol: 4,
			wantEndCol:   25,
			wantOk:       true,
		},
		{
			// Accepted trade-off, mirroring "full date matches": a dot-prefixed
			// token at a boundary is treated as a hidden file, so prose ".NET"
			// selects as one token instead of falling back to "NET".
			name:         "hidden-style prose token is an accepted trade-off",
			line:         "use .NET now",
			col:          5,
			wantStartCol: 4,
			wantEndCol:   8,
			wantOk:       true,
		},
		{
			name:         "mid-token dot hidden suffix is rejected",
			line:         "the.env is config",
			col:          5,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "branch name with long slug",
			line:         "On branch cl/2026-08-05_mcp_approval_model now",
			col:          15,
			wantStartCol: 10,
			wantEndCol:   42,
			wantOk:       true,
		},
		{
			name:         "branch name at start",
			line:         "cl/2026-08-05_mcp_approval_model",
			col:          0,
			wantStartCol: 0,
			wantEndCol:   32,
			wantOk:       true,
		},
		{
			name:         "namespace ref origin/main",
			line:         "origin/main",
			col:          3,
			wantStartCol: 0,
			wantEndCol:   11,
			wantOk:       true,
		},
		{
			name:         "single-separator with line ref",
			line:         "Source: cl/2026-08-05_mcp_approval_model:12",
			col:          20,
			wantStartCol: 8,
			wantEndCol:   43,
			wantOk:       true,
		},
		{
			name:         "short single-separator pair is not a path",
			line:         "src/main",
			col:          3,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
		{
			name:         "short prose pair still rejected",
			line:         "foo/bar",
			col:          2,
			wantStartCol: 0,
			wantEndCol:   0,
			wantOk:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotStart, gotEnd, gotOk := pathBoundsAt(tc.line, tc.col)
			if gotStart != tc.wantStartCol || gotEnd != tc.wantEndCol || gotOk != tc.wantOk {
				t.Errorf("pathBoundsAt(%q, %d) = (%d, %d, %v); want (%d, %d, %v)",
					tc.line, tc.col, gotStart, gotEnd, gotOk, tc.wantStartCol, tc.wantEndCol, tc.wantOk)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestLineBoundsAt
// ---------------------------------------------------------------------------

func TestLineBoundsAt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		lines        []string
		lineIdx      int
		wantStartCol int
		wantEndCol   int
	}{
		{
			name:         "plain line",
			lines:        []string{"hello world"},
			lineIdx:      0,
			wantStartCol: 0,
			wantEndCol:   11,
		},
		{
			name:         "line with ANSI escape codes measures stripped width",
			lines:        []string{"\x1b[31mred text\x1b[0m"},
			lineIdx:      0,
			wantStartCol: 0,
			wantEndCol:   8,
		},
		{
			name:         "empty line",
			lines:        []string{""},
			lineIdx:      0,
			wantStartCol: 0,
			wantEndCol:   0,
		},
		{
			name:         "second of multiple lines",
			lines:        []string{"foo", "bar baz"},
			lineIdx:      1,
			wantStartCol: 0,
			wantEndCol:   7,
		},
		{
			name:         "negative index out of range",
			lines:        []string{"foo"},
			lineIdx:      -1,
			wantStartCol: 0,
			wantEndCol:   0,
		},
		{
			name:         "index beyond slice length out of range",
			lines:        []string{"foo"},
			lineIdx:      5,
			wantStartCol: 0,
			wantEndCol:   0,
		},
		{
			name:         "empty lines slice",
			lines:        []string{},
			lineIdx:      0,
			wantStartCol: 0,
			wantEndCol:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotStart, gotEnd := lineBoundsAt(tc.lines, tc.lineIdx)
			if gotStart != tc.wantStartCol || gotEnd != tc.wantEndCol {
				t.Errorf("lineBoundsAt(lines, %d) = (%d, %d); want (%d, %d)",
					tc.lineIdx, gotStart, gotEnd, tc.wantStartCol, tc.wantEndCol)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestLogicalLineBounds
// ---------------------------------------------------------------------------

func TestLogicalLineBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		lines         []string
		lineIdx       int
		regionLeft    int
		regionRight   int
		wantStartLine int
		wantEndLine   int
		wantStartCol  int
		wantEndCol    int
	}{
		{
			name:          "single short line selects only that line",
			lines:         []string{"hello world"},
			lineIdx:       0,
			regionLeft:    0,
			regionRight:   20,
			wantStartLine: 0,
			wantEndLine:   0,
			wantStartCol:  0,
			wantEndCol:    11,
		},
		{
			name: "wrapped line spans two screen lines",
			lines: []string{
				"0123456789012345678", // full 19-wide line (threshold 18)
				"tail",
			},
			lineIdx:       0,
			regionLeft:    0,
			regionRight:   20,
			wantStartLine: 0,
			wantEndLine:   1,
			wantStartCol:  0,
			wantEndCol:    4,
		},
		{
			name: "wrapped line spans three screen lines",
			lines: []string{
				"0123456789012345678",
				"9876543210987654321",
				"tail",
			},
			lineIdx:       1,
			regionLeft:    0,
			regionRight:   20,
			wantStartLine: 0,
			wantEndLine:   2,
			wantStartCol:  0,
			wantEndCol:    4,
		},
		{
			name: "adjacent short lines not merged",
			lines: []string{
				"short one",
				"short two",
			},
			lineIdx:       0,
			regionLeft:    0,
			regionRight:   20,
			wantStartLine: 0,
			wantEndLine:   0,
			wantStartCol:  0,
			wantEndCol:    9,
		},
		{
			name: "empty continuation stops walk",
			lines: []string{
				"0123456789012345678",
				"",
				"unrelated text",
			},
			lineIdx:       0,
			regionLeft:    0,
			regionRight:   20,
			wantStartLine: 0,
			wantEndLine:   0,
			wantStartCol:  0,
			wantEndCol:    19,
		},
		{
			name:          "out of range lineIdx returns zero-width (negative)",
			lines:         []string{"hello"},
			lineIdx:       -1,
			regionLeft:    0,
			regionRight:   20,
			wantStartLine: -1,
			wantEndLine:   -1,
			wantStartCol:  0,
			wantEndCol:    0,
		},
		{
			name:          "out of range lineIdx returns zero-width (beyond)",
			lines:         []string{"hello"},
			lineIdx:       5,
			regionLeft:    0,
			regionRight:   20,
			wantStartLine: 5,
			wantEndLine:   5,
			wantStartCol:  0,
			wantEndCol:    0,
		},
		{
			name: "region bounds correctly scope content width",
			lines: []string{
				"  0123456789012345678  ", // gutter padding outside region
				"  tail                 ",
			},
			lineIdx:       0,
			regionLeft:    2,
			regionRight:   22,
			wantStartLine: 0,
			wantEndLine:   1,
			wantStartCol:  2,
			wantEndCol:    6,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotStartLine, gotEndLine, gotStartCol, gotEndCol := logicalLineBounds(
				tc.lines, tc.lineIdx, tc.regionLeft, tc.regionRight)
			if gotStartLine != tc.wantStartLine || gotEndLine != tc.wantEndLine ||
				gotStartCol != tc.wantStartCol || gotEndCol != tc.wantEndCol {
				t.Errorf("logicalLineBounds(lines, %d, %d, %d) = (%d, %d, %d, %d); want (%d, %d, %d, %d)",
					tc.lineIdx, tc.regionLeft, tc.regionRight,
					gotStartLine, gotEndLine, gotStartCol, gotEndCol,
					tc.wantStartLine, tc.wantEndLine, tc.wantStartCol, tc.wantEndCol)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestDetectRegion
// ---------------------------------------------------------------------------

// buildTestModel creates a minimal Model for region detection tests.
func buildTestModel(width, height int, sidebarVisible, sidebarRight bool) *Model {
	vp := newScrollModel(width-6, max(1, height-5))

	s := testStyles(theme.AccentAmber)
	m := Model{
		width:    width,
		height:   height,
		viewport: vp,
		sidebar:  sidebarState{expanded: sidebarVisible, styles: s},
		input:    textarea.New(),
		styles:   s,
	}
	if sidebarRight {
		m.sidebarPosition = "right"
	}
	return &m
}

func TestDetectRegion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		width      int
		height     int
		sidebarVis bool
		sidebarPos string
		clickX     int
		clickY     int
		wantRegion selectionRegion
	}{
		{
			name:       "viewport center without sidebar",
			width:      100,
			height:     30,
			sidebarVis: false,
			clickX:     50,
			clickY:     15,
			wantRegion: regionViewport,
		},
		{
			name:       "viewport left edge without sidebar",
			width:      100,
			height:     30,
			sidebarVis: false,
			clickX:     0,
			clickY:     15,
			wantRegion: regionViewport,
		},
		{
			name:       "sidebar left position",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "left",
			clickX:     20,
			clickY:     15,
			wantRegion: regionSidebar,
		},
		{
			name:       "divider column when sidebar left",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "left",
			clickX:     36,
			clickY:     15,
			wantRegion: regionNone,
		},
		{
			name:       "viewport after sidebar left",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "left",
			clickX:     50,
			clickY:     15,
			wantRegion: regionViewport,
		},
		{
			name:       "sidebar right position",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "right",
			clickX:     90,
			clickY:     15,
			wantRegion: regionSidebar,
		},
		{
			name:       "divider column when sidebar right",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "right",
			clickX:     63,
			clickY:     15,
			wantRegion: regionNone,
		},
		{
			name:       "viewport before sidebar right",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "right",
			clickX:     40,
			clickY:     15,
			wantRegion: regionViewport,
		},
		{
			name:       "status bar at bottom",
			width:      100,
			height:     30,
			sidebarVis: false,
			clickX:     50,
			clickY:     29,
			wantRegion: regionNone,
		},
		{
			name:       "input area near bottom",
			width:      100,
			height:     30,
			sidebarVis: false,
			clickX:     50,
			clickY:     26,
			wantRegion: regionInput,
		},
		{
			name:       "activity row above input is regionNone",
			width:      100,
			height:     30,
			sidebarVis: false,
			clickX:     50,
			clickY:     25,
			wantRegion: regionNone,
		},
		{
			name:       "hDivider row above activity row is regionNone",
			width:      100,
			height:     30,
			sidebarVis: false,
			clickX:     50,
			clickY:     24,
			wantRegion: regionNone,
		},
		{
			name:       "sidebar left takes priority over input row",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "left",
			clickX:     20,
			clickY:     26,
			wantRegion: regionSidebar,
		},
		{
			name:       "sidebar right takes priority over input row",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "right",
			clickX:     90,
			clickY:     26,
			wantRegion: regionSidebar,
		},
		{
			name:       "out of bounds negative x",
			width:      100,
			height:     30,
			sidebarVis: false,
			clickX:     -1,
			clickY:     15,
			wantRegion: regionNone,
		},
		{
			name:       "out of bounds negative y",
			width:      100,
			height:     30,
			sidebarVis: false,
			clickX:     50,
			clickY:     -1,
			wantRegion: regionNone,
		},
		{
			name:       "out of bounds x >= width",
			width:      100,
			height:     30,
			sidebarVis: false,
			clickX:     100,
			clickY:     15,
			wantRegion: regionNone,
		},
		{
			name:       "out of bounds y >= height",
			width:      100,
			height:     30,
			sidebarVis: false,
			clickX:     50,
			clickY:     30,
			wantRegion: regionNone,
		},
		{
			name:       "sidebar hidden, all content",
			width:      100,
			height:     30,
			sidebarVis: false,
			clickX:     30,
			clickY:     15,
			wantRegion: regionViewport,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := buildTestModel(tc.width, tc.height, tc.sidebarVis, tc.sidebarPos == "right")
			got := m.detectRegion(tc.clickX, tc.clickY)
			if got != tc.wantRegion {
				t.Errorf("detectRegion(%d, %d) = %v; want %v", tc.clickX, tc.clickY, got, tc.wantRegion)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestClickCount
// ---------------------------------------------------------------------------

func TestClickCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		clicks []struct {
			delay time.Duration
			x     int
			y     int
		}
		wantCounts []int
	}{
		{
			name: "single click sets count to 1",
			clicks: []struct {
				delay time.Duration
				x     int
				y     int
			}{
				{0, 50, 15},
			},
			wantCounts: []int{1},
		},
		{
			name: "double click increments to 2",
			clicks: []struct {
				delay time.Duration
				x     int
				y     int
			}{
				{0, 50, 15},
				{100 * time.Millisecond, 50, 15},
			},
			wantCounts: []int{1, 2},
		},
		{
			name: "triple click increments to 3",
			clicks: []struct {
				delay time.Duration
				x     int
				y     int
			}{
				{0, 50, 15},
				{100 * time.Millisecond, 50, 15},
				{100 * time.Millisecond, 50, 15},
			},
			wantCounts: []int{1, 2, 3},
		},
		{
			name: "fourth click cycles back to 1",
			clicks: []struct {
				delay time.Duration
				x     int
				y     int
			}{
				{0, 50, 15},
				{100 * time.Millisecond, 50, 15},
				{100 * time.Millisecond, 50, 15},
				{100 * time.Millisecond, 50, 15},
			},
			wantCounts: []int{1, 2, 3, 1},
		},
		{
			name: "click timeout resets to 1",
			clicks: []struct {
				delay time.Duration
				x     int
				y     int
			}{
				{0, 50, 15},
				{600 * time.Millisecond, 50, 15},
			},
			wantCounts: []int{1, 1},
		},
		{
			name: "position drift resets to 1",
			clicks: []struct {
				delay time.Duration
				x     int
				y     int
			}{
				{0, 50, 15},
				{100 * time.Millisecond, 53, 15},
			},
			wantCounts: []int{1, 1},
		},
		{
			name: "position within ±1 cell accepted",
			clicks: []struct {
				delay time.Duration
				x     int
				y     int
			}{
				{0, 50, 15},
				{100 * time.Millisecond, 51, 15},
				{100 * time.Millisecond, 50, 16},
			},
			wantCounts: []int{1, 2, 3},
		},
		{
			name: "mixed sequence with reset",
			clicks: []struct {
				delay time.Duration
				x     int
				y     int
			}{
				{0, 50, 15},
				{100 * time.Millisecond, 50, 15},
				{600 * time.Millisecond, 50, 15},
				{100 * time.Millisecond, 50, 15},
			},
			wantCounts: []int{1, 2, 1, 2},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := buildTestModel(100, 30, false, false)
			var gotCounts []int

			clickTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for i, click := range tc.clicks {
				if i > 0 {
					clickTime = clickTime.Add(click.delay)
				}

				clickPos := selectionPoint{line: click.y, col: click.x}
				m.clickCount = m.nextClickCount(clickPos, clickTime)
				m.lastClickTime = clickTime
				m.lastClickPos = clickPos
				gotCounts = append(gotCounts, m.clickCount)
			}

			if len(gotCounts) != len(tc.wantCounts) {
				t.Errorf("got %d counts, want %d", len(gotCounts), len(tc.wantCounts))
				return
			}

			for i, got := range gotCounts {
				if got != tc.wantCounts[i] {
					t.Errorf("click %d: got count %d, want %d", i, got, tc.wantCounts[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestClampToRegion
// ---------------------------------------------------------------------------

func TestClampToRegion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		width      int
		height     int
		sidebarVis bool
		sidebarPos string
		region     selectionRegion
		x          int
		y          int
		wantX      int
		wantY      int
	}{
		{
			name:       "viewport no sidebar center",
			width:      100,
			height:     30,
			sidebarVis: false,
			region:     regionViewport,
			x:          50,
			y:          15,
			wantX:      50,
			wantY:      15,
		},
		{
			name:       "viewport no sidebar left edge clamp",
			width:      100,
			height:     30,
			sidebarVis: false,
			region:     regionViewport,
			x:          0,
			y:          15,
			wantX:      3,
			wantY:      15,
		},
		{
			name:       "viewport no sidebar right edge clamp",
			width:      100,
			height:     30,
			sidebarVis: false,
			region:     regionViewport,
			x:          99,
			y:          15,
			wantX:      96,
			wantY:      15,
		},
		{
			name:       "viewport no sidebar below divider clamp",
			width:      100,
			height:     30,
			sidebarVis: false,
			region:     regionViewport,
			x:          50,
			y:          29,
			wantX:      50,
			wantY:      23,
		},
		{
			name:       "viewport no sidebar above viewport clamp",
			width:      100,
			height:     30,
			sidebarVis: false,
			region:     regionViewport,
			x:          50,
			y:          -1,
			wantX:      50,
			wantY:      0,
		},
		{
			name:       "viewport sidebar left center",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "left",
			region:     regionViewport,
			x:          50,
			y:          15,
			wantX:      50,
			wantY:      15,
		},
		{
			name:       "viewport sidebar left left edge clamp",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "left",
			region:     regionViewport,
			x:          37,
			y:          15,
			wantX:      40,
			wantY:      15,
		},
		{
			name:       "viewport sidebar left right edge clamp",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "left",
			region:     regionViewport,
			x:          99,
			y:          15,
			wantX:      96,
			wantY:      15,
		},
		{
			name:       "viewport sidebar right center",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "right",
			region:     regionViewport,
			x:          30,
			y:          15,
			wantX:      30,
			wantY:      15,
		},
		{
			name:       "viewport sidebar right left edge clamp",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "right",
			region:     regionViewport,
			x:          0,
			y:          15,
			wantX:      3,
			wantY:      15,
		},
		{
			name:       "viewport sidebar right right edge clamp",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "right",
			region:     regionViewport,
			x:          65,
			y:          15,
			wantX:      59,
			wantY:      15,
		},
		{
			name:       "input no sidebar center",
			width:      100,
			height:     30,
			sidebarVis: false,
			region:     regionInput,
			x:          50,
			y:          27,
			wantX:      50,
			wantY:      27,
		},
		{
			name:       "input no sidebar left edge clamp",
			width:      100,
			height:     30,
			sidebarVis: false,
			region:     regionInput,
			x:          0,
			y:          27,
			wantX:      2,
			wantY:      27,
		},
		{
			name:       "input no sidebar right edge clamp",
			width:      100,
			height:     30,
			sidebarVis: false,
			region:     regionInput,
			x:          99,
			y:          27,
			wantX:      98,
			wantY:      27,
		},
		{
			name:       "input no sidebar above input clamp",
			width:      100,
			height:     30,
			sidebarVis: false,
			region:     regionInput,
			x:          50,
			y:          20,
			wantX:      50,
			wantY:      26,
		},
		{
			name:       "input no sidebar in status row clamp",
			width:      100,
			height:     30,
			sidebarVis: false,
			region:     regionInput,
			x:          50,
			y:          29,
			wantX:      50,
			wantY:      28,
		},
		{
			name:       "input sidebar left center",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "left",
			region:     regionInput,
			x:          50,
			y:          27,
			wantX:      50,
			wantY:      27,
		},
		{
			name:       "input sidebar left left edge clamp",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "left",
			region:     regionInput,
			x:          37,
			y:          27,
			wantX:      39,
			wantY:      27,
		},
		{
			name:       "input sidebar left right edge clamp",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "left",
			region:     regionInput,
			x:          99,
			y:          27,
			wantX:      99,
			wantY:      27,
		},
		{
			name:       "input sidebar right center",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "right",
			region:     regionInput,
			x:          30,
			y:          27,
			wantX:      30,
			wantY:      27,
		},
		{
			name:       "input sidebar right left edge clamp",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "right",
			region:     regionInput,
			x:          0,
			y:          27,
			wantX:      2,
			wantY:      27,
		},
		{
			name:       "input sidebar right right edge clamp",
			width:      100,
			height:     30,
			sidebarVis: true,
			sidebarPos: "right",
			region:     regionInput,
			x:          65,
			y:          27,
			wantX:      62,
			wantY:      27,
		},
		{
			name:       "regionNone returns unchanged",
			width:      100,
			height:     30,
			sidebarVis: false,
			region:     regionNone,
			x:          -5,
			y:          999,
			wantX:      -5,
			wantY:      999,
		},
		{
			name:       "regionSidebar returns unchanged",
			width:      100,
			height:     30,
			sidebarVis: true,
			region:     regionSidebar,
			x:          10,
			y:          15,
			wantX:      10,
			wantY:      15,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := buildTestModel(tc.width, tc.height, tc.sidebarVis, tc.sidebarPos == "right")
			gotX, gotY := m.clampToRegion(tc.x, tc.y, tc.region)
			if gotX != tc.wantX || gotY != tc.wantY {
				t.Errorf("clampToRegion(%d, %d, %v) = (%d, %d); want (%d, %d)",
					tc.x, tc.y, tc.region, gotX, gotY, tc.wantX, tc.wantY)
			}
		})
	}
}

// TestClampToRegionScrollbar verifies that the viewport right-edge clamp
// always excludes 3 columns on the right (matching the combined pane padding
// and scrollbar column), regardless of whether a scrollbar is visible.
func TestClampToRegionScrollbar(t *testing.T) {
	t.Parallel()
	m := buildTestModel(100, 30, false, false)
	gotX, _ := m.clampToRegion(99, 15, regionViewport)
	if gotX != 96 {
		t.Errorf("without scrollbar: clampToRegion x = %d; want 96", gotX)
	}

	lines := make([]string, m.viewport.Height()+10)
	for i := range lines {
		lines[i] = "line"
	}
	m.viewport.SetLines(lines)
	gotX, _ = m.clampToRegion(99, 15, regionViewport)
	if gotX != 96 {
		t.Errorf("with scrollbar: clampToRegion x = %d; want 96", gotX)
	}
}

// ---------------------------------------------------------------------------
// Content-anchored viewport selection helpers
// ---------------------------------------------------------------------------

func TestContentLineAtScreenY(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		contentTopPad int
		yOffset       int
		y             int
		want          int
	}{
		{"zero offsets", 0, 0, 5, 4},
		{"top pad only", 2, 0, 5, 2},
		{"scroll only", 0, 3, 5, 7},
		{"top pad and scroll", 2, 3, 5, 5},
		{"first content row", 0, 0, 1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := buildTestModel(100, 30, false, false)
			m.viewport.SetHeight(5)
			m.setViewportContent(strings.Repeat("\n", 19))
			m.contentTopPad = tc.contentTopPad
			m.viewport.SetYOffset(tc.yOffset)
			if got := m.contentLineAtScreenY(tc.y); got != tc.want {
				t.Errorf("contentLineAtScreenY(%d) = %d; want %d", tc.y, got, tc.want)
			}
		})
	}
}

func TestScreenYAtContentLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		contentTopPad int
		yOffset       int
		line          int
		want          int
	}{
		{"zero offsets", 0, 0, 4, 5},
		{"top pad only", 2, 0, 2, 5},
		{"scroll only", 0, 3, 7, 5},
		{"top pad and scroll", 2, 3, 5, 5},
		{"first content line", 0, 0, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := buildTestModel(100, 30, false, false)
			m.viewport.SetHeight(5)
			m.setViewportContent(strings.Repeat("\n", 19))
			m.contentTopPad = tc.contentTopPad
			m.viewport.SetYOffset(tc.yOffset)
			if got := m.screenYAtContentLine(tc.line); got != tc.want {
				t.Errorf("screenYAtContentLine(%d) = %d; want %d", tc.line, got, tc.want)
			}
			// Round-trip: screen -> content -> screen must be the identity.
			if got := m.screenYAtContentLine(m.contentLineAtScreenY(tc.want)); got != tc.want {
				t.Errorf("round trip through screen y %d = %d", tc.want, got)
			}
		})
	}
}

func TestScreenSelectionProjection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		contentTopPad int
		yOffset       int
		sidebarLeft   bool
		selection     selectionState
		wantStart     selectionPoint
		wantEnd       selectionPoint
		wantClear     bool
	}{
		{
			name:          "no sidebar, no offsets, end scrolled below viewport",
			contentTopPad: 0,
			yOffset:       0,
			selection:     selectionState{start: selectionPoint{2, 4}, end: selectionPoint{5, 9}, active: true},
			wantStart:     selectionPoint{3, 7},
			wantEnd:       selectionPoint{5, 97}, // viewport bottom row (1+5-1), full visible width
		},
		{
			name:          "left sidebar and scroll, end below viewport",
			contentTopPad: 2,
			yOffset:       3,
			sidebarLeft:   true,
			selection:     selectionState{start: selectionPoint{4, 10}, end: selectionPoint{6, 20}, active: true},
			wantStart:     selectionPoint{4, 50},
			wantEnd:       selectionPoint{5, 97}, // viewport bottom row, full visible width (sidebar shifts left only)
		},
		{
			name:          "fully below viewport clears highlight",
			contentTopPad: 0,
			yOffset:       10,
			selection:     selectionState{start: selectionPoint{16, 0}, end: selectionPoint{18, 5}, active: true},
			wantClear:     true,
		},
		{
			name:          "span crossing top edge starts at top row with left column",
			contentTopPad: 0,
			yOffset:       2,
			selection:     selectionState{start: selectionPoint{0, 2}, end: selectionPoint{2, 8}, active: true},
			wantStart:     selectionPoint{1, 3}, // viewport top row, region left bound
			wantEnd:       selectionPoint{1, 11},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := buildTestModel(100, 30, tc.sidebarLeft, false)
			m.viewport.SetHeight(5)
			m.setViewportContent(strings.Repeat("\n", 19))
			m.contentTopPad = tc.contentTopPad
			m.viewport.SetYOffset(tc.yOffset)
			m.activeRegion = regionViewport
			m.selection = tc.selection
			got := m.screenSelection()
			if tc.wantClear {
				if got.hasSelection() {
					t.Errorf("screenSelection() = (%v)-(%v); want cleared selection", got.start, got.end)
				}
				return
			}
			if got.start != tc.wantStart || got.end != tc.wantEnd {
				t.Errorf("screenSelection() = (%v)-(%v); want (%v)-(%v)", got.start, got.end, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

func TestExtractViewportText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		lines         []string
		contentTopPad int
		state         selectionState
		want          string
	}{
		{
			name:  "empty selection returns empty string",
			lines: []string{"hello world"},
			state: selectionState{},
			want:  "",
		},
		{
			name:  "single line partial",
			lines: []string{"hello world"},
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 5}},
			want:  "hello",
		},
		{
			name:  "multi-line",
			lines: []string{"hello world", "foo bar baz"},
			state: selectionState{start: selectionPoint{0, 6}, end: selectionPoint{1, 3}},
			want:  "world\nfoo",
		},
		{
			name:          "scrolled window with top pad",
			lines:         []string{"", "", "line A", "line B"},
			contentTopPad: 2,
			state:         selectionState{start: selectionPoint{0, 0}, end: selectionPoint{1, 6}},
			want:          "line A\nline B",
		},
		{
			name:  "partial first and last line",
			lines: []string{"alpha", "beta", "gamma", "delta"},
			state: selectionState{start: selectionPoint{0, 1}, end: selectionPoint{3, 3}},
			want:  "lpha\nbeta\ngamma\ndel",
		},
		{
			name:  "box-drawing borders stripped",
			lines: []string{"│ content │"},
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 11}},
			want:  "content",
		},
		{
			name:  "multi-line tool frame borders stripped",
			lines: []string{"│ line one │", "╰─ line two ─╯"},
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{1, 15}},
			want:  "line one\nline two",
		},
		{
			name:  "ANSI sequences stripped",
			lines: []string{"\x1b[31mred text\x1b[0m"},
			state: selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 8}},
			want:  "red text",
		},
		{
			name:  "out-of-range lines skipped",
			lines: []string{"only line"},
			state: selectionState{start: selectionPoint{-1, 0}, end: selectionPoint{3, 9}},
			want:  "only line",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := buildTestModel(100, 30, false, false)
			m.setViewportContent(strings.Join(tc.lines, "\n"))
			m.contentTopPad = tc.contentTopPad
			m.activeRegion = regionViewport
			m.selection = tc.state
			if got := m.extractViewportText(); got != tc.want {
				t.Errorf("extractViewportText() = %q; want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestMatchRow
// ---------------------------------------------------------------------------

func TestMatchRow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		lines   []string
		rowText string
		oldRow  int
		wantRow int
		wantOK  bool
	}{
		{
			name:    "exact row preserved",
			lines:   []string{"a", "b", "c"},
			rowText: "b",
			oldRow:  1,
			wantRow: 1,
			wantOK:  true,
		},
		{
			name:    "nearest match above old row",
			lines:   []string{"a", "b", "c"},
			rowText: "a",
			oldRow:  2,
			wantRow: 0,
			wantOK:  true,
		},
		{
			name:    "nearest match below old row",
			lines:   []string{"a", "b", "c"},
			rowText: "c",
			oldRow:  0,
			wantRow: 2,
			wantOK:  true,
		},
		{
			name:    "ansi sequences stripped before matching",
			lines:   []string{"\x1b[32mgreen\x1b[0m", "x"},
			rowText: "green",
			oldRow:  1,
			wantRow: 0,
			wantOK:  true,
		},
		{
			name:    "old row dropped",
			lines:   []string{"a", "b", "c"},
			rowText: "c",
			oldRow:  5,
			wantRow: 2,
			wantOK:  true,
		},
		{
			name:    "no match",
			lines:   []string{"a", "b"},
			rowText: "z",
			oldRow:  0,
			wantRow: 0,
			wantOK:  false,
		},
		{
			name:    "equidistant matches ambiguous",
			lines:   []string{"a", "b", "a"},
			rowText: "a",
			oldRow:  1,
			wantRow: 0,
			wantOK:  false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotRow, gotOK := matchRow(tc.lines, tc.rowText, tc.oldRow)
			if gotRow != tc.wantRow || gotOK != tc.wantOK {
				t.Errorf("matchRow(%q, %q, %d) = (%d, %v); want (%d, %v)",
					tc.lines, tc.rowText, tc.oldRow, gotRow, gotOK, tc.wantRow, tc.wantOK)
			}
		})
	}
}
