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
	SummaryBytes      int      `json:"summary_bytes,omitempty"`
	BudgetBytes       int      `json:"budget_bytes,omitempty"`
	UsedBytes         int      `json:"used_bytes,omitempty"`
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

func NewContextCompactionEvent(turn, retainedTurns, retainedMessages, compactedTurns, compactedMessages, summaryBytes int, truncated bool, summaryTitle string) Event {
	return NewContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:              "compaction",
		Turn:              turn,
		RetainedTurns:     retainedTurns,
		RetainedMessages:  retainedMessages,
		CompactedTurns:    compactedTurns,
		CompactedMessages: compactedMessages,
		SummaryTitle:      summaryTitle,
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

func formatContextDiagnosticsEvent(payload ContextDiagnosticsEvent) string {
	parts := []string{fmt.Sprintf("context diagnostics kind=%s", payload.Kind)}
	if payload.Scope != "" {
		parts = append(parts, fmt.Sprintf("scope=%s", payload.Scope))
	}
	if payload.Turn > 0 {
		parts = append(parts, fmt.Sprintf("turn=%d", payload.Turn))
	}
	if payload.RetainedTurns > 0 {
		parts = append(parts, fmt.Sprintf("retained_turns=%d", payload.RetainedTurns))
	}
	if payload.RetainedMessages > 0 {
		parts = append(parts, fmt.Sprintf("retained_messages=%d", payload.RetainedMessages))
	}
	if payload.CompactedTurns > 0 {
		parts = append(parts, fmt.Sprintf("compacted_turns=%d", payload.CompactedTurns))
	}
	if payload.CompactedMessages > 0 {
		parts = append(parts, fmt.Sprintf("compacted_messages=%d", payload.CompactedMessages))
	}
	if payload.SummaryTitle != "" {
		parts = append(parts, fmt.Sprintf("summary=%s", payload.SummaryTitle))
	}
	if payload.SummaryBytes > 0 {
		parts = append(parts, fmt.Sprintf("summary_bytes=%d", payload.SummaryBytes))
	}
	if payload.UsedBytes > 0 {
		parts = append(parts, fmt.Sprintf("used_bytes=%d", payload.UsedBytes))
	}
	if payload.BudgetBytes > 0 {
		parts = append(parts, fmt.Sprintf("budget_bytes=%d", payload.BudgetBytes))
	}
	if payload.Truncated {
		parts = append(parts, "truncated=true")
	}
	if len(payload.Notes) > 0 {
		parts = append(parts, fmt.Sprintf("notes=%s", strings.Join(payload.Notes, ";")))
	}
	return strings.Join(parts, " ")
}
