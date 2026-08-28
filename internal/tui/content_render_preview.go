package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

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

	for _, l := range strings.Split(strings.TrimRight(tc.body, "\n"), "\n") {
		lines = append(lines, b.styles.FgDim.Render(l))
	}
	return lines
}

func (b *contentBuffer) buildGlobLines(tc *toolCallSegment) []string {
	return b.buildListLines(listLinesParams{
		path:       tc.preview.Path,
		label:      "glob results",
		returned:   tc.preview.Returned,
		nextOffset: tc.preview.NextOffset,
		truncated:  tc.preview.Truncated,
		entries:    tc.preview.Entries,
		showDirs:   true,
	})
}

func (b *contentBuffer) buildLSLines(tc *toolCallSegment) []string {
	return b.buildListLines(listLinesParams{
		path:       tc.preview.Path,
		label:      "directory listing",
		returned:   tc.preview.Returned,
		nextOffset: tc.preview.NextOffset,
		truncated:  tc.preview.Truncated,
		entries:    tc.preview.Entries,
		showDirs:   true,
	})
}

// listLinesParams bundles the arguments for buildListLines.
type listLinesParams struct {
	path       string
	label      string
	returned   int
	nextOffset int
	truncated  bool
	entries    []output.ToolPreviewListEntry
	showDirs   bool
}

