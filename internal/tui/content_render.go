package tui

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

var previewSyntaxStyle = chromastyles.Get("github-dark")

func (b *contentBuffer) String(width int) string {
	b.segmentHeights = make([]int, len(b.segments))
	parts := make([]string, 0, len(b.segments)+2)
	for i := range b.segments {
		seg := &b.segments[i]
		if seg.kind == segmentThinkingBlock && !b.showThinking {
			b.segmentHeights[i] = 0
			continue
		}
		// Force re-render for animating segments
		if seg.kind == segmentCompactionBanner && seg.compactionData != nil && !seg.compactionData.finished {
			seg.renderDirty = true
		}
		if !seg.renderDirty && seg.cachedRenderWidth == width && seg.cachedRender != "" {
			b.segmentHeights[i] = strings.Count(seg.cachedRender, "\n")
			if seg.cachedRender != "" {
				parts = append(parts, seg.cachedRender)
			}
			continue
		}
		rendered := b.renderSegment(*seg, width)
		seg.cachedRender = rendered
		seg.cachedRenderWidth = width
		seg.renderDirty = false
		b.segmentHeights[i] = strings.Count(rendered, "\n")
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}
	if preview := b.inProgressPreview(width); preview != "" {
		parts = append(parts, preview)
	}

	result := strings.Join(parts, "")
	return b.fillEmptyLines(result, width)
}

func (b *contentBuffer) fillEmptyLines(s string, width int) string {
	if width < 1 {
		width = 1
	}
	bgLine := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).Render(strings.Repeat(" ", width))
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if lipgloss.Width(line) == 0 {
			lines[i] = bgLine
		}
	}
	return strings.Join(lines, "\n")
}

func (b *contentBuffer) renderSegment(segment contentSegment, width int) string {
	switch segment.kind {
	case segmentToolCall:
		return b.renderToolCallSegment(segment, width)
	case segmentAssistantMarkdown:
		return b.renderAssistantMarkdownSegment(segment, width)
	case segmentAssistantProse:
		return b.renderAssistantProseSegment(segment)
	case segmentApproval:
		return b.renderApprovalSegment(segment)
	case segmentTool:
		return b.renderToolSegment(segment)
	case segmentThinking:
		return b.renderThinkingSegment(segment)
	case segmentUser:
		return b.renderUserSegment(segment, width)
	case segmentUserMarkdown:
		return b.renderUserMarkdownSegment(segment, width)
	case segmentThinkingBlock:
		return theme.WithBg(b.renderThinkingBlockSegment(segment), lipgloss.Color(theme.BgElev))
	case segmentApprovalPill:
		return b.renderApprovalPillSegment(segment, width)
	case segmentCompactionBanner:
		return theme.WithBg(b.renderCompactionBannerSegment(segment, width), lipgloss.Color(theme.BgElev))
	case segmentInterrupted:
		return theme.WithBg(b.renderInterruptedSegment(), lipgloss.Color(theme.BgElev))
	default:
		return b.renderDefaultSegment(segment)
	}
}

func (b *contentBuffer) renderToolCallSegment(segment contentSegment, width int) string {
	return b.renderToolCall(segment.toolData, width)
}

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

func (b *contentBuffer) renderApprovalSegment(segment contentSegment) string {
	return b.styles.ApprovalHighlight.Render(segment.text) + "\n"
}

func (b *contentBuffer) renderToolSegment(segment contentSegment) string {
	return b.styles.ToolBlock.Render(segment.text) + "\n"
}

func (b *contentBuffer) renderThinkingSegment(segment contentSegment) string {
	return b.styles.ThinkingBlock.Render(segment.text) + "\n"
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
	for _, line := range lines {
		wrapped := lipgloss.NewStyle().Width(textWidth).Render(line)
		for _, vl := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
			vl = strings.TrimRight(vl, " ")
			content := b.styles.UserBg.Width(contentWidth).Render("  " + vl)
			sb.WriteString(bar + content + "\n")
		}
	}
	sb.WriteString(pad + "\n")
	sb.WriteString("\n")
	return sb.String()
}

