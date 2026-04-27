package tui

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func (b *contentBuffer) String(width int) string {
	b.segmentHeights = make([]int, len(b.segments))
	parts := make([]string, 0, len(b.segments)+2)
	for i, segment := range b.segments {
		if segment.kind == segmentThinkingBlock && !b.showThinking {
			b.segmentHeights[i] = 0
			continue
		}
		rendered := b.renderSegment(segment, width)
		b.segmentHeights[i] = strings.Count(rendered, "\n")
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}
	if preview := b.inProgressPreview(); preview != "" {
		parts = append(parts, preview)
	}
	parts = append(parts, b.streamingIndicatorView())
	return strings.Join(parts, "")
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
	case segmentThinkingBlock:
		return b.renderThinkingBlockSegment(segment)
	case segmentApprovalPill:
		return b.renderApprovalPillSegment(segment, width)
	case segmentCompactionBanner:
		return b.renderCompactionBannerSegment(segment, width)
	case segmentInterrupted:
		return b.renderInterruptedSegment()
	default:
		return b.renderDefaultSegment(segment)
	}
}

func (b *contentBuffer) renderToolCallSegment(segment contentSegment, width int) string {
	return b.renderToolCall(segment.toolData, width)
}

func (b *contentBuffer) renderAssistantMarkdownSegment(segment contentSegment, width int) string {
	rendered := b.renderMarkdown(segment.text, width)
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
	bar := b.styles.UserBar.Render("│")
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

func (b *contentBuffer) renderThinkingBlockSegment(segment contentSegment) string {
	if segment.thinkData == nil {
		return ""
	}
	td := segment.thinkData
	if td.collapsed {
		runes := []rune(td.preview)
		if len(runes) > 60 {
			runes = runes[:60]
		}
		return b.styles.ThinkingBar.Render("▸ Thinking · "+string(runes)+"…") + "\n"
	}
	var sb strings.Builder
	sb.WriteString(b.styles.ThinkingBar.Render("▾ Thinking") + "\n")
	bar := b.styles.ThinkingBar.Render("▎")
	for _, line := range strings.Split(strings.TrimRight(td.body, "\n"), "\n") {
		sb.WriteString(bar + " " + b.styles.ThinkingBar.Render(line) + "\n")
	}
	return sb.String()
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

func (b *contentBuffer) renderMarkdown(block string, width int) string {
	renderer := b.markdownRenderer(width)
	if renderer == nil {
		return b.styles.AssistantProse.Render("assistant> " + block)
	}
	rendered, err := renderer.Render(block)
	if err != nil {
		b.lastRenderErr = fmt.Errorf("render markdown: %w", err)
		return b.styles.AssistantProse.Render("assistant> " + block)
	}
	return rendered
}

func (b *contentBuffer) markdownRenderer(width int) *glamour.TermRenderer {
	renderWidth := maxInt(1, width-markdownRenderPadding)
	if b.renderer != nil && b.renderWidth == renderWidth {
		return b.renderer
	}
	opts := []glamour.TermRendererOption{
		glamour.WithWordWrap(renderWidth),
		glamour.WithPreservedNewLines(),
	}
	if b.glamourStyleSheet != nil {
		opts = append([]glamour.TermRendererOption{b.glamourStyleSheet}, opts...)
	} else {
		opts = append([]glamour.TermRendererOption{glamour.WithStandardStyle("dark")}, opts...)
	}
	renderer, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		return nil
	}
	b.renderer = renderer
	b.renderWidth = renderWidth
	return renderer
}

func (b *contentBuffer) inProgressPreview() string {
	preview := strings.TrimRight(b.streamBuffer, "\n")
	if strings.TrimSpace(preview) == "" {
		return ""
	}
	cursor := ""
	if b.tickCount%2 == 0 {
		cursor = "█"
	}
	return b.styles.AssistantProse.Render(preview+cursor) + "\n"
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
	dollar := b.styles.Accent.Render("$")
	lines = append(lines, dollar+" "+tc.args)
	if strings.TrimSpace(tc.body) != "" {
		fgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
		for _, l := range strings.Split(strings.TrimRight(tc.body, "\n"), "\n") {
			lines = append(lines, fgStyle.Render(l))
		}
	}
	if tc.hasError {
		lines = append(lines, b.styles.Removed.Render("exit 1"))
	} else {
		lines = append(lines, b.styles.Added.Render("exit 0"))
	}
	return lines
}

func (b *contentBuffer) buildPlainLines(tc *toolCallSegment) []string {
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(tc.body, "\n"), "\n") {
		lines = append(lines, b.styles.FgDim.Render(l))
	}
	return lines
}

