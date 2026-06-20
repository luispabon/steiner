package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// specializedDelegateTools is the set of tool names rendered as a single
// delegation box in the TUI. It contains the specialized delegate agent types
// plus follow_up, which resumes an existing child and reuses the same box.
// Keep this list in sync with internal/delegation — do not import that package.
var specializedDelegateTools = map[string]bool{
	"explore":   true,
	"research":  true,
	"code":      true,
	"plan":      true,
	"verify":    true,
	"follow_up": true,
}

// isSpecializedDelegateTool reports whether tool is a specialized delegate tool.
func isSpecializedDelegateTool(tool string) bool {
	return specializedDelegateTools[strings.ToLower(strings.TrimSpace(tool))]
}

// summarizeArgs extracts a human-readable summary from tool arguments
func summarizeArgs(tool string, args map[string]any) string {
	tool = normalizeToolName(tool)
	if args == nil {
		return tool
	}
	if tool == "follow_up" {
		return summarizeFollowUpArgs(args)
	}
	if tool == "delegate" || isSpecializedDelegateTool(tool) {
		return delegateArgText(args)
	}
	if tool == "mutate" {
		return summarizeMutateArgs(args)
	}
	if tool == "grep" {
		return summarizeGrepArgs(args)
	}
	if tool == "glob" {
		return summarizeGlobArgs(args)
	}
	if tool == "read" || tool == "read_file" {
		return summarizeReadArgs(args)
	}
	if tool == "ls" {
		return summarizeLSArgs(args)
	}
	// Try common arg keys in order
	for _, key := range []string{"command", "path", "file_path", "pattern", "query", "description", "url"} {
		if v, ok := args[key]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	// Fallback: first value
	for _, v := range args {
		return fmt.Sprintf("%v", v)
	}
	return tool
}

func delegateArgText(args map[string]any) string {
	if args == nil || len(args) == 0 {
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
	opType, _ := firstOp["type"].(string)

	var summary string
	if opType == "move" {
		from, _ := firstOp["from"].(string)
		to, _ := firstOp["to"].(string)
		summary = fmt.Sprintf("move %s → %s", from, to)
	} else {
		path, _ := firstOp["path"].(string)
		switch {
		case opType != "" && path != "":
			summary = opType + " " + path
		case path != "":
			summary = path
		case opType != "":
			summary = opType
		default:
			summary = "mutate"
		}
	}

	if len(ops) > 1 {
		return fmt.Sprintf("%s (+%d more)", summary, len(ops)-1)
	}
	return summary
}

func summarizeReadArgs(args map[string]any) string {
	path, _ := args["path"].(string)
	if path == "" {
		path, _ = args["file_path"].(string)
	}
	if path == "" {
		return "read"
	}

	offset := 1
	if v, ok := args["offset"].(float64); ok {
		offset = int(v)
	}

	limit := 200
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}

	end := offset + limit - 1
	return fmt.Sprintf("%s:%d–%d", path, offset, end)
}

func summarizeGrepArgs(args map[string]any) string {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "grep"
	}

	result := fmt.Sprintf("'%s'", pattern)

	path, pathOk := args["path"].(string)
	glob, globOk := args["glob"].(string)

	if pathOk && path != "" {
		if !strings.HasPrefix(path, "./") && !strings.HasPrefix(path, "/") {
			path = "./" + path
		}

		if globOk && glob != "" {
			if strings.HasSuffix(path, "/") {
				result = fmt.Sprintf("%s in %s%s", result, path, glob)
			} else {
				result = fmt.Sprintf("%s in %s/%s", result, path, glob)
			}
		} else {
			result = fmt.Sprintf("%s in %s", result, path)
		}
	}

	return result
}

func summarizeGlobArgs(args map[string]any) string {
	pattern, _ := args["pattern"].(string)
	path, _ := args["path"].(string)

	if path != "" && pattern != "" {
		if !strings.HasPrefix(path, "./") {
			path = "./" + path
		}
		path = strings.TrimRight(path, "/")
		return path + "/" + pattern
	}

	if pattern != "" {
		return pattern
	}

	if path != "" {
		if !strings.HasPrefix(path, "./") {
			path = "./" + path
		}
		return path
	}

	return "glob"
}

func summarizeLSArgs(args map[string]any) string {
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	recursive, _ := args["recursive"].(bool)
	if recursive {
		return path + " (recursive)"
	}
	return path
}

