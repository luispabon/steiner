package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// summarizeArgs extracts a human-readable summary from tool arguments
func summarizeArgs(tool string, args map[string]any) string {
	if args == nil {
		return tool
	}
	// Try common arg keys in order
	for _, key := range []string{"command", "path", "file_path", "pattern", "query", "description"} {
		if v, ok := args[key]; ok {
			s := fmt.Sprintf("%v", v)
			if len(s) > 60 {
				s = s[:57] + "..."
			}
			return s
		}
	}
	// Fallback: first value
	for _, v := range args {
		s := fmt.Sprintf("%v", v)
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		return s
	}
	return tool
}

// previewBodyKind determines how to render the tool body using structured preview data first.
func previewBodyKind(tool string, preview output.ToolPreview) string {
	switch preview.Kind {
	case output.ToolPreviewKindEditDiff:
		return "diff"
	case output.ToolPreviewKindFileWrite, output.ToolPreviewKindReadFile:
		return "file"
	case output.ToolPreviewKindGlobList:
		return "glob"
	case output.ToolPreviewKindLSList:
		return "ls"
	case output.ToolPreviewKindGrep:
		return "grep"
	case output.ToolPreviewKindBash:
		return "bash"
	case output.ToolPreviewKindPlain:
		if strings.EqualFold(strings.TrimSpace(tool), "bash") {
			return "bash"
		}
		return "plain"
	default:
		if strings.EqualFold(strings.TrimSpace(tool), "bash") {
			return "bash"
		}
		return "plain"
	}
}

// inferBodyKind determines how to render the tool body when only the raw result is available.
func inferBodyKind(tool, body string) string {
	switch tool {
	case "bash":
		return "bash"
	case "read", "read_file":
		return "file"
	case "edit", "write", "write_file":
		if strings.HasPrefix(strings.TrimSpace(body), "@@") || strings.Contains(body, "\n@@") {
			return "diff"
		}
		return "plain"
	default:
		return "plain"
	}
}

func (b *contentBuffer) toolTagStyle(tool string) lipgloss.Style {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "bash":
		return b.styles.ToolTagBash
	case "read", "read_file":
		return b.styles.ToolTagRead
	case "write", "write_file", "edit":
		return b.styles.ToolTagWrite
	case "grep":
		return b.styles.ToolTagGrep
	case "glob":
		return b.styles.ToolTagGlob
	case "todo":
		return b.styles.ToolTagTodo
	default:
		return b.styles.ToolTagDefault
	}
}

// toolTagBgHex returns the hex background color of a tool tag pill.
func (b *contentBuffer) toolTagBgHex(tool string) string {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "bash":
		return theme.AccentAmber
	case "read", "read_file":
		return theme.ToolCyan
	case "write", "write_file", "edit":
		return theme.ToolGrn
	case "grep":
		return theme.ToolMag
	case "glob":
		return theme.ToolBlue
	case "todo":
		return theme.Warn
	default:
		return theme.ToolBlue
	}
}

func (b *contentBuffer) renderToolCall(tc *toolCallSegment, width int) string {
	tagStyle := b.toolTagStyle(tc.tool)
	tag := tagStyle.Render(tc.tool)
	tagWidth := lipgloss.Width(tag)
	tagBgColor := b.toolTagBgHex(tc.tool)
	disclosure := "▾"
	if tc.collapsed {
		disclosure = "▸"
	}

	// Build header: tag [gap] args [gap] meta
	// Args column width accounts for meta on the right
	const gap = 2
	metaParts, metaWidth := b.renderToolCallMeta(tc)
	metaStr := ""
	if len(metaParts) > 0 {
		metaStr = strings.Join(metaParts, " ")
	}

	argsAvail := width - lipgloss.Width(disclosure) - 1 - tagWidth - gap - metaWidth - gap - 1
	if argsAvail < 1 {
		argsAvail = 1
	}

	// Build header as plain text
	argsText := tc.args
	if len([]rune(argsText)) > argsAvail {
		argsText = string([]rune(argsText)[:argsAvail-1]) + "…"
	}

	header := disclosure + " " + tag + strings.Repeat(" ", gap) + argsText
	if metaStr != "" {
		header = header + strings.Repeat(" ", gap) + metaStr
	}

	boxStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Padding(0, 1).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(tagBgColor))

	if tc.collapsed {
		boxWidth := width
		if boxWidth < 1 {
			boxWidth = 1
		}

		return theme.WithBg(boxStyle.Width(boxWidth).Render(header)+"\n", lipgloss.Color(theme.BgElev))
	}
	// Expanded: wrap both header + body in single box
	bodyContent := b.renderToolBody(tc, width, tagBgColor)

	// Combine for box rendering
	fullContent := header + "\n" + bodyContent

	// Box dimensions - .Width() sets total width including borders and padding
	boxWidth := width
	if boxWidth < 1 {
		boxWidth = 1
	}

	return theme.WithBg(boxStyle.Width(boxWidth).Render(fullContent)+"\n", lipgloss.Color(theme.BgElev))
}

func (b *contentBuffer) renderToolCallMeta(tc *toolCallSegment) ([]string, int) {
	parts := make([]string, 0, 2)
	width := 0
	if tc.bodyKind == "diff" {
		doc := b.previewDocument(tc)
		if doc.Kind == output.PreviewFormatKindEditDiff {
			added, removed := output.CountPreviewChanges(doc)
			diffMeta := b.styles.Added.Render(fmt.Sprintf("+%d", added)) + " " + b.styles.Removed.Render(fmt.Sprintf("-%d", removed))
			if len(parts) > 0 {
				width++
			}
			parts = append(parts, diffMeta)
			width += lipgloss.Width(diffMeta)
		}
	}
	if tc.meta != "" {
		styled := tc.meta
		if tc.hasError {
			styled = b.styles.Removed.Render(tc.meta)
		} else {
			styled = b.styles.Added.Render(tc.meta)
		}
		if len(parts) > 0 {
			width++
		}
		parts = append(parts, styled)
		width += lipgloss.Width(styled)
	}
	return parts, width
}

func (b *contentBuffer) renderToolBody(tc *toolCallSegment, width int, tagBgColor string) string {
	const maxRows = 20

	rowWidth := width // caller accounts for outer box border+padding
	if rowWidth < 10 {
		rowWidth = 10
	}

	var lines []string
	switch tc.bodyKind {
	case "bash":
		lines = b.buildBashLines(tc)
	case "glob":
		lines = b.buildGlobLines(tc)
	case "ls":
		lines = b.buildLSLines(tc)
	case "grep":
		lines = b.buildGrepLines(tc)
	case "diff":
		lines = b.buildDiffPreviewLines(tc, rowWidth-2)
	case "file":
		lines = b.buildFilePreviewLines(tc, rowWidth-2)
	default:
		lines = b.buildPlainLines(tc)
	}

	truncated := false
	if len(lines) > maxRows {
		lines = lines[:maxRows]
		truncated = true
	}

	bodyContent := strings.Join(lines, "\n")
	if truncated {
		bodyContent += "\n" + b.styles.FgMute.Render("↓ more")
	}

	return bodyContent
}

func cloneToolArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return nil
	}
	cloned := make(map[string]any, len(arguments))
	for key, value := range arguments {
		cloned[key] = value
	}
	return cloned
}

func previewContentLineCount(doc output.PreviewDocument) int {
	count := len(doc.Lines)
	if count == 0 {
		return 0
	}
	last := doc.Lines[count-1]
	if last.Kind == output.PreviewLineKindTruncated {
		count--
	}
	return count
}
