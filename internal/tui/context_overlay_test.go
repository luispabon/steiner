package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/output"
)

func TestContextOverlayRendersMarkdownAndKeepsBaseVisible(t *testing.T) {
	t.Parallel()
	useTrueColor(t)
	m := newModel(Config{Model: "gpt-test"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	baseLines := make([]string, m.viewport.Height())
	baseLines[0] = "base top"
	baseLines[len(baseLines)-1] = "base bottom"
	for i := 1; i < len(baseLines)-1; i++ {
		baseLines[i] = "base filler"
	}
	m.setViewportLines(baseLines)
	base := m.View().Content

	report := strings.Join([]string{
		"# Heading",
		"",
		"- item one",
		"- item two",
		"",
		"```go",
		"fmt.Println(\"hello\")",
		"```",
		"",
		"Use `inline code` here.",
	}, "\n")

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", report)})

	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = closed, want open after context report")
	}
	if got := m.contextOverlay.title; got != "Context Report" {
		t.Fatalf("context overlay title = %q, want Context Report", got)
	}
	rendered := stripANSI(strings.Join(m.contextOverlay.renderedLines, "\n"))
	for _, want := range []string{"Heading", "item one", "item two", "fmt.Println", "inline code"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered markdown = %q, want %q", rendered, want)
		}
	}
	if !strings.Contains(strings.Join(m.contextOverlay.renderedLines, "\n"), "\x1b[") {
		t.Fatalf("rendered markdown = %q, want styled ANSI output", rendered)
	}
	renderedOverlay := stripANSI(m.renderContextOverlay())
	lines := strings.Split(renderedOverlay, "\n")
	if len(lines) < 3 || !strings.Contains(lines[2], "Context Report") {
		t.Fatalf("overlay view = %q, want Context Report title on the title line", renderedOverlay)
	}

	composed := stripANSI(composeCenteredOverlay(base, m.renderContextOverlay(), m.width, m.height))
	if !strings.Contains(composed, "base top") || !strings.Contains(composed, "gpt-test") {
		t.Fatalf("composed view = %q, want transcript content and sidebar visible outside overlay", composed)
	}
	if !strings.Contains(stripANSI(base), "gpt-test") {
		t.Fatalf("base view = %q, want sidebar content in the underlying screen", base)
	}
}

func TestContextOverlayRendersConfigYAMLWithSyntaxHighlighting(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 24})

	report := "```yaml\nmodel:\n  base_url: http://localhost:11434/v1\n  context_size: 8192\n```"
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Config", report)})

	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = closed, want open after config report")
	}
	if got := m.contextOverlay.title; got != "Config" {
		t.Fatalf("context overlay title = %q, want Config", got)
	}
	rendered := strings.Join(m.contextOverlay.renderedLines, "\n")
	plain := stripANSI(rendered)
	for _, want := range []string{"model:", "base_url:", "context_size:"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered yaml = %q, want %q", plain, want)
		}
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("rendered yaml = %q, want styled ANSI output", plain)
	}
	if !strings.Contains(stripANSI(m.renderContextOverlay()), "Config") {
		t.Fatalf("overlay view = %q, want Config header", stripANSI(m.renderContextOverlay()))
	}
}

func TestContextOverlayOpensForLongContextReport(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Single line longer than 100 chars should open the overlay.
	longLine := strings.Repeat("x", 101)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", longLine)})

	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = closed, want open for long single-line context report")
	}
}

func TestContextOverlayOpensForMultiLineContextReport(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Multi-line report should open the overlay.
	report := "line one\nline two"
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", report)})

	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = closed, want open for multi-line context report")
	}
}

func TestContextOverlayClosesOnEsc(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", "# Heading\n\nContent with newline.")})
	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = closed, want open after context report")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = open, want closed after Esc")
	}
}