// renderUserMarkdownSegment renders a markdown-like user prompt with glamour
// while keeping the left-bar framing so user messages remain visually distinct
// from assistant output.
func (b *contentBuffer) renderUserMarkdownSegment(segment contentSegment, width int) string {
	contentWidth := width - 1
	if contentWidth < 2 {
		contentWidth = 2
	}

	userBgHex := lipgloss.Color(theme.UserSoft)

	// Bar ┃ with UserSoft background so terminal default (black) doesn't
	// show through. Build from a lipgloss style with both foreground (UserBar)
	// and background (UserSoft) set.
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
	// Trim trailing then leading newlines so glamour's paragraph spacing
	// doesn't create empty lines at the start of the bubble.
	rendered = strings.TrimRight(rendered, "\n")
	rendered = strings.TrimLeft(rendered, "\n")

	var sb strings.Builder
	sb.WriteString(pad + "\n")
	for _, line := range strings.Split(rendered, "\n") {
		if line == "" {
			continue
		}
		// Wrap with UserSoft background so glamour's ANSI resets don't
		// kill the background mid-line, then pad to width.
		wrapped := theme.WithBg(line, userBgHex)
		padded := theme.PadLines(wrapped, contentWidth, userBgHex)
		sb.WriteString(bar + padded + "\n")
	}
	sb.WriteString(pad + "\n")
	sb.WriteString("\n")
	return sb.String()
}

func (b *contentBuffer) renderThinkingBlockSegment(segment contentSegment) string {
	if segment.thinkData == nil {
		return ""
	}
	td := segment.thinkData
	style := b.thinkingTextStyle(td.source)
	var result string
	if td.collapsed {
		runes := []rune(td.preview)
		if len(runes) > 60 {
			runes = runes[:60]
		}
		result = style.Render("▸ Thinking · "+string(runes)+"…") + "\n"
	} else {
		var sb strings.Builder
		sb.WriteString(style.Render("▾ Thinking") + "\n")
		bar := style.Render("▎")
		for _, line := range strings.Split(strings.TrimRight(td.body, "\n"), "\n") {
			sb.WriteString(bar + " " + style.Render(line) + "\n")
		}
		result = sb.String()
	}
	return result
}

func (b *contentBuffer) thinkingTextStyle(source output.ChunkSource) lipgloss.Style {
	if source == output.ChunkSourceScaffoldInference {
		return b.styles.ThinkingBar
	}
	return b.styles.FgDim.Italic(true)
}

func (b *contentBuffer) renderApprovalPillSegment(segment contentSegment, width int) string {
	if segment.approvalData == nil {
		return ""
	}
	return b.renderApprovalPill(segment.approvalData, width)
}

func (b *contentBuffer) renderCompactionBannerSegment(segment contentSegment, width int) string {
	if segment.compactionData == nil {
		return ""
	}
	return b.renderCompactionBanner(segment.compactionData, width)
}

func (b *contentBuffer) renderInterruptedSegment() string {
	return b.styles.FgMute.Render("interrupted") + "\n\n"
}

