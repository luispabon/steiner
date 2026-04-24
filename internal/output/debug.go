package output

import (
	"fmt"
	"strings"
	"time"
)

type ContextDiagnosticsEvent struct {
	Kind              string   `json:"kind"`
	Scope             string   `json:"scope,omitempty"`
	Turn              int      `json:"turn,omitempty"`
	RetainedTurns     int      `json:"retained_turns,omitempty"`
	RetainedMessages  int      `json:"retained_messages,omitempty"`
	CompactedTurns    int      `json:"compacted_turns,omitempty"`
	CompactedMessages int      `json:"compacted_messages,omitempty"`
	SummaryTitle      string   `json:"summary_title,omitempty"`
	SummaryPreview    string   `json:"summary_preview,omitempty"`
	SummaryBytes      int      `json:"summary_bytes,omitempty"`
	BudgetBytes       int      `json:"budget_bytes,omitempty"`
	UsedBytes         int      `json:"used_bytes,omitempty"`
	PromptTokens      int      `json:"prompt_tokens,omitempty"`
	ReservedTokens    int      `json:"reserved_tokens,omitempty"`
	ContextTokens     int      `json:"context_tokens,omitempty"`
	TotalTokens       int      `json:"total_tokens,omitempty"`
	Truncated         bool     `json:"truncated,omitempty"`
	Notes             []string `json:"notes,omitempty"`
}

func NewContextDiagnosticsEvent(payload ContextDiagnosticsEvent) Event {
	if payload.Kind == "" {
		payload.Kind = "diagnostic"
	}
	return Event{
		Type:      EventTypeContextDiagnostics,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func NewContextCompactionEvent(turn, retainedTurns, retainedMessages, compactedTurns, compactedMessages, summaryBytes int, truncated bool, summaryTitle string, summaryPreview ...string) Event {
	preview := ""
	if len(summaryPreview) > 0 {
		preview = summaryPreview[0]
	}
	return NewContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:              "compaction",
		Turn:              turn,
		RetainedTurns:     retainedTurns,
		RetainedMessages:  retainedMessages,
		CompactedTurns:    compactedTurns,
		CompactedMessages: compactedMessages,
		SummaryTitle:      summaryTitle,
		SummaryPreview:    strings.TrimSpace(preview),
		SummaryBytes:      summaryBytes,
		Truncated:         truncated,
	})
}

func NewContextBudgetEvent(scope string, turn, usedBytes, budgetBytes int, truncated bool, notes ...string) Event {
	return NewContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:        "budget",
		Scope:       scope,
		Turn:        turn,
		UsedBytes:   usedBytes,
		BudgetBytes: budgetBytes,
		Truncated:   truncated,
		Notes:       append([]string(nil), notes...),
	})
}

func NewContextTokenBudgetEvent(scope string, turn, promptTokens, reservedTokens, totalTokens, contextTokens int, truncated bool, notes ...string) Event {
	return NewContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:           "budget",
		Scope:          scope,
		Turn:           turn,
		PromptTokens:   promptTokens,
		ReservedTokens: reservedTokens,
		ContextTokens:  contextTokens,
		TotalTokens:    totalTokens,
		Truncated:      truncated,
		Notes:          append([]string(nil), notes...),
	})
}

func formatContextDiagnosticsEvent(payload ContextDiagnosticsEvent) string {
	switch payload.Kind {
	case "budget":
		return formatContextBudgetSummary(payload)
	case "compaction":
		return formatContextCompactionSummary(payload)
	default:
		return formatGenericContextDiagnostics(payload)
	}
}

func formatContextBudgetSummary(payload ContextDiagnosticsEvent) string {
	scope := humanizeDiagnosticScope(payload.Scope)
	if scope == "" {
		scope = "context"
	}

	parts := []string{}
	switch {
	case payload.TotalTokens > 0 || payload.PromptTokens > 0 || payload.ReservedTokens > 0 || payload.ContextTokens > 0:
		contextTokens := payload.ContextTokens
		if contextTokens == 0 {
			contextTokens = payload.BudgetBytes
		}
		parts = append(parts, fmt.Sprintf(
			"budget %s used prompt=%d reserve=%d total=%d/%d tokens",
			scope,
			payload.PromptTokens,
			payload.ReservedTokens,
			payload.TotalTokens,
			contextTokens,
		))
	default:
		parts = append(parts, fmt.Sprintf("budget %s used %d/%d bytes", scope, payload.UsedBytes, payload.BudgetBytes))
	}
	if payload.Turn > 0 {
		parts = append(parts, fmt.Sprintf("turn %d", payload.Turn))
	}
	if payload.Truncated {
		parts = append(parts, "truncated")
	}
	if notes := joinDiagnosticNotes(payload.Notes); notes != "" {
		parts = append(parts, "notes "+notes)
	}
	return strings.Join(parts, "; ")
}

func formatContextCompactionSummary(payload ContextDiagnosticsEvent) string {
	parts := []string{
		fmt.Sprintf(
			"compaction turn %d compacted %d %s/%d %s; retained %d %s/%d %s",
			payload.Turn,
			payload.CompactedTurns,
			pluralizeDiagnosticWord(payload.CompactedTurns, "turn"),
			payload.CompactedMessages,
			pluralizeDiagnosticWord(payload.CompactedMessages, "message"),
			payload.RetainedTurns,
			pluralizeDiagnosticWord(payload.RetainedTurns, "turn"),
			payload.RetainedMessages,
			pluralizeDiagnosticWord(payload.RetainedMessages, "message"),
		),
	}

	summary := strings.TrimSpace(payload.SummaryTitle)
	if preview := strings.TrimSpace(payload.SummaryPreview); preview != "" {
		if summary != "" {
			summary += ": "
		}
		summary += preview
	}
	if summary != "" {
		parts = append(parts, fmt.Sprintf("kept summary %q", truncateDiagnosticText(summary, 160)))
	}
	if payload.SummaryBytes > 0 {
		parts = append(parts, fmt.Sprintf("summary %d bytes", payload.SummaryBytes))
	}
	if payload.Truncated {
		parts = append(parts, "summary truncated")
	}
	if notes := joinDiagnosticNotes(payload.Notes); notes != "" {
		parts = append(parts, "notes "+notes)
	}
	return strings.Join(parts, "; ")
}

func formatGenericContextDiagnostics(payload ContextDiagnosticsEvent) string {
	parts := []string{fmt.Sprintf("context diagnostics kind=%s", payload.Kind)}
	if payload.Scope != "" {
		parts = append(parts, fmt.Sprintf("scope=%s", payload.Scope))
	}
	if payload.Turn > 0 {
		parts = append(parts, fmt.Sprintf("turn=%d", payload.Turn))
	}
	if payload.Truncated {
		parts = append(parts, "truncated=true")
	}
	if len(payload.Notes) > 0 {
		parts = append(parts, fmt.Sprintf("notes=%s", strings.Join(payload.Notes, ";")))
	}
	return strings.Join(parts, " ")
}

func humanizeDiagnosticScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return ""
	}
	return strings.ReplaceAll(scope, "_", " ")
}

func joinDiagnosticNotes(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	filtered := make([]string, 0, len(notes))
	for _, note := range notes {
		note = strings.TrimSpace(note)
		if note == "" {
			continue
		}
		filtered = append(filtered, strings.ReplaceAll(note, "=", " "))
	}
	return strings.Join(filtered, ", ")
}

func truncateDiagnosticText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

func pluralizeDiagnosticWord(value int, singular string) string {
	if value == 1 {
		return singular
	}
	return singular + "s"
}
