package output

import (
	"fmt"
	"strconv"
	"strings"
)

type inspectionSnapshot struct {
	TotalDiagnostics   int
	ContextDiagnostics int
	LastStopReason     string
	LastBudget         string
	LastCompaction     string
	Recent             []string
	RecentContext      []string
}

func summarizeInspection(events []Event, recentLimit int) inspectionSnapshot {
	if recentLimit < 0 {
		recentLimit = 0
	}

	summary := inspectionSnapshot{
		TotalDiagnostics: len(events),
	}
	if len(events) == 0 {
		return summary
	}

	recent := make([]string, 0, min(len(events), recentLimit))
	recentContext := make([]string, 0, min(len(events), recentLimit))

	for _, event := range events {
		if line := FormatEvent(event); strings.TrimSpace(line) != "" {
			recent = appendRecentLine(recent, line, recentLimit)
			if isContextDiagnosticEvent(event) {
				recentContext = appendRecentLine(recentContext, line, recentLimit)
			}
		}

		switch payload := event.Payload.(type) {
		case StopReasonEvent:
			if segment := renderEvent(event); strings.TrimSpace(segment.Text) != "" {
				summary.LastStopReason = segment.Text
			}
		case ContextDiagnosticsEvent:
			summary.ContextDiagnostics++
			segment := renderEvent(event)
			if strings.TrimSpace(segment.Text) == "" {
				continue
			}
			switch payload.Kind {
			case "budget":
				summary.LastBudget = segment.Text
			case "compaction":
				summary.LastCompaction = segment.Text
			}
		}
	}

	summary.Recent = recent
	summary.RecentContext = recentContext
	return summary
}

func appendRecentLine(lines []string, line string, limit int) []string {
	if limit == 0 {
		return lines
	}
	lines = append(lines, line)
	if len(lines) > limit {
		return append([]string(nil), lines[len(lines)-limit:]...)
	}
	return lines
}

func isContextDiagnosticEvent(event Event) bool {
	_, ok := event.Payload.(ContextDiagnosticsEvent)
	return ok
}

func previewDocumentForToolPayload(preview ToolPreview) (PreviewDocument, bool) {
	switch preview.Kind {
	case ToolPreviewKindEditDiff:
		return FormatEditDiffPreview(preview.Path, preview.Before, preview.After), true
	case ToolPreviewKindFileWrite:
		return FormatFilePreview(preview.Path, preview.Contents), true
	case ToolPreviewKindReadFile:
		doc := FormatFilePreview(preview.Path, normalizeReadPreviewContents(preview.Contents, 0))
		doc.StartLine = preview.StartLine
		return doc, true
	case ToolPreviewKindFetchURL:
		return FormatFilePreviewWithLanguage(preview.Path, preview.Language, preview.Contents), true
	case ToolPreviewKindWebSearch:
		return FormatFilePreviewWithLanguage(preview.Path, preview.Language, preview.Contents), true
	default:
		return PreviewDocument{}, false
	}
}

func normalizeReadPreviewContents(contents string, startLine int) string {
	if contents == "" {
		return contents
	}

	lines := strings.Split(contents, "\n")
	if len(lines) == 0 {
		return contents
	}

	if startLine > 0 {
		current := startLine
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if !hasReadLineNumberPrefixForLine(line, current) {
				return contents
			}
			current++
		}
		return stripReadPreviewContents(lines, startLine)
	}

	hasNumberedPrefix := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !hasReadLineNumberPrefix(line) {
			return contents
		}
		hasNumberedPrefix = true
	}
	if !hasNumberedPrefix {
		return contents
	}
	return stripReadPreviewContents(lines, 1)
}

func stripReadPreviewContents(lines []string, startLine int) string {
	out := make([]string, len(lines))
	current := startLine
	for i, line := range lines {
		out[i] = stripReadLineNumberPrefixForLine(line, current)
		current++
	}
	return strings.Join(out, "\n")
}

func hasReadLineNumberPrefix(line string) bool {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	start := i
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == start {
		return false
	}
	return i < len(line) && (line[i] == ' ' || line[i] == '\t')
}

func hasReadLineNumberPrefixForLine(line string, lineNumber int) bool {
	prefix := strconv.Itoa(lineNumber)
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if !strings.HasPrefix(line[i:], prefix) {
		return false
	}
	i += len(prefix)
	return i == len(line) || line[i] == ' ' || line[i] == '\t'
}

func stripReadLineNumberPrefixForLine(line string, lineNumber int) string {
	prefix := strconv.Itoa(lineNumber)
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if strings.HasPrefix(line[i:], prefix) {
		i += len(prefix)
	}
	if i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[i:]
}

func (r *PlainRenderer) renderDisplayFilePreviewLocked(payload DisplayFilePayload) {
	doc := payload.Preview
	if doc.Kind != PreviewFormatKindFile {
		return
	}

	if caption := renderDisplayFileCaption(payload); caption != "" {
		r.writeStringLocked(r.decorate(ChannelTool, "  "+caption) + "\n")
	}
	for _, line := range doc.Lines {
		text := renderPreviewLineText(line)
		if text == "" {
			continue
		}
		r.writeStringLocked(r.decorate(renderPreviewChannel(line), "  "+text) + "\n")
	}
	if doc.Truncated {
		truncation := fmt.Sprintf("  output truncated after %d lines", doc.LineLimit)
		r.writeStringLocked(r.decorate(ChannelStatus, truncation) + "\n")
	}
}