func TestContextOverlayScrollsLongRenderedMarkdown(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 72, Height: 24})

	var lines []string
	lines = append(lines, "# Long Report", "")
	for i := 0; i < 80; i++ {
		lines = append(lines, "- item "+strings.Repeat("x", 8)+" "+strings.Repeat("y", 8))
	}
	report := strings.Join(lines, "\n")

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", report)})

	if got := m.contextOverlay.lineCount; got <= m.contextOverlay.visibleHeight() {
		t.Fatalf("lineCount = %d, want more than %d", got, m.contextOverlay.visibleHeight())
	}
	if got := m.contextOverlay.scrollOffset; got != 0 {
		t.Fatalf("scrollOffset = %d, want 0 at open", got)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.contextOverlay.scrollOffset; got != 1 {
		t.Fatalf("scrollOffset after key down = %d, want 1", got)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	wantOffset := 1 + m.contextOverlay.visibleHeight()
	if got := m.contextOverlay.scrollOffset; got != wantOffset {
		t.Fatalf("scrollOffset after page down = %d, want %d", got, wantOffset)
	}
	if maxOffset := m.contextOverlay.maxScrollOffset(); m.contextOverlay.scrollOffset > maxOffset {
		t.Fatalf("scrollOffset after page down = %d, want at most %d", m.contextOverlay.scrollOffset, maxOffset)
	}
}

func TestContextOverlayScrollToEndSetsLastPage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		lineCount int
		want      int
	}{
		{name: "empty", lineCount: 0, want: 0},
		{name: "fits in one page", lineCount: 16, want: 0},
		{name: "one line over a page", lineCount: 17, want: 1},
		{name: "long report", lineCount: 80, want: 64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := contextOverlayState{OverlayShell: OverlayShell{height: 24}, lineCount: tt.lineCount}.scrollToEnd()
			if s.scrollOffset != tt.want {
				t.Fatalf("scrollOffset = %d, want %d", s.scrollOffset, tt.want)
			}
		})
	}
}

// longContextReport returns a report with more lines than fit in one overlay
// page. "Top marker" is the first line and "line 80" is the last.
func longContextReport() string {
	lines := []string{"Top marker", ""}
	for i := 1; i <= 80; i++ {
		lines = append(lines, fmt.Sprintf("- line %02d", i))
	}
	return strings.Join(lines, "\n")
}

func TestContextOverlayReportOpenPosition(t *testing.T) {
	t.Parallel()
	report := longContextReport()
	tests := []struct {
		name         string
		event        output.Event
		wantLastPage bool
	}{
		{name: "scroll to end opens last page", event: output.NewOverlayReportEventAtEnd("History", report), wantLastPage: true},
		{name: "default opens top", event: output.NewOverlayReportEvent("History", report), wantLastPage: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
			m = updateModel(t, m, runtimeEventMsg{Event: tt.event})
			if !m.contextOverlay.IsOpen() {
				t.Fatal("context overlay = closed, want open after report event")
			}
			if m.contextOverlay.lineCount <= m.contextOverlay.visibleHeight() {
				t.Fatalf("lineCount = %d, want more than %d", m.contextOverlay.lineCount, m.contextOverlay.visibleHeight())
			}
			wantOffset := 0
			if tt.wantLastPage {
				wantOffset = m.contextOverlay.maxScrollOffset()
			}
			if got := m.contextOverlay.scrollOffset; got != wantOffset {
				t.Fatalf("scrollOffset = %d, want %d", got, wantOffset)
			}
			plain := stripANSI(m.View().Content)
			if got := strings.Contains(plain, "line 80"); got != tt.wantLastPage {
				t.Fatalf("last line visible = %v, want %v; overlay = %q", got, tt.wantLastPage, plain)
			}
			if got := strings.Contains(plain, "Top marker"); got == tt.wantLastPage {
				t.Fatalf("top line visible = %v, want %v; overlay = %q", got, !tt.wantLastPage, plain)
			}
		})
	}
}

// composedContextView returns the stripped frame at the model's current size.
func composedContextView(m *Model) string {
	return stripANSI(m.View().Content)
}