func (b *contentBuffer) renderDefaultSegment(segment contentSegment) string {
	return b.styles.AssistantProse.Render(segment.text) + "\n"
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

func (b *contentBuffer) inProgressPreview(width int) string {
	preview := strings.TrimRight(b.streamBuffer, "\n")
	if strings.TrimSpace(preview) == "" {
		return ""
	}
	cursor := ""
	if b.tickCount%2 == 0 {
		cursor = "█"
	}
	wrapped := ansi.Hardwrap(preview+cursor, max(1, width), true)
	return b.styles.AssistantProse.Render(wrapped) + "\n"
}

func (b *contentBuffer) streamingIndicatorView() string {
	if b.streamingPhase == "" {
		return ""
	}
	label := "thinking…"
	if b.streamingPhase == "tool" {
		label = "running tool…"
	}
	activeDot := b.tickCount % 3
	dots := make([]string, 3)
	for i := range dots {
		if i == activeDot {
			dots[i] = b.styles.Accent.Render("·")
		} else {
			dots[i] = b.styles.FgMute.Render("·")
		}
	}
	return strings.Join(dots, "  ") + "  " + b.styles.FgMute.Render(label) + "\n"
}

func (b *contentBuffer) buildBashLines(tc *toolCallSegment) []string {
	var lines []string
	outputText := tc.preview.Output
	if strings.TrimSpace(outputText) == "" {
		outputText = tc.body
	}
	if tc.preview.Command != "" {
		lines = append(lines, b.styles.Accent.Render("$")+" "+tc.preview.Command)
	} else {
		lines = append(lines, b.styles.Accent.Render("$")+" "+tc.args)
	}
	outputLines := strings.Split(strings.TrimRight(outputText, "\n"), "\n")
	if len(outputLines) == 1 && strings.TrimSpace(outputLines[0]) == "" {
		outputLines = nil
	}
	fgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	for _, l := range outputLines {
		lines = append(lines, fgStyle.Render(l))
	}
	if tc.preview.Message != "" {
		lines = append(lines, b.styles.FgMute.Render(tc.preview.Message))
	}
	exitCode := tc.preview.ExitCode
	if exitCode == 0 && tc.hasError {
		exitCode = 1
	}
	exitLine := fmt.Sprintf("exit %d", exitCode)
	if exitCode != 0 {
		lines = append(lines, b.styles.Removed.Render(exitLine))
	} else {
		lines = append(lines, b.styles.Added.Render(exitLine))
	}
	if tc.preview.Truncated {
		lines = append(lines, b.styles.FgMute.Render("output truncated"))
	}
	return lines
}

func (b *contentBuffer) buildPlainLines(tc *toolCallSegment) []string {
	var lines []string

	// For scratchpad, show the current four-field model instead of the tool
	// result JSON.
	if tc.tool == "scratchpad" && len(tc.rawArgs) > 0 {
		for _, key := range []string{"intent", "decisions", "open", "next"} {
			if val, ok := tc.rawArgs[key]; ok {
				if s, ok := val.(string); ok && s != "" {
					label := strings.ToUpper(key[:1]) + key[1:] + ": " + s
					lines = append(lines, b.styles.FgDim.Render(label))
				}
			}
		}
		if len(lines) > 0 {
			return lines
		}
	}

	for _, l := range strings.Split(strings.TrimRight(tc.body, "\n"), "\n") {
		lines = append(lines, b.styles.FgDim.Render(l))
	}
	return lines
}

func (b *contentBuffer) buildGlobLines(tc *toolCallSegment) []string {
	return b.buildListLines(tc.preview.Path, "glob results", tc.preview.Returned, tc.preview.NextOffset, tc.preview.Truncated, tc.preview.Entries, true)
}

func (b *contentBuffer) buildLSLines(tc *toolCallSegment) []string {
	return b.buildListLines(tc.preview.Path, "directory listing", tc.preview.Returned, tc.preview.NextOffset, tc.preview.Truncated, tc.preview.Entries, true)
}

func (b *contentBuffer) buildListLines(path, label string, returned, nextOffset int, truncated bool, entries []output.ToolPreviewListEntry, showDirs bool) []string {
	summary := label
	if path != "" {
		summary = path + " · " + summary
	}
	if returned > 0 {
		summary += fmt.Sprintf(" · %d entries", returned)
	} else {
		summary += " · no entries"
	}
	if nextOffset > 0 || truncated {
		summary += " · more available"
	}

	lines := []string{b.styles.FgDim.Render(summary)}
	if len(entries) == 0 {
		lines = append(lines, b.styles.FgMute.Render("no results"))
		return lines
	}

	for _, entry := range entries {
		name := entry.Path
		if showDirs && entry.IsDir {
			name += "/"
			lines = append(lines, b.styles.ToolTagRead.Render(name))
		} else {
			lines = append(lines, b.styles.FgDim.Render(name))
		}
	}
	if nextOffset > 0 || truncated {
		lines = append(lines, b.styles.FgMute.Render("more available"))
	}
	return lines
}

func (b *contentBuffer) buildGrepLines(tc *toolCallSegment) []string {
	switch tc.preview.OutputMode {
	case "files_with_matches":
		return b.buildGrepFileLines(tc)
	case "count":
		return b.buildGrepCountLines(tc)
	default:
		return b.buildGrepContentLines(tc)
	}
}

func (b *contentBuffer) buildGrepFileLines(tc *toolCallSegment) []string {
	summary := "files with matches"
	if tc.preview.Path != "" {
		summary = tc.preview.Path + " · " + summary
	}
	if tc.preview.Returned > 0 {
		summary += fmt.Sprintf(" · %d files", tc.preview.Returned)
	}
	if tc.preview.NextOffset > 0 {
		summary += " · more available"
	}
	lines := []string{b.styles.FgDim.Render(summary)}
	if len(tc.preview.GrepFiles) == 0 {
		lines = append(lines, b.styles.FgMute.Render("no matches found"))
		return lines
	}
	for _, file := range tc.preview.GrepFiles {
		lines = append(lines, b.styles.ToolTagGrep.Render(file.Path))
	}
	if tc.preview.NextOffset > 0 {
		lines = append(lines, b.styles.FgMute.Render("more available"))
	}
	return lines
}

func (b *contentBuffer) buildGrepCountLines(tc *toolCallSegment) []string {
	summary := "match counts"
	if tc.preview.Path != "" {
		summary = tc.preview.Path + " · " + summary
	}
	if tc.preview.Returned > 0 {
		summary += fmt.Sprintf(" · %d matches", tc.preview.Returned)
	}
	if tc.preview.NextOffset > 0 {
		summary += " · more available"
	}
	lines := []string{b.styles.FgDim.Render(summary)}
	if len(tc.preview.GrepFiles) == 0 {
		lines = append(lines, b.styles.FgMute.Render("no matches found"))
		return lines
	}
	for _, file := range tc.preview.GrepFiles {
		lines = append(lines, b.styles.ToolTagGrep.Render(fmt.Sprintf("%s:%d", file.Path, file.Count)))
	}
	if tc.preview.NextOffset > 0 {
		lines = append(lines, b.styles.FgMute.Render("more available"))
	}
	return lines
}

func (b *contentBuffer) buildGrepContentLines(tc *toolCallSegment) []string {
	summary := "content matches"
	if tc.preview.Path != "" {
		summary = tc.preview.Path + " · " + summary
	}
	total := 0
	for _, file := range tc.preview.GrepFiles {
		total += len(file.Matches)
	}
	if total > 0 {
		summary += fmt.Sprintf(" · %d matches", total)
	}
	if tc.preview.NextOffset > 0 {
		summary += " · more available"
	}
	lines := []string{b.styles.FgDim.Render(summary)}
	if len(tc.preview.GrepFiles) == 0 {
		lines = append(lines, b.styles.FgMute.Render("no matches found"))
		return lines
	}
	for _, file := range tc.preview.GrepFiles {
		lines = append(lines, b.styles.ToolTagGrep.Render("## "+file.Path))
		for _, match := range file.Matches {
			if match.LineNumber > 0 {
				lines = append(lines, b.styles.FgFaint.Render(fmt.Sprintf("%4d  ", match.LineNumber))+b.styles.FgDim.Render(match.Text))
				continue
			}
			lines = append(lines, b.styles.FgDim.Render(match.Text))
		}
	}
	if tc.preview.NextOffset > 0 {
		lines = append(lines, b.styles.FgMute.Render("more available"))
	}
	return lines
}

func (b *contentBuffer) buildFilePreviewLines(tc *toolCallSegment, width int) []string {
	doc := b.previewDocument(tc)
	if doc.Kind == "" {
		return b.buildPlainLines(tc)
	}

	rule := b.styles.FgMute.Render(strings.Repeat("─", max(1, width-2)))
	caption := b.renderFileCaption(tc, doc)

	lines := make([]string, 0, len(doc.Lines)+4)
	lines = append(lines, rule)
	lines = append(lines, caption)
	lines = append(lines, rule)
	lines = append(lines, b.renderFilePreviewDocument(doc)...)
	if doc.Truncated {
		lines = append(lines, b.styles.FgFaint.Render("   …  1 more"))
	}
	lines = append(lines, rule)
	return lines
}

func (b *contentBuffer) buildDiffPreviewLines(tc *toolCallSegment, width int) []string {
	doc := b.previewDocument(tc)
	if doc.Kind != output.PreviewFormatKindEditDiff {
		return b.buildPlainLines(tc)
	}

	lines := make([]string, 0, len(doc.Lines)+1)
	lines = append(lines, b.renderDiffPreviewDocument(doc, width)...)
	if doc.Truncated {
		lines = append(lines, b.styles.FgMute.Render("… output truncated"))
	}
	return lines
}

func (b *contentBuffer) previewDocument(tc *toolCallSegment) output.PreviewDocument {
	if tc.displayPreview != nil {
		return *tc.displayPreview
	}
	switch tc.preview.Kind {
	case output.ToolPreviewKindEditDiff:
		return output.FormatEditDiffPreview(tc.preview.Path, tc.preview.Before, tc.preview.After)
	case output.ToolPreviewKindFileWrite:
		return output.FormatFilePreview(tc.preview.Path, tc.preview.Contents)
	case output.ToolPreviewKindReadFile:
		return output.FormatFilePreview(tc.preview.Path, tc.preview.Contents)
	default:
		return output.PreviewDocument{}
	}
}

func (b *contentBuffer) renderFileCaption(tc *toolCallSegment, doc output.PreviewDocument) string {
	label := "file preview"
	switch {
	case tc.displayPreview != nil:
		label = "display file preview"
		if doc.Language != "" && doc.Language != "plain" {
			label += " · " + doc.Language
		}
	case tc.preview.Kind == output.ToolPreviewKindFileWrite:
		if tc.preview.Created {
			label = "new file preview"
		} else {
			label = "updated file contents preview"
		}
	case tc.preview.Kind == output.ToolPreviewKindReadFile:
		label = "read file preview"
		if doc.Language != "" && doc.Language != "plain" {
			label += " · " + doc.Language
		}
	}
	lineCount := previewContentLineCount(doc)
	if doc.Path != "" {
		return b.styles.FgDim.Render(fmt.Sprintf("%s · %s · %d lines", doc.Path, label, lineCount))
	}
	return b.styles.FgDim.Render(fmt.Sprintf("%s · %d lines", label, lineCount))
}

func (b *contentBuffer) renderFilePreviewDocument(doc output.PreviewDocument) []string {
	lines := make([]string, 0, len(doc.Lines))
	for i, line := range doc.Lines {
		if line.Kind == output.PreviewLineKindTruncated {
			lines = append(lines, b.renderPreviewLine(line))
			continue
		}
		gutter := b.styles.FgFaint.Render(fmt.Sprintf("%4d  ", i+1))
		lines = append(lines, gutter+b.renderPreviewLine(line))
	}
	return lines
}

func (b *contentBuffer) renderDiffHeader(doc output.PreviewDocument, width int) string {
	added, removed := output.CountPreviewChanges(doc)
	addedStr := fmt.Sprintf("+%d", added)
	removedStr := fmt.Sprintf("-%d", removed)
	plainTagStyled := b.styles.ToolTagWrite.Render(" [edit] ")
	plainPathStyled := b.baseTextStyle().Render(doc.Path)
	plainMetrics := addedStr + " " + removedStr
	headerPlainWidth := lipgloss.Width(plainTagStyled) + 1 + lipgloss.Width(plainPathStyled)
	available := width - headerPlainWidth - lipgloss.Width(plainMetrics) - 1
	if available < 1 {
		available = 1
	}
	header := plainTagStyled + " " + plainPathStyled
	header = lipgloss.NewStyle().Width(available).Render(header)
	styledMetrics := b.styles.Added.Render(addedStr) + " " + b.styles.Removed.Render(removedStr)
	return b.styles.BgElev2.Render(header + " " + styledMetrics)
}

func (b *contentBuffer) renderDiffPreviewDocument(doc output.PreviewDocument, width int) []string {
	lines := make([]string, 0, len(doc.Lines))
	oldLine, newLine := 1, 1
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", max(1, width)))
	for _, line := range doc.Lines {
		switch line.Kind {
		case output.PreviewLineKindHeader:
			if strings.TrimSpace(line.Prefix) == "@@" {
				lines = append(lines, rule)
			}
		case output.PreviewLineKindContext:
			lines = append(lines, b.renderDiffRow(line, oldLine, " ", theme.Bg))
			oldLine++
			newLine++
		case output.PreviewLineKindRemoved:
			lines = append(lines, b.renderDiffRow(line, oldLine, "-", theme.DiffRemovedBg))
			oldLine++
		case output.PreviewLineKindAdded:
			lines = append(lines, b.renderDiffRow(line, newLine, "+", theme.DiffAddedBg))
			newLine++
		case output.PreviewLineKindTruncated:
			lines = append(lines, b.styles.FgMute.Render("… output truncated"))
		default:
			lines = append(lines, b.renderPreviewLine(line))
		}
	}
	return lines
}

func (b *contentBuffer) renderDiffRow(line output.PreviewLine, lineNo int, sign string, bgColor string) string {
	lineNoStr := b.styles.FgMute.Render(fmt.Sprintf("%4d", lineNo))
	var signStr string
	switch sign {
	case "+":
		signStr = b.styles.Added.Render("+")
	case "-":
		signStr = b.styles.Removed.Render("-")
	default:
		signStr = b.styles.FgMute.Render(" ")
	}
	bg := lipgloss.NewStyle().Background(lipgloss.Color(bgColor))

	var sb strings.Builder
	sb.WriteString(bg.Render(lineNoStr + " " + signStr + " "))
	for _, span := range line.Spans {
		rendered := b.renderPreviewSpan(span)
		if rendered == "" {
			continue
		}
		sb.WriteString(bg.Render(rendered))
	}
	return sb.String()
}

func (b *contentBuffer) renderPreviewLine(line output.PreviewLine) string {
	text := b.renderPreviewSpans(line.Spans)
	if text == "" {
		return b.styles.FgDim.Render("")
	}
	switch line.Kind {
	case output.PreviewLineKindHeader:
		return b.styles.FgMute.Render(text)
	case output.PreviewLineKindContext:
		return b.baseTextStyle().Render(text)
	case output.PreviewLineKindAdded:
		return b.styles.Added.Render(text)
	case output.PreviewLineKindRemoved:
		return b.styles.Removed.Render(text)
	case output.PreviewLineKindTruncated:
		return b.styles.Warn.Render(text)
	default:
		return text
	}
}

func (b *contentBuffer) renderPreviewSpans(spans []output.PreviewSpan) string {
	var sb strings.Builder
	for _, span := range spans {
		sb.WriteString(b.renderPreviewSpan(span))
	}
	return sb.String()
}

func (b *contentBuffer) renderPreviewSpan(span output.PreviewSpan) string {
	style := b.previewTokenStyle(span.Type)
	return style.Render(span.Text)
}

func (b *contentBuffer) previewTokenStyle(token chroma.TokenType) lipgloss.Style {
	if b.previewStyleCache == nil {
		b.previewStyleCache = make(map[chroma.TokenType]lipgloss.Style)
	}
	if style, ok := b.previewStyleCache[token]; ok {
		return style
	}

	style, meaningful := chromaStyleToLipgloss(previewSyntaxStyle.Get(token))
	if !meaningful {
		style = b.baseTextStyle()
	}
	b.previewStyleCache[token] = style
	return style
}

func chromaStyleToLipgloss(entry chroma.StyleEntry) (lipgloss.Style, bool) {
	style := lipgloss.NewStyle()
	meaningful := false

	if entry.Colour.IsSet() {
		style = style.Foreground(lipgloss.Color(entry.Colour.String()))
		meaningful = true
	}
	switch entry.Bold {
	case chroma.Yes:
		style = style.Bold(true)
		meaningful = true
	case chroma.No:
		style = style.Bold(false)
		meaningful = true
	}
	switch entry.Italic {
	case chroma.Yes:
		style = style.Italic(true)
		meaningful = true
	case chroma.No:
		style = style.Italic(false)
		meaningful = true
	}
	switch entry.Underline {
	case chroma.Yes:
		style = style.Underline(true)
		meaningful = true
	case chroma.No:
		style = style.Underline(false)
		meaningful = true
	}

	return style, meaningful
}

func (b *contentBuffer) baseTextStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
}