func (b *contentBuffer) buildFilePreviewLines(tc *toolCallSegment, width int) []string {
	doc := b.previewDocument(tc)
	if doc.Kind == "" {
		return b.buildPlainLines(tc)
	}

	rule := b.styles.FgMute.Render(strings.Repeat("─", maxInt(1, width-2)))
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

	lines := make([]string, 0, len(doc.Lines)+2)
	lines = append(lines, b.renderDiffHeader(doc, width))
	lines = append(lines, b.renderDiffPreviewDocument(doc, width)...)
	if doc.Truncated {
		lines = append(lines, b.styles.FgMute.Render("… output truncated"))
	}
	return lines
}

func (b *contentBuffer) previewDocument(tc *toolCallSegment) output.PreviewDocument {
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
	switch tc.preview.Kind {
	case output.ToolPreviewKindFileWrite:
		if tc.preview.Created {
			label = "new file preview"
		} else {
			label = "updated file contents preview"
		}
	case output.ToolPreviewKindReadFile:
		label = "read file preview"
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
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", maxInt(1, width)))
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
	var raw strings.Builder
	for _, span := range line.Spans {
		raw.WriteString(span.Text)
	}
	rawText := raw.String()
	var body string
	if sign == " " {
		body = b.styles.FgDim.Render(rawText)
	} else {
		body = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Render(rawText)
	}
	bg := lipgloss.NewStyle().Background(lipgloss.Color(bgColor))
	return bg.Render(lineNoStr + " " + signStr + " " + body)
}

func (b *contentBuffer) renderPreviewLine(line output.PreviewLine) string {
	var sb strings.Builder
	for _, span := range line.Spans {
		sb.WriteString(b.renderPreviewSpan(span))
	}
	text := sb.String()
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

func (b *contentBuffer) renderPreviewSpan(span output.PreviewSpan) string {
	style := b.previewTokenStyle(span.Type)
	return style.Render(span.Text)
}

func (b *contentBuffer) previewTokenStyle(token chroma.TokenType) lipgloss.Style {
	switch {
	case token.InCategory(chroma.Comment):
		return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgFaint)).Italic(true)
	case token.InCategory(chroma.Keyword):
		return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.SyntaxBlue))
	case token.InCategory(chroma.Name) && token.InSubCategory(chroma.NameBuiltin),
		token.InCategory(chroma.Name) && token.InSubCategory(chroma.NameClass):
		return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.ToolCyan))
	case token.InCategory(chroma.Name) && token.InSubCategory(chroma.NameAttribute):
		return b.styles.Added // struct tags — green like strings
	case token.InCategory(chroma.LiteralString):
		return b.styles.Added // green
	case token.InCategory(chroma.LiteralNumber):
		return b.styles.Warn // amber
	case token.InCategory(chroma.Operator):
		return b.styles.FgFaint
	case token.InCategory(chroma.Punctuation):
		return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	case token.InCategory(chroma.GenericDeleted):
		return b.styles.Removed
	case token.InCategory(chroma.GenericInserted):
		return b.styles.Added
	default:
		return b.baseTextStyle()
	}
}

func (b *contentBuffer) baseTextStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
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
