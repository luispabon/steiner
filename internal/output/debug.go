package output

import (
	"fmt"
	"strings"
	"time"
)

type ContextDiagnosticsEvent struct {
	Kind               string   `json:"kind"`
	Scope              string   `json:"scope,omitempty"`
	Turn               int      `json:"turn,omitempty"`
	Severity           string   `json:"severity,omitempty"`
	SessionState       string   `json:"session_state,omitempty"`
	CompactionCount    int      `json:"compaction_count,omitempty"`
	RestartGuidance    string   `json:"restart_guidance,omitempty"`
	RetainedTurns      int      `json:"retained_turns,omitempty"`
	RetainedMessages   int      `json:"retained_messages,omitempty"`
	CompactedTurns     int      `json:"compacted_turns,omitempty"`
	CompactedMessages  int      `json:"compacted_messages,omitempty"`
	SummaryTitle       string   `json:"summary_title,omitempty"`
	SummaryPreview     string   `json:"summary_preview,omitempty"`
	SummaryBytes       int      `json:"summary_bytes,omitempty"`
	BudgetBytes        int      `json:"budget_bytes,omitempty"`
	UsedBytes          int      `json:"used_bytes,omitempty"`
	PromptTokens       int      `json:"prompt_tokens,omitempty"`
	ReservedTokens     int      `json:"reserved_tokens,omitempty"`
	SafetyMarginTokens int      `json:"safety_margin_tokens,omitempty"`
	ContextTokens      int      `json:"context_tokens,omitempty"`
	TotalTokens        int      `json:"total_tokens,omitempty"`
	Truncated          bool     `json:"truncated,omitempty"`
	Notes              []string `json:"notes,omitempty"`
}

// NewContextDiagnosticsEvent creates a new context diagnostics event.
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

// NewContextCompactionEvent creates a new context compaction event.
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

// NewContextSessionHealthEvent creates a new context session health event.
func NewContextSessionHealthEvent(scope string, turn, compactionCount int, severity, sessionState, restartGuidance string, notes ...string) Event {
	return NewContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:            "session_health",
		Scope:           scope,
		Turn:            turn,
		Severity:        severity,
		SessionState:    sessionState,
		CompactionCount: compactionCount,
		RestartGuidance: restartGuidance,
		Notes:           append([]string(nil), notes...),
	})
}

// NewContextBudgetEvent creates a new context budget event.
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

// NewContextTokenBudgetEvent creates a new context token budget event.
func NewContextTokenBudgetEvent(scope string, turn, promptTokens, reservedTokens, safetyMarginTokens, totalTokens, contextTokens int, truncated bool, notes ...string) Event {
	return NewContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:               "budget",
		Scope:              scope,
		Turn:               turn,
		PromptTokens:       promptTokens,
		ReservedTokens:     reservedTokens,
		SafetyMarginTokens: safetyMarginTokens,
		ContextTokens:      contextTokens,
		TotalTokens:        totalTokens,
		Truncated:          truncated,
		Notes:              append([]string(nil), notes...),
	})
}

func formatContextDiagnosticsEvent(payload ContextDiagnosticsEvent) string {
	switch payload.Kind {
	case "budget":
		return formatContextBudgetSummary(payload)
	case "compaction":
		return formatContextCompactionSummary(payload)
	case "session_health":
		return formatContextSessionHealthSummary(payload)
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
	parts = append(parts, formatDiagnosticEscalation(payload)...)
	switch {
	case payload.TotalTokens > 0 || payload.PromptTokens > 0 || payload.ReservedTokens > 0 || payload.SafetyMarginTokens > 0 || payload.ContextTokens > 0:
		contextTokens := payload.ContextTokens
		if contextTokens == 0 {
			contextTokens = payload.BudgetBytes
		}
		parts = append(parts, fmt.Sprintf(
			"budget %s prompt=%d reserve=%d safety=%d budget=%d/%d tokens",
			scope,
			payload.PromptTokens,
			payload.ReservedTokens,
			payload.SafetyMarginTokens,
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
	parts := []string{formatDiagnosticHeadline(payload, "compaction")}
	parts = append(parts, formatDiagnosticEscalation(payload)...)

	summary := strings.TrimSpace(payload.SummaryTitle)
	if preview := strings.TrimSpace(payload.SummaryPreview); preview != "" {
		if summary != "" {
			summary += ": "
		}
		summary += preview
	}
	if summary != "" {
		parts = append(parts, fmt.Sprintf("kept summary %q", TruncateWithEllipsis(summary, 160)))
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

func formatContextSessionHealthSummary(payload ContextDiagnosticsEvent) string {
	parts := []string{formatDiagnosticHeadline(payload, "session health")}
	parts = append(parts, formatDiagnosticEscalation(payload)...)
	if payload.CompactionCount > 0 {
		parts = append(parts, fmt.Sprintf("after %d compaction%s", payload.CompactionCount, PluralSuffix(payload.CompactionCount, "", "s")))
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
	parts = append(parts, formatDiagnosticEscalation(payload)...)
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

func formatDiagnosticHeadline(payload ContextDiagnosticsEvent, subject string) string {
	parts := make([]string, 0, 5)
	if severity := strings.TrimSpace(payload.Severity); severity != "" {
		parts = append(parts, severity+":")
	}
	parts = append(parts, subject)
	if payload.CompactionCount > 0 {
		parts = append(parts, fmt.Sprintf("#%d", payload.CompactionCount))
	}
	if payload.Turn > 0 {
		parts = append(parts, fmt.Sprintf("turn %d", payload.Turn))
	}
	if subject == "compaction" {
		parts = append(parts, fmt.Sprintf(
			"compacted %d %s/%d %s; retained %d %s/%d %s",
			payload.CompactedTurns,
			PluralSuffix(payload.CompactedTurns, "turn", "turns"),
			payload.CompactedMessages,
			PluralSuffix(payload.CompactedMessages, "message", "messages"),
			payload.RetainedTurns,
			PluralSuffix(payload.RetainedTurns, "turn", "turns"),
			payload.RetainedMessages,
			PluralSuffix(payload.RetainedMessages, "message", "messages"),
		))
	}
	return strings.Join(parts, " ")
}

func formatDiagnosticEscalation(payload ContextDiagnosticsEvent) []string {
	parts := make([]string, 0, 3)
	if state := strings.TrimSpace(payload.SessionState); state != "" {
		parts = append(parts, "state "+state)
	}
	if guidance := strings.TrimSpace(payload.RestartGuidance); guidance != "" {
		parts = append(parts, guidance)
	}
	if payload.Kind != "session_health" && payload.CompactionCount > 0 {
		parts = append(parts, fmt.Sprintf("compactions %d", payload.CompactionCount))
	}
	return parts
}