// setGlamourStyleSheet rebuilds the glamour stylesheet and invalidates the cached
// renderer so the next markdown render picks up the new accent colour.
func (b *contentBuffer) setGlamourStyleSheet(accentHex string) {
	b.glamourStyleSheet = theme.BuildGlamourStyleSheet(accentHex)
	b.renderer = nil
	b.renderWidth = 0
}

func (b *contentBuffer) renderApprovalPill(ad *approvalPillData, width int) string {
	const indent = "   "
	innerWidth := width - len(indent)
	if innerWidth < 10 {
		innerWidth = 10
	}

	label := "approval required"
	if ad.tool != "" {
		label = ad.tool
		if ad.mode != "" {
			label += " · " + ad.mode
		}
	}

	if ad.resolved {
		bar := b.styles.FgMute.Render("│")
		var statusStr string
		if ad.accepted {
			statusStr = b.styles.Added.Render("✓ approved")
		} else {
			statusStr = b.styles.Removed.Render("✗ denied")
		}
		statusW := lipgloss.Width(statusStr)
		// bar(1) + space(1) + question + space(1) + status = innerWidth
		qW := innerWidth - 3 - statusW
		if qW < 0 {
			qW = 0
		}
		question := lipgloss.NewStyle().Width(qW).Render(b.styles.FgDim.Render(label))
		return indent + bar + " " + question + " " + statusStr + "\n"
	}

	// Unresolved: accent bar + bg-elev background + question in fg + styled buttons
	bar := b.styles.Accent.Render("│")

	accentSoft := lipgloss.Color(theme.AccentSoft)
	bgElev2C := lipgloss.Color(theme.BgElev2)
	fgMuteC := lipgloss.Color(theme.FgMute)
	fgDimC := lipgloss.Color(theme.FgDim)
	accentC := lipgloss.Color(theme.AccentAmber)

	btnApprove := lipgloss.NewStyle().Background(accentSoft).Foreground(fgMuteC).Render("[y]") +
		lipgloss.NewStyle().Background(accentSoft).Foreground(accentC).Render(" approve")
	btnDeny := lipgloss.NewStyle().Background(bgElev2C).Foreground(fgMuteC).Render("[n]") +
		lipgloss.NewStyle().Background(bgElev2C).Foreground(fgDimC).Render(" deny")
	btnAlways := lipgloss.NewStyle().Background(bgElev2C).Foreground(fgMuteC).Render("[a]") +
		lipgloss.NewStyle().Background(bgElev2C).Foreground(fgDimC).Render(" always")
	buttons := btnApprove + " " + btnDeny + " " + btnAlways
	buttonsW := lipgloss.Width(buttons)

	// bg-elev region: innerWidth - 1 (bar)
	bgW := innerWidth - 1
	// space(1) + question + space(1) + buttons fills bgW
	qW := bgW - 2 - buttonsW
	if qW < 0 {
		qW = 0
	}

	question := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Foreground(lipgloss.Color(theme.Fg)).
		Width(qW).Render(label)

	bgContent := " " + question + " " + buttons
	bgRow := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).Width(bgW).Render(bgContent)

	return indent + bar + bgRow + "\n"
}

