package output

import (
	"fmt"
	"strings"
	"time"
)

// ContextDiagnosticsEvent records context-management diagnostics for logs and UI.
type ContextDiagnosticsEvent struct {
	Kind                string   `json:"kind"`
	Scope               string   `json:"scope,omitempty"`
	Turn                int      `json:"turn,omitempty"`
	Severity            string   `json:"severity,omitempty"`
	SessionState        string   `json:"session_state,omitempty"`
	Action              string   `json:"action,omitempty"`
	Reason              string   `json:"reason,omitempty"`
	Tool                string   `json:"tool,omitempty"`
	Path                string   `json:"path,omitempty"`
	Window              int      `json:"window,omitempty"`
	Parsed              bool     `json:"parsed,omitempty"`
	Failures            int      `json:"failures,omitempty"`
	CompactionCount     int      `json:"compaction_count,omitempty"`
	RestartGuidance     string   `json:"restart_guidance,omitempty"`
	RetainedTurns       int      `json:"retained_turns,omitempty"`
	RetainedMessages    int      `json:"retained_messages,omitempty"`
	CompactedTurns      int      `json:"compacted_turns,omitempty"`
	CompactedMessages   int      `json:"compacted_messages,omitempty"`
	SummaryTitle        string   `json:"summary_title,omitempty"`
	SummaryPreview      string   `json:"summary_preview,omitempty"`
	SummaryText         string   `json:"summary_text,omitempty"`
	SummaryBytes        int      `json:"summary_bytes,omitempty"`
	BudgetBytes         int      `json:"budget_bytes,omitempty"`
	UsedBytes           int      `json:"used_bytes,omitempty"`
	PromptTokens        int      `json:"prompt_tokens,omitempty"`
	ContextTokens       int      `json:"context_tokens,omitempty"`
	TotalTokens         int      `json:"total_tokens,omitempty"`
	Truncated           bool     `json:"truncated,omitempty"`
	ContextWindow       int      `json:"context_window,omitempty"`
	ContextUsagePercent float64  `json:"context_usage_percent,omitempty"`
	CompactionThreshold float64  `json:"compaction_threshold,omitempty"`
	EstimatorPadTokens  int      `json:"estimator_pad_tokens,omitempty"`
	Status              string   `json:"status,omitempty"`
	Mode                string   `json:"mode,omitempty"`
	BeforePromptTokens  int      `json:"before_prompt_tokens,omitempty"`
	BeforeUsagePercent  float64  `json:"before_usage_percent,omitempty"`
	AfterPromptTokens   int      `json:"after_prompt_tokens,omitempty"`
	AfterUsagePercent   float64  `json:"after_usage_percent,omitempty"`
	RetainedRawTurns    int      `json:"retained_raw_turns,omitempty"`
	SummaryTokenBudget  int      `json:"summary_token_budget,omitempty"`
	ThresholdAchieved   bool     `json:"threshold_achieved,omitempty"`
	Notes               []string `json:"notes,omitempty"`
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
func NewContextTokenBudgetEvent(scope string, turn, promptTokens, contextWindow int, contextUsagePercent, compactionThreshold float64, estimatorPadTokens, totalTokens int, status string, truncated bool, notes ...string) Event {
	return NewContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:                "budget",
		Scope:               scope,
		Turn:                turn,
		PromptTokens:        promptTokens,
		ContextWindow:       contextWindow,
		ContextUsagePercent: contextUsagePercent,
		CompactionThreshold: compactionThreshold,
		EstimatorPadTokens:  estimatorPadTokens,
		ContextTokens:       contextWindow,
		TotalTokens:         totalTokens,
		Status:              status,
		Truncated:           truncated,
		Notes:               append([]string(nil), notes...),
	})
}

// NewFileAnnotationEvent creates a new file annotation diagnostic event.
func NewFileAnnotationEvent(turn int, path, action, reason string, previousTurn int, notes ...string) Event {
	eventNotes := append([]string(nil), notes...)
	if previousTurn > 0 {
		eventNotes = append(eventNotes, fmt.Sprintf("previous_turn=%d", previousTurn))
	}
	return NewContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:     "file_annotation",
		Scope:    "read",
		Turn:     turn,
		Severity: "info",
		Action:   action,
		Reason:   reason,
		Path:     path,
		Notes:    eventNotes,
	})
}

