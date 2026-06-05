package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func (b *contentBuffer) renderAssistantMarkdownSegment(segment contentSegment, width int) string {
	rendered := b.renderMarkdown(segment.text, true, width)
	if strings.TrimSpace(rendered) != "" {
		return strings.TrimRight(rendered, "\n") + "\n\n"
	}
	return b.styles.AssistantProse.Render(segment.text) + "\n\n"
}

func (b *contentBuffer) renderAssistantProseSegment(segment contentSegment) string {
	return b.styles.AssistantProse.Render(segment.text) + "\n\n"
}

func (b *contentBuffer) renderUserSegment(segment contentSegment, width int) string {
	lines := strings.Split(strings.TrimRight(segment.text, "\n"), "\n")
	contentWidth := width - 1
	if contentWidth < 2 {
		contentWidth = 2
	}
	bar := b.styles.UserBar.Render("┃")
	pad := bar + b.styles.UserBg.Width(contentWidth).Render("")
	var sb strings.Builder
	sb.WriteString(pad + "\n")
	textWidth := contentWidth - 3
	if textWidth < 1 {
		textWidth = 1
	}
	isFirstLine := true
	for _, line := range lines {
		wrapped := lipgloss.NewStyle().Width(textWidth).Render(line)
		wrappedLines := strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
		for wi, vl := range wrappedLines {
			vl = strings.TrimRight(vl, " ")
			if isFirstLine && wi == 0 {
				if cmdPrefix, ok := matchCommandPrefix(segment.text, b.skillNames); ok && strings.HasPrefix(vl, cmdPrefix) {
					prefixStyle := lipgloss.NewStyle().
						Bold(true).
						Foreground(b.styles.AccentColor).
						Background(b.styles.UserBg.GetBackground())
					prefixText := "  " + cmdPrefix
					prefix := stripTrailingReset(prefixStyle.Render(prefixText))
					restText := vl[len(cmdPrefix):]
					restWidth := contentWidth - len([]rune(prefixText))
					if restWidth < 1 {
						restWidth = 1
					}
					rest := stripTrailingReset(b.styles.UserBg.Width(restWidth).Render(restText))
					content := prefix + rest + "\x1b[0m"
					sb.WriteString(bar + content + "\n")
					continue
				}
			}
			content := b.styles.UserBg.Width(contentWidth).Render("  " + vl)
			sb.WriteString(bar + content + "\n")
		}
		isFirstLine = false
	}
	sb.WriteString(pad + "\n")
	sb.WriteString("\n")
	return sb.String()
}