func (b *contentBuffer) buildListLines(p listLinesParams) []string {
	summary := p.label
	if p.path != "" {
		summary = p.path + " · " + summary
	}
	if p.returned > 0 {
		summary += fmt.Sprintf(" · %d entries", p.returned)
	} else {
		summary += " · no entries"
	}
	if p.nextOffset > 0 || p.truncated {
		summary += " · more available"
	}

	lines := []string{b.styles.FgDim.Render(summary)}
	if len(p.entries) == 0 {
		lines = append(lines, b.styles.FgMute.Render("no results"))
		return lines
	}

	for _, entry := range p.entries {
		name := entry.Path
		if p.showDirs && entry.IsDir {
			name += "/"
			lines = append(lines, b.toolTagStyle("read").Render(name))
		} else {
			lines = append(lines, b.styles.FgDim.Render(name))
		}
	}
	if p.nextOffset > 0 || p.truncated {
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

// buildGrepResultLines renders a grep-mode summary header followed by
// per-file lines produced by renderFile, with a shared "no matches
// found"/"more available" wrapping for all grep output modes.
func (b *contentBuffer) buildGrepResultLines(tc *toolCallSegment, summary string, renderFile func(output.ToolPreviewGrepFile) []string) []string {
	if tc.preview.NextOffset > 0 {
		summary += " · more available"
	}
	lines := []string{b.styles.FgDim.Render(summary)}
	if len(tc.preview.GrepFiles) == 0 {
		lines = append(lines, b.styles.FgMute.Render("no matches found"))
		return lines
	}
	for _, file := range tc.preview.GrepFiles {
		lines = append(lines, renderFile(file)...)
	}
	if tc.preview.NextOffset > 0 {
		lines = append(lines, b.styles.FgMute.Render("more available"))
	}
	return lines
}

func (b *contentBuffer) buildGrepFileLines(tc *toolCallSegment) []string {
	summary := "files with matches"
	if tc.preview.Path != "" {
		summary = tc.preview.Path + " · " + summary
	}
	if tc.preview.Returned > 0 {
		summary += fmt.Sprintf(" · %d files", tc.preview.Returned)
	}
	return b.buildGrepResultLines(tc, summary, func(file output.ToolPreviewGrepFile) []string {
		return []string{b.toolTagStyle("grep").Render(file.Path)}
	})
}

func (b *contentBuffer) buildGrepCountLines(tc *toolCallSegment) []string {
	summary := "match counts"
	if tc.preview.Path != "" {
		summary = tc.preview.Path + " · " + summary
	}
	if tc.preview.Returned > 0 {
		summary += fmt.Sprintf(" · %d matches", tc.preview.Returned)
	}
	return b.buildGrepResultLines(tc, summary, func(file output.ToolPreviewGrepFile) []string {
		return []string{b.toolTagStyle("grep").Render(fmt.Sprintf("%s:%d", file.Path, file.Count))}
	})
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
	return b.buildGrepResultLines(tc, summary, func(file output.ToolPreviewGrepFile) []string {
		lines := []string{b.toolTagStyle("grep").Render("## " + file.Path)}
		for _, match := range file.Matches {
			if match.LineNumber > 0 {
				lines = append(lines, b.styles.FgFaint.Render(fmt.Sprintf("%4d  ", match.LineNumber))+b.styles.FgDim.Render(match.Text))
				continue
			}
			lines = append(lines, b.styles.FgDim.Render(match.Text))
		}
		return lines
	})
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

func (b *contentBuffer) buildFetchURLLines(tc *toolCallSegment, width int) []string {
	rule := b.styles.FgMute.Render(strings.Repeat("─", max(1, width)))

	lines := make([]string, 0, 12)
	lines = append(lines, rule)

	// URL
	if tc.preview.Path != "" {
		lines = append(lines, b.styles.FgDim.Render("url:         "+tc.preview.Path))
	}

	// max_size from tool arguments
	if tc.rawArgs != nil {
		if maxSize, ok := tc.rawArgs["max_size"]; ok {
			lines = append(lines, b.styles.FgDim.Render(fmt.Sprintf("max_size:    %v", maxSize)))
		}
	}

	// HTTP status code
	if tc.preview.StatusCode > 0 {
		lines = append(lines, b.styles.FgDim.Render(fmt.Sprintf("http:        %d", tc.preview.StatusCode)))
	}

	// content_length
	if tc.preview.ContentLength > 0 {
		lines = append(lines, b.styles.FgDim.Render(fmt.Sprintf("size:        %d runes", tc.preview.ContentLength)))
	}

	// title
	if tc.preview.FetchTitle != "" {
		lines = append(lines, b.styles.FgDim.Render("title:       "+tc.preview.FetchTitle))
	}

	// description
	if tc.preview.FetchDescription != "" {
		lines = append(lines, b.styles.FgDim.Render("description: "+tc.preview.FetchDescription))
	}

	lines = append(lines, rule)

	// Body
	switch tc.preview.Language {
	case "error":
		lines = append(lines, b.styles.FgDim.Render(fmt.Sprintf("%s · error · %s", tc.preview.Path, tc.preview.Contents)))
	case "image":
		// Image placeholder
		lines = append(lines, b.styles.FgDim.Render(tc.preview.Contents))
		lines = append(lines, b.styles.FgMute.Render("image returned to model"))
	default:
		// Markdown body via existing preview document formatting.
		doc := b.previewDocument(tc)
		if doc.Kind != "" {
			for _, line := range doc.Lines {
				lines = append(lines, b.renderPreviewLine(line))
			}
			if doc.Truncated {
				lines = append(lines, b.styles.FgFaint.Render("   …  1 more"))
			}
		}
	}

	return lines
}

func (b *contentBuffer) previewDocument(tc *toolCallSegment) output.PreviewDocument {
	if tc.displayPreview != nil {
		return *tc.displayPreview
	}
	switch tc.preview.Kind {
	case output.ToolPreviewKindReadFile:
		doc := output.FormatFilePreview(tc.preview.Path, tc.preview.Contents)
		doc.StartLine = tc.preview.StartLine
		return doc
	case output.ToolPreviewKindFetchURL:
		return output.FormatFilePreviewWithLanguage(tc.preview.Path, tc.preview.Language, tc.preview.Contents)
	case output.ToolPreviewKindWebSearch:
		return output.FormatFilePreviewWithLanguage(tc.preview.Path, tc.preview.Language, tc.preview.Contents)
	default:
		return output.PreviewDocument{}
	}
}

func (b *contentBuffer) renderFileCaption(tc *toolCallSegment, doc output.PreviewDocument) string {
	label, useRange := computeFilePreviewLabel(tc, doc)
	if useRange {
		return b.styles.FgDim.Render(buildOffsetCaption(doc, label))
	}
	if tc.preview.Kind == output.ToolPreviewKindWebSearch {
		return b.styles.FgDim.Render(label)
	}
	lineCount := previewContentLineCount(doc)
	if doc.Path != "" {
		return b.styles.FgDim.Render(fmt.Sprintf("%s · %s · %d lines", doc.Path, label, lineCount))
	}
	return b.styles.FgDim.Render(fmt.Sprintf("%s · %d lines", label, lineCount))
}

func computeFilePreviewLabel(tc *toolCallSegment, doc output.PreviewDocument) (label string, useRange bool) {
	switch {
	case tc.displayPreview != nil:
		label = "display file preview"
		if doc.Language != "" && doc.Language != "plain" {
			label += " · " + doc.Language
		}
	case tc.preview.Kind == output.ToolPreviewKindReadFile:
		label = "read file preview"
		if doc.Language != "" && doc.Language != "plain" {
			label += " · " + doc.Language
		}
		if doc.StartLine > 1 {
			useRange = true
		}
	case tc.preview.Kind == output.ToolPreviewKindFetchURL:
		label = "fetched page preview"
		if doc.Language != "" && doc.Language != "plain" {
			label += " · " + doc.Language
		}
	case tc.preview.Kind == output.ToolPreviewKindWebSearch:
		if tc.preview.Returned >= 0 {
			label = fmt.Sprintf("search results - %d results", tc.preview.Returned)
		} else {
			label = "search results"
		}
		return label, false
	}
	return label, useRange
}

func buildOffsetCaption(doc output.PreviewDocument, label string) string {
	lineCount := previewContentLineCount(doc)
	if doc.Path != "" {
		return fmt.Sprintf("%s · %s · lines %d–%d", doc.Path, label, doc.StartLine, doc.StartLine+lineCount-1)
	}
	return fmt.Sprintf("%s · lines %d–%d", label, doc.StartLine, doc.StartLine+lineCount-1)
}

func (b *contentBuffer) renderFilePreviewDocument(doc output.PreviewDocument) []string {
	lines := make([]string, 0, len(doc.Lines))
	startLine := doc.StartLine
	if startLine <= 0 {
		startLine = 1
	}
	for i, line := range doc.Lines {
		if line.Kind == output.PreviewLineKindTruncated {
			lines = append(lines, b.renderPreviewLine(line))
			continue
		}
		gutter := b.styles.FgFaint.Render(fmt.Sprintf("%4d  ", startLine+i))
		lines = append(lines, gutter+b.renderPreviewLine(line))
	}
	return lines
}

func (b *contentBuffer) renderDiffPreviewDocument(doc output.PreviewDocument, width int) []string {
	lines := make([]string, 0, len(doc.Lines))
	oldLine, newLine := 1, 1
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", max(1, width-2)))
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

func (b *contentBuffer) buildMutateLines(tc *toolCallSegment, width int) []string {
	if len(tc.preview.MutateOperations) == 0 {
		return b.buildPlainLines(tc)
	}

	var lines []string

	// Result summary
	if tc.preview.HunksApplied > 0 || tc.preview.HunksFailed > 0 {
		summary := fmt.Sprintf("%d operations", len(tc.preview.MutateOperations))
		summary += fmt.Sprintf(" · %d applied", tc.preview.HunksApplied)
		if tc.preview.HunksFailed > 0 {
			summary += fmt.Sprintf(" · %d failed", tc.preview.HunksFailed)
		}
		lines = append(lines, b.styles.FgDim.Render(summary))
	}

	rule := b.styles.FgMute.Render(strings.Repeat("─", max(1, width-2)))

	for _, op := range tc.preview.MutateOperations {
		lines = append(lines, "") // blank separator
		lines = append(lines, b.renderMutateOperation(op, rule)...)
	}

	return lines
}

func (b *contentBuffer) renderMutateOperation(op output.ToolPreviewMutateOperation, rule string) []string {
	switch op.Type {
	case "create", "write":
		return b.renderMutateWriteOp(op, rule)
	case "replace":
		return b.renderMutateReplaceOp(op, rule)
	case "delete_file":
		return b.renderMutateDeleteOp(op)
	case "move":
		return b.renderMutateMoveOp(op)
	}
	return nil
}

func (b *contentBuffer) renderMutateWriteOp(op output.ToolPreviewMutateOperation, rule string) []string {
	badge := b.styles.Added.Render("A")
	if op.Type == "write" {
		badge = b.styles.Warn.Render("M")
	}
	header := badge + " " + b.styles.FgDim.Render(op.Path)
	lines := []string{header, rule}

	doc := output.FormatFilePreview(op.Path, op.Content)
	lines = append(lines, b.renderFilePreviewDocument(doc)...)
	lines = append(lines, rule)
	return lines
}

func (b *contentBuffer) renderMutateReplaceOp(op output.ToolPreviewMutateOperation, rule string) []string {
	removedBg, addedBg := mutateDiffStyles()
	badge := b.styles.Warn.Render("M")
	header := badge + " " + b.styles.FgDim.Render(op.Path)
	lines := []string{header, rule}

	oldLines := strings.Split(op.OldString, "\n")
	newLines := strings.Split(op.NewString, "\n")
	maxLen := max(len(oldLines), len(newLines))
	for i := 0; i < maxLen; i++ {
		if i < len(oldLines) {
			lines = append(lines, removedBg.Render("- "+oldLines[i]))
		}
		if i < len(newLines) {
			lines = append(lines, addedBg.Render("+ "+newLines[i]))
		}
	}
	lines = append(lines, rule)
	return lines
}

func (b *contentBuffer) renderMutateDeleteOp(op output.ToolPreviewMutateOperation) []string {
	badge := b.styles.Removed.Render("D")
	return []string{badge + " " + b.styles.FgDim.Render(op.Path)}
}

func (b *contentBuffer) renderMutateMoveOp(op output.ToolPreviewMutateOperation) []string {
	badge := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.ToolBlue)).Render("R")
	header := badge + " " + b.styles.FgDim.Render(op.From) + " " + b.styles.FgFaint.Render("→") + " " + b.styles.FgDim.Render(op.To)
	return []string{header}
}

func mutateDiffStyles() (removed, added lipgloss.Style) {
	added = lipgloss.NewStyle().
		Background(lipgloss.Color(theme.DiffAddedBg)).
		Foreground(lipgloss.Color(theme.Added))
	removed = lipgloss.NewStyle().
		Background(lipgloss.Color(theme.DiffRemovedBg)).
		Foreground(lipgloss.Color(theme.Removed))
	return removed, added
}
