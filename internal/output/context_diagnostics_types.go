package output

import "time"

// ContextDiagnosticsEvent is the legacy kitchen-sink diagnostic payload kept
// as a compatibility constructor/input shape while typed sub-events replace it
// as the emitted payloads.
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

// ContextCompactionEvent records context compaction progress and results.
type ContextCompactionEvent struct {
	Scope               string   `json:"scope,omitempty"`
	Turn                int      `json:"turn,omitempty"`
	Severity            string   `json:"severity,omitempty"`
	SessionState        string   `json:"session_state,omitempty"`
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

// BudgetSnapshot returns the token-budget fields of a compaction event as a
// ContextBudgetEvent, so callers can refresh the context counter once
// compaction finishes.
func (e ContextCompactionEvent) BudgetSnapshot() ContextBudgetEvent {
	return ContextBudgetEvent{
		Turn:                e.Turn,
		PromptTokens:        e.PromptTokens,
		ContextTokens:       e.ContextTokens,
		TotalTokens:         e.TotalTokens,
		ContextWindow:       e.ContextWindow,
		ContextUsagePercent: e.ContextUsagePercent,
		CompactionThreshold: e.CompactionThreshold,
		EstimatorPadTokens:  e.EstimatorPadTokens,
		Status:              e.Status,
	}
}

// ContextSessionHealthEvent records compaction-count escalation guidance.
type ContextSessionHealthEvent struct {
	Scope           string   `json:"scope,omitempty"`
	Turn            int      `json:"turn,omitempty"`
	Severity        string   `json:"severity,omitempty"`
	SessionState    string   `json:"session_state,omitempty"`
	CompactionCount int      `json:"compaction_count,omitempty"`
	RestartGuidance string   `json:"restart_guidance,omitempty"`
	Notes           []string `json:"notes,omitempty"`
}

// ContextBudgetEvent records byte-budget or token-budget diagnostics.
type ContextBudgetEvent struct {
	Scope               string   `json:"scope,omitempty"`
	Turn                int      `json:"turn,omitempty"`
	UsedBytes           int      `json:"used_bytes,omitempty"`
	BudgetBytes         int      `json:"budget_bytes,omitempty"`
	PromptTokens        int      `json:"prompt_tokens,omitempty"`
	ContextTokens       int      `json:"context_tokens,omitempty"`
	TotalTokens         int      `json:"total_tokens,omitempty"`
	Truncated           bool     `json:"truncated,omitempty"`
	ContextWindow       int      `json:"context_window,omitempty"`
	ContextUsagePercent float64  `json:"context_usage_percent,omitempty"`
	CompactionThreshold float64  `json:"compaction_threshold,omitempty"`
	EstimatorPadTokens  int      `json:"estimator_pad_tokens,omitempty"`
	Status              string   `json:"status,omitempty"`
	Notes               []string `json:"notes,omitempty"`
}

// ContextFileAnnotationEvent records contextual file-annotation hints.
type ContextFileAnnotationEvent struct {
	Scope    string   `json:"scope,omitempty"`
	Turn     int      `json:"turn,omitempty"`
	Severity string   `json:"severity,omitempty"`
	Action   string   `json:"action,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Path     string   `json:"path,omitempty"`
	Notes    []string `json:"notes,omitempty"`
}

type contextDiagnosticPayload interface {
	contextDiagnosticPayload()
	toLegacyContextDiagnostics() ContextDiagnosticsEvent
}

func (ContextDiagnosticsEvent) contextDiagnosticPayload()    {}
func (ContextCompactionEvent) contextDiagnosticPayload()     {}
func (ContextSessionHealthEvent) contextDiagnosticPayload()  {}
func (ContextBudgetEvent) contextDiagnosticPayload()         {}
func (ContextFileAnnotationEvent) contextDiagnosticPayload() {}

func (payload ContextDiagnosticsEvent) toLegacyContextDiagnostics() ContextDiagnosticsEvent {
	if payload.Kind == "" {
		payload.Kind = "diagnostic"
	}
	payload.Notes = append([]string(nil), payload.Notes...)
	return payload
}

func (payload ContextCompactionEvent) toLegacyContextDiagnostics() ContextDiagnosticsEvent {
	return ContextDiagnosticsEvent{
		Kind:                "compaction",
		Scope:               payload.Scope,
		Turn:                payload.Turn,
		Severity:            payload.Severity,
		SessionState:        payload.SessionState,
		CompactionCount:     payload.CompactionCount,
		RestartGuidance:     payload.RestartGuidance,
		RetainedTurns:       payload.RetainedTurns,
		RetainedMessages:    payload.RetainedMessages,
		CompactedTurns:      payload.CompactedTurns,
		CompactedMessages:   payload.CompactedMessages,
		SummaryTitle:        payload.SummaryTitle,
		SummaryPreview:      payload.SummaryPreview,
		SummaryText:         payload.SummaryText,
		SummaryBytes:        payload.SummaryBytes,
		PromptTokens:        payload.PromptTokens,
		ContextTokens:       payload.ContextTokens,
		TotalTokens:         payload.TotalTokens,
		Truncated:           payload.Truncated,
		ContextWindow:       payload.ContextWindow,
		ContextUsagePercent: payload.ContextUsagePercent,
		CompactionThreshold: payload.CompactionThreshold,
		EstimatorPadTokens:  payload.EstimatorPadTokens,
		Status:              payload.Status,
		Mode:                payload.Mode,
		BeforePromptTokens:  payload.BeforePromptTokens,
		BeforeUsagePercent:  payload.BeforeUsagePercent,
		AfterPromptTokens:   payload.AfterPromptTokens,
		AfterUsagePercent:   payload.AfterUsagePercent,
		RetainedRawTurns:    payload.RetainedRawTurns,
		SummaryTokenBudget:  payload.SummaryTokenBudget,
		ThresholdAchieved:   payload.ThresholdAchieved,
		Notes:               append([]string(nil), payload.Notes...),
	}
}

func (payload ContextSessionHealthEvent) toLegacyContextDiagnostics() ContextDiagnosticsEvent {
	return ContextDiagnosticsEvent{
		Kind:            "session_health",
		Scope:           payload.Scope,
		Turn:            payload.Turn,
		Severity:        payload.Severity,
		SessionState:    payload.SessionState,
		CompactionCount: payload.CompactionCount,
		RestartGuidance: payload.RestartGuidance,
		Notes:           append([]string(nil), payload.Notes...),
	}
}

func (payload ContextBudgetEvent) toLegacyContextDiagnostics() ContextDiagnosticsEvent {
	return ContextDiagnosticsEvent{
		Kind:                "budget",
		Scope:               payload.Scope,
		Turn:                payload.Turn,
		BudgetBytes:         payload.BudgetBytes,
		UsedBytes:           payload.UsedBytes,
		PromptTokens:        payload.PromptTokens,
		ContextTokens:       payload.ContextTokens,
		TotalTokens:         payload.TotalTokens,
		Truncated:           payload.Truncated,
		ContextWindow:       payload.ContextWindow,
		ContextUsagePercent: payload.ContextUsagePercent,
		CompactionThreshold: payload.CompactionThreshold,
		EstimatorPadTokens:  payload.EstimatorPadTokens,
		Status:              payload.Status,
		Notes:               append([]string(nil), payload.Notes...),
	}
}

func (payload ContextFileAnnotationEvent) toLegacyContextDiagnostics() ContextDiagnosticsEvent {
	return ContextDiagnosticsEvent{
		Kind:     "file_annotation",
		Scope:    payload.Scope,
		Turn:     payload.Turn,
		Severity: payload.Severity,
		Action:   payload.Action,
		Reason:   payload.Reason,
		Path:     payload.Path,
		Notes:    append([]string(nil), payload.Notes...),
	}
}

func contextDiagnosticEvent(payload contextDiagnosticPayload) Event {
	return Event{
		Type:      EventTypeContextDiagnostics,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func contextDiagnosticFromLegacy(payload ContextDiagnosticsEvent) contextDiagnosticPayload {
	payload = payload.toLegacyContextDiagnostics()
	switch payload.Kind {
	case "compaction":
		return ContextCompactionEvent{
			Scope:               payload.Scope,
			Turn:                payload.Turn,
			Severity:            payload.Severity,
			SessionState:        payload.SessionState,
			CompactionCount:     payload.CompactionCount,
			RestartGuidance:     payload.RestartGuidance,
			RetainedTurns:       payload.RetainedTurns,
			RetainedMessages:    payload.RetainedMessages,
			CompactedTurns:      payload.CompactedTurns,
			CompactedMessages:   payload.CompactedMessages,
			SummaryTitle:        payload.SummaryTitle,
			SummaryPreview:      payload.SummaryPreview,
			SummaryText:         payload.SummaryText,
			SummaryBytes:        payload.SummaryBytes,
			PromptTokens:        payload.PromptTokens,
			ContextTokens:       payload.ContextTokens,
			TotalTokens:         payload.TotalTokens,
			Truncated:           payload.Truncated,
			ContextWindow:       payload.ContextWindow,
			ContextUsagePercent: payload.ContextUsagePercent,
			CompactionThreshold: payload.CompactionThreshold,
			EstimatorPadTokens:  payload.EstimatorPadTokens,
			Status:              payload.Status,
			Mode:                payload.Mode,
			BeforePromptTokens:  payload.BeforePromptTokens,
			BeforeUsagePercent:  payload.BeforeUsagePercent,
			AfterPromptTokens:   payload.AfterPromptTokens,
			AfterUsagePercent:   payload.AfterUsagePercent,
			RetainedRawTurns:    payload.RetainedRawTurns,
			SummaryTokenBudget:  payload.SummaryTokenBudget,
			ThresholdAchieved:   payload.ThresholdAchieved,
			Notes:               append([]string(nil), payload.Notes...),
		}
	case "session_health":
		return ContextSessionHealthEvent{
			Scope:           payload.Scope,
			Turn:            payload.Turn,
			Severity:        payload.Severity,
			SessionState:    payload.SessionState,
			CompactionCount: payload.CompactionCount,
			RestartGuidance: payload.RestartGuidance,
			Notes:           append([]string(nil), payload.Notes...),
		}
	case "budget", "session_loaded":
		return ContextBudgetEvent{
			Scope:               payload.Scope,
			Turn:                payload.Turn,
			UsedBytes:           payload.UsedBytes,
			BudgetBytes:         payload.BudgetBytes,
			PromptTokens:        payload.PromptTokens,
			ContextTokens:       payload.ContextTokens,
			TotalTokens:         payload.TotalTokens,
			Truncated:           payload.Truncated,
			ContextWindow:       payload.ContextWindow,
			ContextUsagePercent: payload.ContextUsagePercent,
			CompactionThreshold: payload.CompactionThreshold,
			EstimatorPadTokens:  payload.EstimatorPadTokens,
			Status:              payload.Status,
			Notes:               append([]string(nil), payload.Notes...),
		}
	case "file_annotation":
		return ContextFileAnnotationEvent{
			Scope:    payload.Scope,
			Turn:     payload.Turn,
			Severity: payload.Severity,
			Action:   payload.Action,
			Reason:   payload.Reason,
			Path:     payload.Path,
			Notes:    append([]string(nil), payload.Notes...),
		}
	default:
		return payload
	}
}

// CloneContextDiagnosticPayload returns a deep-enough clone for diagnostic
// payloads that carry slices.
func CloneContextDiagnosticPayload(payload any) (any, bool) {
	diag, ok := payload.(contextDiagnosticPayload)
	if !ok {
		return nil, false
	}
	return contextDiagnosticFromLegacy(diag.toLegacyContextDiagnostics()), true
}

// ContextDiagnosticKind returns the logical diagnostic kind for typed or legacy
// context diagnostic payloads.
func ContextDiagnosticKind(payload any) string {
	diag, ok := payload.(contextDiagnosticPayload)
	if !ok {
		return ""
	}
	return diag.toLegacyContextDiagnostics().Kind
}

// ContextDiagnosticSeverity returns the severity string for typed or legacy
// context diagnostic payloads.
func ContextDiagnosticSeverity(payload any) string {
	diag, ok := payload.(contextDiagnosticPayload)
	if !ok {
		return ""
	}
	return diag.toLegacyContextDiagnostics().Severity
}

// ContextDiagnosticNotes returns a copied notes slice for typed or legacy
// context diagnostic payloads.
func ContextDiagnosticNotes(payload any) []string {
	diag, ok := payload.(contextDiagnosticPayload)
	if !ok {
		return nil
	}
	return append([]string(nil), diag.toLegacyContextDiagnostics().Notes...)
}

// ContextDiagnosticReason returns the reason field for typed or legacy context
// diagnostic payloads.
func ContextDiagnosticReason(payload any) string {
	diag, ok := payload.(contextDiagnosticPayload)
	if !ok {
		return ""
	}
	return diag.toLegacyContextDiagnostics().Reason
}

// AsContextCompactionEvent converts typed or legacy payloads into a compaction
// diagnostic event when applicable.
func AsContextCompactionEvent(payload any) (ContextCompactionEvent, bool) {
	diag, ok := payload.(contextDiagnosticPayload)
	if !ok {
		return ContextCompactionEvent{}, false
	}
	legacy := diag.toLegacyContextDiagnostics()
	if legacy.Kind != "compaction" {
		return ContextCompactionEvent{}, false
	}
	typed, ok := contextDiagnosticFromLegacy(legacy).(ContextCompactionEvent)
	return typed, ok
}

// AsContextSessionHealthEvent converts typed or legacy payloads into a session
// health diagnostic event when applicable.
func AsContextSessionHealthEvent(payload any) (ContextSessionHealthEvent, bool) {
	diag, ok := payload.(contextDiagnosticPayload)
	if !ok {
		return ContextSessionHealthEvent{}, false
	}
	legacy := diag.toLegacyContextDiagnostics()
	if legacy.Kind != "session_health" {
		return ContextSessionHealthEvent{}, false
	}
	typed, ok := contextDiagnosticFromLegacy(legacy).(ContextSessionHealthEvent)
	return typed, ok
}

// AsContextBudgetEvent converts typed or legacy payloads into a budget
// diagnostic event when applicable.
func AsContextBudgetEvent(payload any) (ContextBudgetEvent, bool) {
	diag, ok := payload.(contextDiagnosticPayload)
	if !ok {
		return ContextBudgetEvent{}, false
	}
	legacy := diag.toLegacyContextDiagnostics()
	if legacy.Kind != "budget" && legacy.Kind != "session_loaded" {
		return ContextBudgetEvent{}, false
	}
	typed, ok := contextDiagnosticFromLegacy(legacy).(ContextBudgetEvent)
	return typed, ok
}

// AsContextFileAnnotationEvent converts typed or legacy payloads into a file
// annotation diagnostic event when applicable.
func AsContextFileAnnotationEvent(payload any) (ContextFileAnnotationEvent, bool) {
	diag, ok := payload.(contextDiagnosticPayload)
	if !ok {
		return ContextFileAnnotationEvent{}, false
	}
	legacy := diag.toLegacyContextDiagnostics()
	if legacy.Kind != "file_annotation" {
		return ContextFileAnnotationEvent{}, false
	}
	typed, ok := contextDiagnosticFromLegacy(legacy).(ContextFileAnnotationEvent)
	return typed, ok
}
