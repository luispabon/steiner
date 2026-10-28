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

	if got := m.contextOverlay.lineCount; got <= contextOverlayMaxLines {
		t.Fatalf("lineCount = %d, want more than %d", got, contextOverlayMaxLines)
	}
	if got := m.contextOverlay.scrollOffset; got != 0 {
		t.Fatalf("scrollOffset = %d, want 0 at open", got)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.contextOverlay.scrollOffset; got != 1 {
		t.Fatalf("scrollOffset after key down = %d, want 1", got)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if got := m.contextOverlay.scrollOffset; got != 31 {
		t.Fatalf("scrollOffset after page down = %d, want 31", got)
	}
	if maxOffset := m.contextOverlay.lineCount - contextOverlayMaxLines; maxOffset >= 0 && m.contextOverlay.scrollOffset > maxOffset {
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
		{name: "fits in one page", lineCount: contextOverlayMaxLines, want: 0},
		{name: "one line over a page", lineCount: contextOverlayMaxLines + 1, want: 1},
		{name: "long report", lineCount: 80, want: 80 - contextOverlayMaxLines},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := contextOverlayState{lineCount: tt.lineCount}.scrollToEnd()
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
			if m.contextOverlay.lineCount <= contextOverlayMaxLines {
				t.Fatalf("lineCount = %d, want more than %d", m.contextOverlay.lineCount, contextOverlayMaxLines)
			}
			wantOffset := 0
			if tt.wantLastPage {
				wantOffset = m.contextOverlay.lineCount - contextOverlayMaxLines
			}
			if got := m.contextOverlay.scrollOffset; got != wantOffset {
				t.Fatalf("scrollOffset = %d, want %d", got, wantOffset)
			}
			plain := stripANSI(m.renderContextOverlay())
			if got := strings.Contains(plain, "line 80"); got != tt.wantLastPage {
				t.Fatalf("last line visible = %v, want %v; overlay = %q", got, tt.wantLastPage, plain)
			}
			if got := strings.Contains(plain, "Top marker"); got == tt.wantLastPage {
				t.Fatalf("top line visible = %v, want %v; overlay = %q", got, !tt.wantLastPage, plain)
			}
		})
	}
}

func stripANSI(s string) string {
	return ansi.Strip(s)
}