// renderPendingSteerSegment renders a queued steering message: dashed muted bar,
// "queued: " prefix, italic dim text.
func (b *contentBuffer) renderPendingSteerSegment(segment contentSegment, width int) string {
	contentWidth := width - 1
	if contentWidth < 2 {
		contentWidth = 2
	}

	bar := b.styles.FgMute.Render("┇")
	prefix := b.styles.FgDim.Render("queued: ")
	prefixW := lipgloss.Width(prefix)

	italicDim := lipgloss.NewStyle().Italic(true).Foreground(b.styles.FgDim.GetForeground())

	// First line gets the "queued: " prefix; continuation lines indent to match.
	indent := strings.Repeat(" ", prefixW)
	firstLineW := contentWidth - 2 - prefixW
	if firstLineW < 1 {
		firstLineW = 1
	}
	contLineW := contentWidth - 2 - prefixW
	if contLineW < 1 {
		contLineW = 1
	}

	lines := strings.Split(strings.TrimRight(segment.text, "\n"), "\n")

	var sb strings.Builder
	first := true
	for _, line := range lines {
		w := contLineW
		pfx := indent
		if first {
			w = firstLineW
			pfx = prefix
			first = false
		}
		wrapped := lipgloss.NewStyle().Width(w).Render(line)
		for _, vl := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
			vl = strings.TrimRight(vl, " ")
			sb.WriteString(bar + "  " + pfx + italicDim.Render(vl) + "\n")
			pfx = indent
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

// renderUserMarkdownSegment renders a markdown-like user prompt with glamour
// while keeping the left-bar framing so user messages remain visually distinct
// from assistant output.
func (b *contentBuffer) renderUserMarkdownSegment(segment contentSegment, width int) string {
	if _, ok := matchCommandPrefix(segment.text, b.skillNames); ok {
		return b.renderUserSegment(segment, width)
	}
	contentWidth := width - 1
	if contentWidth < 2 {
		contentWidth = 2
	}

	userBgHex := lipgloss.Color(theme.UserSoft)
	barStyle := lipgloss.NewStyle().
		Foreground(b.styles.UserBar.GetForeground()).
		Background(userBgHex)
	bar := barStyle.Render("┃")
	padStyle := lipgloss.NewStyle().
		Width(contentWidth + 1).
		Background(userBgHex)
	pad := padStyle.Render(barStyle.Render("┃"))

	rendered, err := renderMarkdownBlock(segment.text, contentWidth-2, b.styles, b.glamourStyleSheet, &b.renderer, &b.renderWidth)
	if err != nil {
		b.lastRenderErr = fmt.Errorf("render user markdown: %w", err)
		return b.renderUserSegment(segment, width)
	}
	rendered = strings.TrimRight(rendered, "\n")
	rendered = strings.TrimLeft(rendered, "\n")

	var sb strings.Builder
	sb.WriteString(pad + "\n")
	for _, line := range strings.Split(rendered, "\n") {
		if line == "" {
			continue
		}
		wrapped := theme.WithBg(line, userBgHex)
		padded := theme.PadLines(wrapped, contentWidth, userBgHex)
		sb.WriteString(bar + padded + "\n")
	}
	sb.WriteString(pad + "\n")
	sb.WriteString("\n")
	return sb.String()
}

func (b *contentBuffer) renderThinkingBlockSegment(segment contentSegment, width int) string {
	if segment.thinkData == nil {
		return ""
	}
	td := segment.thinkData
	style := b.thinkingTextStyle()
	bar := style.Render("▎")
	contentWidth := max(1, width-2)

	if td.collapsed {
		// Derive 3-line preview from body at render time.
		allLines := []string{}
		for _, line := range strings.Split(strings.TrimRight(td.body, "\n"), "\n") {
			wrapped := ansi.Hardwrap(ansi.Wordwrap(line, contentWidth, ""), contentWidth, true)
			for _, wl := range strings.Split(wrapped, "\n") {
				allLines = append(allLines, wl)
			}
		}
		truncated := len(allLines) > 3
		if truncated {
			allLines = allLines[:3]
		}
		var sb strings.Builder
		sb.WriteString(style.Render("▸ Thinking") + "\n")
		for _, wl := range allLines {
			sb.WriteString(bar + " " + style.Render(wl) + "\n")
		}
		if truncated {
			sb.WriteString(bar + " " + style.Render("…") + "\n")
		}
		return sb.String()
	}

	// Expanded state — wrap body lines.
	var sb strings.Builder
	sb.WriteString(style.Render("▾ Thinking") + "\n")
	for _, line := range strings.Split(strings.TrimRight(td.body, "\n"), "\n") {
		wrapped := ansi.Hardwrap(ansi.Wordwrap(line, contentWidth, ""), contentWidth, true)
		for _, wl := range strings.Split(wrapped, "\n") {
			sb.WriteString(bar + " " + style.Render(wl) + "\n")
		}
	}
	return sb.String()
}

func (b *contentBuffer) thinkingTextStyle() lipgloss.Style {
	return b.styles.FgDim.Italic(true)
}

func (b *contentBuffer) renderMarkdown(block string, isAssistant bool, width int) string {
	label := "user"
	if isAssistant {
		label = "assistant"
	}
	rendered, err := renderMarkdownBlock(block, width, b.styles, b.glamourStyleSheet, &b.renderer, &b.renderWidth)
	if err != nil {
		b.lastRenderErr = fmt.Errorf("render markdown: %w", err)
		return b.styles.UserBg.Render(label + "> " + block)
	}
	return theme.WithBg(rendered, lipgloss.Color(theme.BgElev))
}

func renderMarkdownBlock(block string, width int, styles theme.Styles, styleSheet glamour.TermRendererOption, renderer **glamour.TermRenderer, renderWidth *int) (string, error) {
	targetWidth := max(1, width-markdownRenderPadding)
	if renderer != nil && *renderer != nil && renderWidth != nil && *renderWidth == targetWidth {
		rendered, err := (*renderer).Render(block)
		if err != nil {
			return styles.AssistantProse.Render("assistant> " + block), err
		}
		return rendered, nil
	}
	opts := []glamour.TermRendererOption{
		glamour.WithWordWrap(targetWidth),
		glamour.WithPreservedNewLines(),
	}
	if styleSheet != nil {
		opts = append([]glamour.TermRendererOption{styleSheet}, opts...)
	} else {
		opts = append([]glamour.TermRendererOption{glamour.WithStandardStyle("dark")}, opts...)
	}
	termRenderer, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		return styles.AssistantProse.Render("assistant> " + block), err
	}
	if renderer != nil {
		*renderer = termRenderer
	}
	if renderWidth != nil {
		*renderWidth = targetWidth
	}
	rendered, err := termRenderer.Render(block)
	if err != nil {
		return styles.AssistantProse.Render("assistant> " + block), err
	}
	return rendered, nil
}
