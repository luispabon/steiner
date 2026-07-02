package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// testSelStyle is used as the highlight style in applyScreenHighlight tests.
var testSelStyle = lipgloss.NewStyle().Background(lipgloss.Color("#3a4a5a"))

// ---------------------------------------------------------------------------
// TestExtractText
// ---------------------------------------------------------------------------

func TestExtractText(t *testing.T) {
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotStart, gotEnd := wordBoundsAt(tc.line, tc.col)
			if gotStart != tc.wantStartCol || gotEnd != tc.wantEndCol {
				t.Errorf("wordBoundsAt(%q, %d) = (%d, %d); want (%d, %d)",
					tc.line, tc.col, gotStart, gotEnd, tc.wantStartCol, tc.wantEndCol)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestLineBoundsAt
// ---------------------------------------------------------------------------

func TestLineBoundsAt(t *testing.T) {
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
			gotStart, gotEnd := lineBoundsAt(tc.lines, tc.lineIdx)
			if gotStart != tc.wantStartCol || gotEnd != tc.wantEndCol {
				t.Errorf("lineBoundsAt(lines, %d) = (%d, %d); want (%d, %d)",
					tc.lineIdx, gotStart, gotEnd, tc.wantStartCol, tc.wantEndCol)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestDetectRegion
// ---------------------------------------------------------------------------

// buildTestModel creates a minimal Model for region detection tests.
func buildTestModel(width, height int, sidebarVisible, sidebarRight bool) Model {
	vp := viewport.New()
	vp.SetWidth(width - 6)
	vp.SetHeight(max(1, height-5))

	m := Model{
		width:    width,
		height:   height,
		viewport: vp,
		sidebar:  sidebarState{expanded: sidebarVisible},
		input:    textarea.New(),
	}
	if sidebarRight {
		m.sidebarPosition = "right"
	}
	return m
}

func TestDetectRegion(t *testing.T) {
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
			m := buildTestModel(100, 30, false, false)
			var gotCounts []int

			for i, click := range tc.clicks {
				if i > 0 {
					time.Sleep(click.delay)
				}

				clickPos := selectionPoint{line: click.y, col: click.x}
				clickTime := time.Now()

				timeDiff := clickTime.Sub(m.lastClickTime)
				const clickTimeout = 500 * time.Millisecond
				isWithinTime := timeDiff <= clickTimeout && timeDiff >= 0

				isWithinDelta := false
				if isWithinTime {
					colDiff := m.lastClickPos.col - clickPos.col
					lineDiff := m.lastClickPos.line - clickPos.line
					if colDiff < 0 {
						colDiff = -colDiff
					}
					if lineDiff < 0 {
						lineDiff = -lineDiff
					}
					isWithinDelta = colDiff <= 1 && lineDiff <= 1
				}

				if isWithinTime && isWithinDelta {
					m.clickCount++
					if m.clickCount > 3 {
						m.clickCount = 1
					}
				} else {
					m.clickCount = 1
				}

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
// accounts for the narrower (2-column) padding ApplyPanePadding uses when a
// scrollbar is present, versus the 3-column padding used otherwise.
func TestClampToRegionScrollbar(t *testing.T) {
	m := buildTestModel(100, 30, false, false)
	if m.hasScrollbar() {
		t.Fatalf("expected no scrollbar before content overflow")
	}
	gotX, _ := m.clampToRegion(99, 15, regionViewport)
	if gotX != 96 {
		t.Errorf("without scrollbar: clampToRegion x = %d; want 96", gotX)
	}

	lines := make([]string, m.viewport.Height()+10)
	for i := range lines {
		lines[i] = "line"
	}
	m.viewport.SetContentLines(lines)
	if !m.hasScrollbar() {
		t.Fatalf("expected scrollbar after content overflow")
	}
	gotX, _ = m.clampToRegion(99, 15, regionViewport)
	if gotX != 97 {
		t.Errorf("with scrollbar: clampToRegion x = %d; want 97", gotX)
	}
}
