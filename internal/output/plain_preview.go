package output

import (
	"fmt"
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
