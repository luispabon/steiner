package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// specializedDelegateTools is the set of tool names that are specialized delegates
// and should be rendered as a single delegation box in the TUI.
// Keep this list in sync with internal/delegation agent types — do not import that package.
var specializedDelegateTools = map[string]bool{
	"explore":  true,
	"research": true,
	"code":     true,
	"plan":     true,
	"verify":   true,
}

// isSpecializedDelegateTool reports whether tool is a specialized delegate tool.
func isSpecializedDelegateTool(tool string) bool {
	return specializedDelegateTools[strings.ToLower(strings.TrimSpace(tool))]
}

// summarizeArgs extracts a human-readable summary from tool arguments
func summarizeArgs(tool string, args map[string]any) string {
	if args == nil {
		return tool
	}
	if strings.EqualFold(strings.TrimSpace(tool), "delegate") || isSpecializedDelegateTool(tool) {
		return summarizeDelegateArgs(args)
	}
	if strings.EqualFold(strings.TrimSpace(tool), "mutate") {
		return summarizeMutateArgs(args)
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

func summarizeDelegateArgs(args map[string]any) string {
	for _, key := range []string{"task", "prompt", "description", "instructions", "goal"} {
		if v, ok := args[key]; ok {
			return truncateToolArgSummary(fmt.Sprintf("%v", v))
		}
	}
	return summarizeFirstArgValue(args)
}

func summarizeFirstArgValue(args map[string]any) string {
	// Fallback: first value
	for _, v := range args {
		return truncateToolArgSummary(fmt.Sprintf("%v", v))
	}
	return ""
}

func truncateToolArgSummary(s string) string {
	if len(s) > 60 {
		s = s[:57] + "..."
	}
	return s
}

func delegatePromptText(args map[string]any) string {
	if args == nil {
		return ""
	}
	for _, key := range []string{"task", "prompt", "description", "instructions", "goal"} {
		if v, ok := args[key]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	for _, v := range args {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func summarizeMutateArgs(args map[string]any) string {
	ops, _ := args["operations"].([]any)
	if len(ops) == 0 {
		return "mutate"
	}
	firstOp, _ := ops[0].(map[string]any)
	path, _ := firstOp["path"].(string)
	if path == "" {
		from, _ := firstOp["from"].(string)
		to, _ := firstOp["to"].(string)
		if from != "" || to != "" {
			path = strings.TrimSpace(from + " -> " + to)
		}
	}
	if path == "" {
		path = "mutate"
	}
	if len(ops) > 1 {
		return fmt.Sprintf("%s (+%d more)", path, len(ops)-1)
	}
	return path
}

// previewBodyKind determines how to render the tool body using structured preview data first.
func previewBodyKind(tool string, preview output.ToolPreview) string {
	switch preview.Kind {
	case output.ToolPreviewKindReadFile, output.ToolPreviewKindFetchURL, output.ToolPreviewKindWebSearch:
		return "file"
	case output.ToolPreviewKindGlobList:
		return "glob"
	case output.ToolPreviewKindLSList:
		return "ls"
	case output.ToolPreviewKindGrep:
		return "grep"
	case output.ToolPreviewKindBash:
		return "bash"
	case output.ToolPreviewKindMutate:
		return "mutate"
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
func inferBodyKind(tool, _ string) string {
	switch tool {
	case "bash":
		return "bash"
	case "read", "read_file":
		return "file"
	case "mutate":
		return "mutate"
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
	case "mutate":
		return b.styles.ToolTagWrite
	case "grep":
		return b.styles.ToolTagGrep
	case "search":
		return b.styles.ToolTagSearch
	case "glob":
		return b.styles.ToolTagGlob
	case "todo":
		return b.styles.ToolTagTodo
	default:
		return b.styles.ToolTagDefault
	}
}

func (b *contentBuffer) toolBorderStyle(tool string) lipgloss.Style {
	switch normalizeToolName(tool) {
	case "bash":
		return b.styles.ToolBorderBash
	case "read", "read_file":
		return b.styles.ToolBorderRead
	case "mutate":
		return b.styles.ToolBorderWrite
	case "grep":
		return b.styles.ToolBorderGrep
	case "search":
		return b.styles.ToolBorderSearch
	case "glob":
		return b.styles.ToolBorderGlob
	case "todo":
		return b.styles.ToolBorderTodo
	default:
		return b.styles.ToolBorderDefault
	}
}

func (b *contentBuffer) renderToolCall(tc *toolCallSegment, width int) string {
	content := b.renderToolCallFrame(tc, width)
	return b.renderToolCallBox(content, tc.tool, width)
}

func (b *contentBuffer) renderToolCallGroup(group *toolCallGroupSegment, width int) string {
	if group == nil || len(group.entries) == 0 {
		return ""
	}

	parts := make([]string, 0, len(group.entries)*2-1)
	dividerWidth := width - 4
	if dividerWidth < 1 {
		dividerWidth = 1
	}
	for i, tc := range group.entries {
		parts = append(parts, b.renderToolCallFrame(tc, width))
		if i < len(group.entries)-1 {
			parts = append(parts, b.renderToolCallDivider(dividerWidth))
		}
	}

	return b.renderToolCallBox(strings.Join(parts, "\n"), group.tool, width)
}

func (b *contentBuffer) renderToolCallFrame(tc *toolCallSegment, width int) string {
	if tc == nil {
		return ""
	}
	tagStyle := b.toolTagStyle(tc.tool)
	tag := tagStyle.Render(tc.tool)
	tagWidth := lipgloss.Width(tag)
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
	if tc.collapsed {
		return header
	}

	bodyContent := b.renderToolBody(tc, width)

	return header + "\n" + bodyContent
}

func (b *contentBuffer) renderToolCallBox(content, tool string, width int) string {
	borderStyle := b.toolBorderStyle(tool)
	boxStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Padding(0, 1).
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderStyle.GetForeground())

	content = theme.WithBg(content, lipgloss.Color(theme.BgElev))

	boxWidth := width - 2
	if boxWidth < 1 {
		boxWidth = 1
	}

	return boxStyle.Width(boxWidth).Render(content) + "\n"
}

func (b *contentBuffer) renderToolCallDivider(width int) string {
	if width < 1 {
		width = 1
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.BorderSoft)).
		Render(strings.Repeat("─", width))
}

func (b *contentBuffer) renderToolCallMeta(tc *toolCallSegment) ([]string, int) {
	parts := make([]string, 0, 2)
	width := 0
	if tc.meta != "" {
		var styled string
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

func (b *contentBuffer) renderToolBody(tc *toolCallSegment, width int) string {
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
	case "file":
		lines = b.buildFilePreviewLines(tc, rowWidth-2)
	case "mutate":
		lines = b.buildMutateLines(tc, rowWidth-2)
	default:
		lines = b.buildPlainLines(tc)
	}

	truncated := false
	if len(lines) > maxRows && tc.displayPreview == nil {
		lines = lines[:maxRows]
		truncated = true
	}

	bodyContent := strings.Join(lines, "\n")
	if truncated {
		bodyContent += "\n" + b.styles.FgMute.Render("↓ more")
	}

	return b.appendApprovalContent(bodyContent, tc, rowWidth)
}

func (b *contentBuffer) appendApprovalContent(bodyContent string, tc *toolCallSegment, width int) string {
	var suffix string
	switch {
	case tc.approvalPending && !tc.approvalResolved:
		suffix = b.renderToolApprovalBlock(tc, width)
	case tc.approvalResolved:
		suffix = b.renderToolApprovalResolvedLine(tc, width)
	default:
		return bodyContent
	}
	if bodyContent != "" {
		return bodyContent + "\n" + suffix
	}
	return suffix
}

// toolAccentColor returns the full-opacity accent color for the given tool.
func toolAccentColor(tool string) lipgloss.Color {
	switch normalizeToolName(tool) {
	case "bash":
		return lipgloss.Color(theme.AccentAmber)
	case "mutate":
		return lipgloss.Color(theme.ToolGrn)
	case "read", "read_file":
		return lipgloss.Color(theme.ToolCyan)
	default:
		return lipgloss.Color(theme.ToolBlue)
	}
}

func (b *contentBuffer) renderToolApprovalBlock(tc *toolCallSegment, width int) string {
	accentC := toolAccentColor(tc.tool)
	accentStyle := lipgloss.NewStyle().Foreground(accentC)

	// Separator
	sepWidth := width
	if sepWidth < 1 {
		sepWidth = 1
	}
	sepStyle := accentStyle.Faint(true)
	sep := sepStyle.Render(strings.Repeat("─", sepWidth))

	// Pulsing dot — blinks on every 500ms tick
	var dot string
	if b.tickCount%2 == 0 {
		dot = accentStyle.Render("●")
	} else {
		dot = b.styles.FgDim.Render("●")
	}
	header := dot + " " + accentStyle.Bold(true).Render("APPROVAL REQUIRED")

	// Preview text
	previewText := strings.TrimSpace(tc.approvalPreview)
	if previewText == "" {
		previewText = "no preview available"
	}
	preview := b.styles.FgMute.Render(truncateRunes(previewText, width))

	// Buttons
	btns := b.renderToolApprovalButtons(tc, accentC, width)

	return strings.Join([]string{sep, header, preview, btns}, "\n")
}

func (b *contentBuffer) renderToolApprovalButtons(tc *toolCallSegment, accentC lipgloss.Color, width int) string {
	type btnSpec struct {
		label string
		key   string
		idx   int
	}
	specs := []btnSpec{
		{"Allow once", "y", 0},
		{"Always allow", "a", 1},
		{"Deny", "n", 2},
	}

	items := make([]string, 0, len(specs))
	for _, spec := range specs {
		var bg lipgloss.Color
		var fg lipgloss.Color
		switch spec.idx {
		case 0:
			bg = accentC
			fg = lipgloss.Color(theme.Black)
		case 1:
			bg = lipgloss.Color(theme.BgInput)
			fg = lipgloss.Color(theme.FgMute)
		case 2:
			bg = lipgloss.Color("#3a1a1a")
			fg = lipgloss.Color("#FF5F5F")
		}

		keyStyle := lipgloss.NewStyle().
			Background(bg).
			Foreground(fg).
			Faint(true)
		btnStyle := lipgloss.NewStyle().
			Padding(0, 2).
			Background(bg).
			Foreground(fg)
		if tc.approvalSelectedAction == spec.idx {
			btnStyle = btnStyle.Bold(true).Underline(true)
		}

		label := spec.label + " " + keyStyle.Render(spec.key)
		items = append(items, btnStyle.Render(label))
	}

	line := strings.Join(items, " ")
	if width > 0 && lipgloss.Width(line) > width {
		return lipgloss.JoinVertical(lipgloss.Left, items...)
	}
	return line
}

func (b *contentBuffer) renderToolApprovalResolvedLine(tc *toolCallSegment, width int) string {
	if tc.approvalAccepted {
		verb := toolApprovalVerb(tc.tool)
		text := "✓ approved — " + verb
		style := lipgloss.NewStyle().
			Background(lipgloss.Color(theme.DiffAddedBg)).
			Foreground(lipgloss.Color(theme.Added)).
			Width(width)
		return style.Render(text)
	}
	text := "✗ denied — tool call blocked"
	style := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.DiffRemovedBg)).
		Foreground(lipgloss.Color(theme.Removed)).
		Width(width)
	return style.Render(text)
}

func toolApprovalVerb(tool string) string {
	switch normalizeToolName(tool) {
	case "bash":
		return "running command"
	case "read", "read_file":
		return "reading file"
	case "mutate":
		return "modifying file"
	default:
		return "executing tool"
	}
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