// previewBodyKind determines how to render the tool body using structured preview data first.
func previewBodyKind(tool string, preview output.ToolPreview) string {
	switch preview.Kind {
	case output.ToolPreviewKindReadFile, output.ToolPreviewKindWebSearch:
		return "file"
	case output.ToolPreviewKindFetchURL:
		return "fetch_url"
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
	innerWidth := width - 4 // border (2) + padding (2)
	if innerWidth < 1 {
		innerWidth = 1
	}
	content := b.renderToolCallFrame(tc, innerWidth)
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
	innerWidth := width - 4 // border (2) + padding (2)
	if innerWidth < 1 {
		innerWidth = 1
	}
	for i, tc := range group.entries {
		parts = append(parts, b.renderToolCallFrame(tc, innerWidth))
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
	case "fetch_url":
		lines = b.buildFetchURLLines(tc, rowWidth-2)
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
	switch {
	case tc.approvalPending && !tc.approvalResolved:
		suffix := b.renderToolApprovalBlock(tc, width)
		if bodyContent != "" {
			return bodyContent + "\n" + suffix
		}
		return suffix
	case tc.approvalResolved:
		resolved := b.renderToolApprovalResolvedLine(tc, width)
		// Denied or error: skip body content — resolved line is sufficient.
		if !tc.approvalAccepted || tc.hasError {
			return resolved
		}
		if bodyContent != "" {
			return bodyContent + "\n" + resolved
		}
		return resolved
	default:
		return bodyContent
	}
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

	// Separator — inner content width = Width(width-2) - padding(1+1) = width-4
	sepWidth := width - 4
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

	// Preview: parse JSON key-value pairs and format as bold-key lines with 1-line padding.
	preview := b.renderApprovalPreview(tc.approvalPreview, width)

	// Buttons
	btns := b.renderToolApprovalButtons(tc, accentC, width)

	return strings.Join([]string{sep, header, preview, btns}, "\n")
}

// renderApprovalPreview formats the approval preview string.
// If the preview is JSON, each key is rendered bold followed by its value.
// Otherwise, the raw text is shown muted with 1-line padding above and below.
func (b *contentBuffer) renderApprovalPreview(raw string, width int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return b.styles.FgMute.Render("no preview available")
	}

	var kvMap map[string]any
	if err := json.Unmarshal([]byte(raw), &kvMap); err == nil && len(kvMap) > 0 {
		// JSON: render each key-value pair on its own line, key bold.
		boldStyle := lipgloss.NewStyle().Bold(true)
		muteStyle := b.styles.FgMute
		var lines []string
		lines = append(lines, "") // top padding
		for k, v := range kvMap {
			key := k
			if len(key) > 0 {
				key = strings.ToUpper(key[:1]) + key[1:]
			}
			label := boldStyle.Render(key + ":")
			val := muteStyle.Render(truncateRunes(fmt.Sprintf("%v", v), width-len(k)-2))
			lines = append(lines, label+" "+val)
		}
		lines = append(lines, "") // bottom padding
		return strings.Join(lines, "\n")
	}

	// Plain text — pad top and bottom with empty lines.
	return "\n" + b.styles.FgMute.Render(truncateRunes(raw, width)) + "\n"
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

	// Selected button uses tool accent color; all others are a neutral gray.
	grayBg := lipgloss.Color(theme.BgInput)
	grayFg := lipgloss.Color(theme.FgMute)
	accentFg := lipgloss.Color(theme.Black)

	items := make([]string, 0, len(specs))
	for _, spec := range specs {
		selected := tc.approvalSelectedAction == spec.idx

		var bg, fg lipgloss.Color
		if selected {
			bg = accentC
			fg = accentFg
		} else {
			bg = grayBg
			fg = grayFg
		}

		// Key chip: "[ y ]" — rendered separately to avoid nested ANSI corruption.
		chipStyle := lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(selected)
		labelStyle := lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(selected).Padding(0, 1)

		chipRendered := chipStyle.Render("[ " + spec.key + " ]")
		labelRendered := labelStyle.Render(spec.label)
		items = append(items, chipRendered+labelRendered)
	}

	line := strings.Join(items, "  ")
	if width > 0 && lipgloss.Width(line) > width {
		return lipgloss.JoinVertical(lipgloss.Left, items...)
	}
	return line
}

func (b *contentBuffer) renderToolApprovalResolvedLine(tc *toolCallSegment, width int) string {
	// width-4: accounts for box Width(width-2) + Padding(0,1) inner content area.
	// PaddingLeft(1) consumes 1 of those chars, so Width shrinks by 1 more.
	lineWidth := max(1, width-4)
	var style lipgloss.Style
	var text string
	if tc.approvalAccepted {
		style = lipgloss.NewStyle().
			Background(lipgloss.Color(theme.DiffAddedBg)).
			Foreground(lipgloss.Color(theme.Added)).
			PaddingLeft(1).
			Width(lineWidth)
		text = "✓ approved"
	} else {
		style = lipgloss.NewStyle().
			Background(lipgloss.Color(theme.DiffRemovedBg)).
			Foreground(lipgloss.Color(theme.Removed)).
			PaddingLeft(1).
			Width(lineWidth)
		text = "✗ denied"
	}
	return "\n" + style.Render(text)
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