func formatContextDiagnosticsEvent(payload ContextDiagnosticsEvent) string {
	switch payload.Kind {
	case "budget":
		return formatContextBudgetSummary(payload)
	case "file_annotation":
		return formatContextFileAnnotationSummary(payload)
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
	case payload.ContextWindow > 0 || payload.PromptTokens > 0 || payload.ContextUsagePercent > 0 || payload.CompactionThreshold > 0 || payload.EstimatorPadTokens > 0 || payload.Status != "":
		status := strings.TrimSpace(payload.Status)
		if status == "" {
			status = "unknown_context"
		}
		parts = append(parts, fmt.Sprintf(
			"budget %s prompt_tokens=%d context_window=%d context_usage_percent=%s compaction_threshold=%s estimator_pad_tokens=%d status=%s",
			scope,
			payload.PromptTokens,
			payload.ContextWindow,
			formatPercent(payload.ContextUsagePercent),
			formatPercent(payload.CompactionThreshold),
			payload.EstimatorPadTokens,
			status,
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

func formatContextFileAnnotationSummary(payload ContextDiagnosticsEvent) string {
	parts := []string{formatDiagnosticHeadline(payload, "file annotation")}
	if action := strings.TrimSpace(payload.Action); action != "" {
		parts = append(parts, action)
	}
	if payload.Path != "" {
		parts = append(parts, fmt.Sprintf("path=%s", payload.Path))
	}
	if reason := strings.TrimSpace(payload.Reason); reason != "" {
		parts = append(parts, fmt.Sprintf("reason=%s", reason))
	}
	if notes := joinDiagnosticNotes(payload.Notes); notes != "" {
		parts = append(parts, "notes "+notes)
	}
	return strings.Join(parts, "; ")
}

func formatContextCompactionSummary(payload ContextDiagnosticsEvent) string {
	parts := []string{formatDiagnosticHeadline(payload, "compaction")}
	parts = append(parts, formatDiagnosticEscalation(payload)...)
	parts = append(parts, formatCompactionMode(payload)...)
	parts = append(parts, formatCompactionUsage(payload)...)
	parts = append(parts, formatCompactionBudget(payload)...)
	parts = append(parts, formatCompactionSummaryDetails(payload)...)
	if notes := joinDiagnosticNotes(payload.Notes); notes != "" {
		parts = append(parts, "notes "+notes)
	}
	return strings.Join(parts, "; ")
}

func formatCompactionMode(payload ContextDiagnosticsEvent) []string {
	mode := strings.TrimSpace(payload.Mode)
	if mode == "" {
		return nil
	}
	return []string{fmt.Sprintf("mode=%s", mode)}
}

func formatCompactionUsage(payload ContextDiagnosticsEvent) []string {
	if payload.BeforePromptTokens <= 0 && payload.BeforeUsagePercent <= 0 && payload.AfterPromptTokens <= 0 && payload.AfterUsagePercent <= 0 {
		return nil
	}
	return []string{
		fmt.Sprintf(
			"before prompt_tokens=%d context_usage_percent=%s",
			payload.BeforePromptTokens,
			formatPercent(payload.BeforeUsagePercent),
		),
		fmt.Sprintf(
			"after prompt_tokens=%d context_usage_percent=%s",
			payload.AfterPromptTokens,
			formatPercent(payload.AfterUsagePercent),
		),
	}
}

func formatCompactionBudget(payload ContextDiagnosticsEvent) []string {
	parts := make([]string, 0, 3)
	if payload.RetainedRawTurns > 0 {
		parts = append(parts, fmt.Sprintf("retained raw turns=%d", payload.RetainedRawTurns))
	}
	if payload.SummaryTokenBudget > 0 {
		parts = append(parts, fmt.Sprintf("summary token budget=%d", payload.SummaryTokenBudget))
	}
	if payload.Mode != "" || payload.SummaryTokenBudget > 0 || payload.ContextWindow > 0 || payload.CompactionThreshold > 0 {
		parts = append(parts, fmt.Sprintf("threshold achieved=%t", payload.ThresholdAchieved))
	}
	return parts
}

func formatCompactionSummaryDetails(payload ContextDiagnosticsEvent) []string {
	parts := make([]string, 0, 3)
	if summary := compactionSummaryPreview(payload); summary != "" {
		parts = append(parts, fmt.Sprintf("kept summary %q", TruncateWithEllipsis(summary, 160)))
	}
	if payload.SummaryBytes > 0 {
		parts = append(parts, fmt.Sprintf("summary %d bytes", payload.SummaryBytes))
	}
	if payload.Truncated {
		parts = append(parts, "summary truncated")
	}
	return parts
}

func compactionSummaryPreview(payload ContextDiagnosticsEvent) string {
	summary := strings.TrimSpace(payload.SummaryTitle)
	preview := strings.TrimSpace(payload.SummaryPreview)
	switch {
	case summary != "" && preview != "":
		return summary + ": " + preview
	case summary != "":
		return summary
	default:
		return preview
	}
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

func formatPercent(value float64) string {
	return fmt.Sprintf("%.0f%%", value)
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
