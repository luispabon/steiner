package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

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

func (b *contentBuffer) buildPatchLines(tc *toolCallSegment) []string {
	patch, _ := tc.rawArgs["patch"].(string)
	if strings.TrimSpace(patch) == "" {
		return b.buildPlainLines(tc)
	}

	addedBg := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.DiffAddedBg)).
		Foreground(lipgloss.Color(theme.Added))
	removedBg := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.DiffRemovedBg)).
		Foreground(lipgloss.Color(theme.Removed))

	rawLines := strings.Split(strings.TrimRight(patch, "\n"), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		switch {
		case strings.HasPrefix(line, "*** Add File:"):
			lines = append(lines, b.styles.Added.Render(line))
		case strings.HasPrefix(line, "*** Delete File:"):
			lines = append(lines, b.styles.Removed.Render(line))
		case strings.HasPrefix(line, "*** Update File:"):
			lines = append(lines, b.styles.FgDim.Render(line))
		case line == "*** Begin Patch" || line == "*** End Patch":
			lines = append(lines, b.styles.FgFaint.Render(line))
		case strings.HasPrefix(line, "@@"):
			lines = append(lines, b.styles.FgFaint.Render(line))
		case strings.HasPrefix(line, "+"):
			lines = append(lines, addedBg.Render(line))
		case strings.HasPrefix(line, "-"):
			lines = append(lines, removedBg.Render(line))
		default:
			lines = append(lines, b.styles.FgDim.Render(line))
		}
	}

	p := tc.preview
	if p.HunksApplied > 0 || p.HunksFailed > 0 {
		applied := b.styles.Added.Render(fmt.Sprintf("%d applied", p.HunksApplied))
		failed := b.styles.Removed.Render(fmt.Sprintf("%d failed", p.HunksFailed))
		lines = append(lines, b.styles.FgFaint.Render("─────"))
		if p.HunksFailed > 0 {
			lines = append(lines, applied+" · "+failed)
		} else {
			lines = append(lines, applied)
		}
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
	case output.ToolPreviewKindFetchURL:
		return output.FormatFilePreviewWithLanguage(tc.preview.Path, tc.preview.Language, tc.preview.Contents)
	case output.ToolPreviewKindWebSearch:
		return output.FormatFilePreviewWithLanguage(tc.preview.Path, tc.preview.Language, tc.preview.Contents)
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
	case tc.preview.Kind == output.ToolPreviewKindFetchURL:
		label = "fetched page preview"
		if doc.Language != "" && doc.Language != "plain" {
			label += " · " + doc.Language
		}
	case tc.preview.Kind == output.ToolPreviewKindWebSearch:
		if tc.preview.Returned >= 0 {
			return b.styles.FgDim.Render(fmt.Sprintf("search results - %d results", tc.preview.Returned))
		}
		return b.styles.FgDim.Render("search results")
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
