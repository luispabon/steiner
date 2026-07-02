package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
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
			useTrueColor(t)
			result := applyScreenHighlight(tc.frame, tc.state, testSelStyle)
			lines := strings.Split(result, "\n")
			tc.checkFn(t, lines)
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
			wantY:      24,
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