func TestContextOverlayFitsShortTerminal(t *testing.T) {
	t.Parallel()
	m := newModel(Config{Model: "gpt-test"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", longContextReport())})

	if _, h := measureOverlay(m.renderContextOverlay()); h != m.height {
		t.Fatalf("overlay height = %d, want %d (16 body rows plus frame)", h, m.height)
	}
	view := composedContextView(m)
	for _, want := range []string{"╭", "╰", "esc"} {
		if !strings.Contains(view, want) {
			t.Fatalf("composed view = %q, want %q so the whole frame is visible", view, want)
		}
	}
}

func TestContextOverlayVisibleHeightFollowsTerminal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		height int
		want   int
	}{
		{height: 8, want: 3},
		{height: 24, want: 16},
		{height: 60, want: 30},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("height %d", tt.height), func(t *testing.T) {
			t.Parallel()
			s := contextOverlayState{OverlayShell: OverlayShell{height: tt.height}}
			if got := s.visibleHeight(); got != tt.want {
				t.Fatalf("visibleHeight() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestContextOverlayPageKeysReachEnds(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", longContextReport())})

	// Sixteen-row pages over the 82-line report: five PgDown presses reach the last page.
	for range 5 {
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	if view := composedContextView(m); !strings.Contains(view, "line 80") || strings.Contains(view, "Top marker") {
		t.Fatalf("after PgDown, composed view = %q, want last line visible and top line hidden", view)
	}
	for range 5 {
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	}
	if view := composedContextView(m); !strings.Contains(view, "Top marker") || strings.Contains(view, "line 80") {
		t.Fatalf("after PgUp, composed view = %q, want top line visible and last line hidden", view)
	}
}

func TestContextOverlayResizeKeepsPosition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		event  output.Event
		height int
		atEnd  bool
	}{
		{name: "end stays at end when growing", event: output.NewOverlayReportEventAtEnd("History", longContextReport()), height: 40, atEnd: true},
		{name: "end stays at end when shrinking", event: output.NewOverlayReportEventAtEnd("History", longContextReport()), height: 16, atEnd: true},
		{name: "top stays at top when growing", event: output.NewOverlayReportEvent("History", longContextReport()), height: 40, atEnd: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
			m = updateModel(t, m, runtimeEventMsg{Event: tt.event})
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: tt.height})

			view := composedContextView(m)
			if tt.atEnd {
				if got, want := m.contextOverlay.scrollOffset, m.contextOverlay.maxScrollOffset(); got != want {
					t.Fatalf("scrollOffset = %d, want last page %d", got, want)
				}
				if !strings.Contains(view, "line 80") {
					t.Fatalf("composed view = %q, want last line visible", view)
				}
				return
			}
			if got := m.contextOverlay.scrollOffset; got != 0 {
				t.Fatalf("scrollOffset = %d, want 0", got)
			}
			if !strings.Contains(view, "Top marker") {
				t.Fatalf("composed view = %q, want top line visible", view)
			}
		})
	}
}

// shortContextReport returns a report that fits on one page at 24 rows.
func shortContextReport() string {
	lines := []string{"Top marker", ""}
	for i := 1; i <= 9; i++ {
		lines = append(lines, fmt.Sprintf("- item %02d", i))
	}
	return strings.Join(lines, "\n")
}

// TestContextOverlayFitThenShrinkStaysTop guards against a report that fit on
// one page being treated as end-anchored: shrinking the terminal so it
// overflows must keep the top of the report visible.
func TestContextOverlayFitThenShrinkStaysTop(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", shortContextReport())})
	if got := m.contextOverlay.scrollOffset; got != 0 {
		t.Fatalf("scrollOffset before shrink = %d, want 0", got)
	}

	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})

	if got := m.contextOverlay.scrollOffset; got != 0 {
		t.Fatalf("scrollOffset after shrink = %d, want 0", got)
	}
	view := composedContextView(m)
	if !strings.Contains(view, "Top marker") || strings.Contains(view, "item 09") {
		t.Fatalf("composed view = %q, want top of report visible after shrink", view)
	}
}

func stripANSI(s string) string {
	return ansi.Strip(s)
}