func (b *contentBuffer) renderCompactionBanner(cd *compactionBannerData, width int) string {
	if cd.finished {
		var body string
		switch {
		case cd.msgCount > 0:
			body = fmt.Sprintf("context compacted · %d messages summarized into 1", cd.msgCount)
		case cd.summary != "":
			body = "context compacted · " + cd.summary
		default:
			body = "context compacted"
		}
		return b.styles.ThinkingBar.Render("▼ "+body) + "\n"
	}

	// In-progress banner: warn left bar + bg-elev row, full transcript width
	if width < 10 {
		width = 10
	}
	bar := b.styles.Warn.Render("│")
	bgW := width - 1

	compacting := b.styles.Warn.Render(" Compacting")
	subtitle := b.styles.FgDim.Render(" " + cd.subtitle + " ")

	// Show pct% only when real progress data is known
	pctStr := ""
	if cd.pct > 0 {
		pctStr = fmt.Sprintf("%d%% ", cd.pct)
	}
	pctLabel := b.styles.FgDim.Render(pctStr)

	fixedW := lipgloss.Width(compacting) + lipgloss.Width(subtitle) + lipgloss.Width(pctLabel)
	barW := bgW - fixedW
	if barW < 0 {
		barW = 0
	}

	// Use real progress if known; otherwise animate a sweep using tickCount
	var filled int
	if cd.progress > 0 {
		filled = int(float64(barW) * cd.progress)
	} else if barW > 0 {
		// Sweep 0→barW and repeat; each tick = 500ms
		filled = b.tickCount % (barW + 1)
	}
	if filled > barW {
		filled = barW
	}
	filledBar := b.styles.Warn.Render(strings.Repeat("█", filled))
	emptyBar := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev2)).Render(strings.Repeat(" ", barW-filled))
	progressBar := filledBar + emptyBar

	rowContent := compacting + subtitle + progressBar + pctLabel
	row := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).Width(bgW).Render(rowContent)

	return bar + row + "\n"
}
