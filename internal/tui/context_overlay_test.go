package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/output"
)

func TestContextOverlayRendersMarkdownAndKeepsBaseVisible(t *testing.T) {
	m := newModel(Config{Model: "gpt-test"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	baseLines := make([]string, m.viewport.Height)
	baseLines[0] = "base top"
	baseLines[len(baseLines)-1] = "base bottom"
	for i := 1; i < len(baseLines)-1; i++ {
		baseLines[i] = "base filler"
	}
	m.viewport.SetContent(strings.Join(baseLines, "\n"))
	base := m.View()

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

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextReportEvent(report)})

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
	titleStyle := lipgloss.NewStyle().
		Foreground(m.styles.AccentColor).
		Bold(true).
		Width(m.contextOverlay.WithDimensions(m.width, m.height).InnerWidth())
	if !strings.Contains(m.renderContextOverlay(), titleStyle.Render("Context Report")) {
		t.Fatalf("overlay view = %q, want accent-colored Context Report title", stripANSI(m.renderContextOverlay()))
	}

	composed := composeCenteredOverlay(base, m.renderContextOverlay(), m.width, m.height)
	if !strings.Contains(composed, "base filler") {
		t.Fatalf("composed view = %q, want transcript content visible outside overlay", composed)
	}
	if !strings.Contains(base, "model gpt-test") {
		t.Fatalf("base view = %q, want sidebar content in the underlying screen", base)
	}
}

func TestContextOverlayRendersConfigYAMLWithSyntaxHighlighting(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 24})

	report := "```yaml\nmodel:\n  base_url: http://localhost:11434/v1\n  context_size: 8192\n```"
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewConfigReportEvent(report)})

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
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Single line longer than 100 chars should open the overlay.
	longLine := strings.Repeat("x", 101)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextReportEvent(longLine)})

	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = closed, want open for long single-line context report")
	}
}

func TestContextOverlayOpensForMultiLineContextReport(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Multi-line report should open the overlay.
	report := "line one\nline two"
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextReportEvent(report)})

	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = closed, want open for multi-line context report")
	}
}

func TestContextOverlayClosesOnEsc(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextReportEvent("# Heading\n\nContent with newline.")})
	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = closed, want open after context report")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = open, want closed after Esc")
	}
}

func TestContextOverlayScrollsLongRenderedMarkdown(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 72, Height: 24})

	var lines []string
	lines = append(lines, "# Long Report", "")
	for i := 0; i < 80; i++ {
		lines = append(lines, "- item "+strings.Repeat("x", 8)+" "+strings.Repeat("y", 8))
	}
	report := strings.Join(lines, "\n")

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextReportEvent(report)})

	if got := m.contextOverlay.lineCount; got <= contextOverlayMaxLines {
		t.Fatalf("lineCount = %d, want more than %d", got, contextOverlayMaxLines)
	}
	if got := m.contextOverlay.scrollOffset; got != 0 {
		t.Fatalf("scrollOffset = %d, want 0 at open", got)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.contextOverlay.scrollOffset; got != 1 {
		t.Fatalf("scrollOffset after key down = %d, want 1", got)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if got := m.contextOverlay.scrollOffset; got != 31 {
		t.Fatalf("scrollOffset after page down = %d, want 31", got)
	}
	if maxOffset := m.contextOverlay.lineCount - contextOverlayMaxLines; maxOffset >= 0 && m.contextOverlay.scrollOffset > maxOffset {
		t.Fatalf("scrollOffset after page down = %d, want at most %d", m.contextOverlay.scrollOffset, maxOffset)
	}
}

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiEscapePattern.ReplaceAllString(s, "")
}