func renderDisplayFileCaption(payload DisplayFilePayload) string {
	label := "display file preview"
	if payload.Preview.Language != "" && payload.Preview.Language != "plain" {
		label = fmt.Sprintf("%s · %s", label, payload.Preview.Language)
	}
	if payload.Path != "" {
		if payload.Offset > 1 && payload.Preview.StartLine > 0 {
			lineCount := previewDocumentLineCount(payload.Preview)
			return fmt.Sprintf("%s · %s · lines %d–%d", payload.Path, label, payload.Preview.StartLine, payload.Preview.StartLine+lineCount-1)
		}
		return fmt.Sprintf("%s · %s · %d lines", payload.Path, label, previewDocumentLineCount(payload.Preview))
	}
	return fmt.Sprintf("%s · %d lines", label, previewDocumentLineCount(payload.Preview))
}

func (r *PlainRenderer) renderPreviewDocumentLocked(preview ToolPreview, doc PreviewDocument) {
	if doc.Kind == "" {
		return
	}
	if caption := renderPreviewCaption(preview, doc); caption != "" {
		r.writeStringLocked(r.decorate(ChannelStatus, "  "+caption) + "\n")
	}
	for _, line := range doc.Lines {
		if doc.Kind == PreviewFormatKindEditDiff && line.Kind == PreviewLineKindHeader {
			switch strings.TrimSpace(line.Prefix) {
			case "---", "+++":
				continue
			}
		}
		if text := renderPreviewLineText(line); text != "" {
			r.writeStringLocked(r.decorate(renderPreviewChannel(line), "  "+text) + "\n")
		}
	}
}

func previewDocumentLineCount(doc PreviewDocument) int {
	count := 0
	for _, line := range doc.Lines {
		switch line.Kind {
		case PreviewLineKindText, PreviewLineKindContext, PreviewLineKindAdded, PreviewLineKindRemoved:
			count++
		}
	}
	return count
}

func renderPreviewLineText(line PreviewLine) string {
	prefix := strings.TrimSpace(line.Prefix)
	text := previewContentText(line)
	if line.Kind == PreviewLineKindHeader {
		text = strings.TrimSpace(text)
	}
	switch {
	case prefix == "":
		return text
	case text == "":
		return prefix
	default:
		return prefix + " " + text
	}
}

func previewContentText(line PreviewLine) string {
	var b strings.Builder
	for _, span := range line.Spans {
		b.WriteString(span.Text)
	}
	return b.String()
}

func renderPreviewChannel(line PreviewLine) Channel {
	switch line.Kind {
	case PreviewLineKindAdded:
		return ChannelApproval
	case PreviewLineKindRemoved:
		return ChannelError
	case PreviewLineKindHeader, PreviewLineKindTruncated:
		return ChannelStatus
	case PreviewLineKindContext, PreviewLineKindText:
		return ChannelTool
	default:
		return ChannelStatus
	}
}

// CountPreviewChanges counts added and removed lines in a diff preview document.
func CountPreviewChanges(doc PreviewDocument) (adds, removes int) {
	for _, line := range doc.Lines {
		switch line.Kind {
		case PreviewLineKindAdded:
			adds++
		case PreviewLineKindRemoved:
			removes++
		}
	}
	return adds, removes
}

func renderPreviewCaption(preview ToolPreview, doc PreviewDocument) string {
	switch doc.Kind {
	case PreviewFormatKindFile:
		label := "file preview"
		switch preview.Kind {
		case ToolPreviewKindFileWrite:
			if preview.Created {
				label = "new file preview"
			} else {
				label = "updated file contents preview"
			}
		case ToolPreviewKindReadFile:
			label = "read file preview"
			if doc.StartLine > 1 {
				lineCount := previewDocumentLineCount(doc)
				return fmt.Sprintf("%s · %s · lines %d–%d", doc.Path, label, doc.StartLine, doc.StartLine+lineCount-1)
			}
		case ToolPreviewKindFetchURL:
			label = "fetched page preview"
		case ToolPreviewKindWebSearch:
			if preview.Returned >= 0 {
				return fmt.Sprintf("search results - %d results", preview.Returned)
			}
			return "search results"
		}
		if doc.Path != "" {
			return fmt.Sprintf("%s · %s · %d lines", doc.Path, label, previewDocumentLineCount(doc))
		}
		return fmt.Sprintf("%s · %d lines", label, previewDocumentLineCount(doc))
	case PreviewFormatKindEditDiff:
		added, removed := CountPreviewChanges(doc)
		label := "edit diff"
		if doc.Path != "" {
			return fmt.Sprintf("%s · %s · +%d/-%d", doc.Path, label, added, removed)
		}
		return fmt.Sprintf("%s · +%d/-%d", label, added, removed)
	default:
		return ""
	}
}
