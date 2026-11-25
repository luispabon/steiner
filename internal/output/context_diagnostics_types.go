package output

import (
	"fmt"
	"strings"
	"time"
)

// SessionHealthScopeSave is the Scope of a session_health diagnostic that
// reports a failed session save.
const SessionHealthScopeSave = "session_save"

// SessionHealthScopeSandboxTmp is the Scope of a session_health diagnostic that
// reports the sandbox /tmp directory being lost and recreated mid-session.
const SessionHealthScopeSandboxTmp = "sandbox_tmp"

// CompactionFallbackUsed is the FallbackStatus of a compaction that ran on the fallback model.
const CompactionFallbackUsed = "used"

// CompactionFallbackSkipped is the FallbackStatus of a compaction that did not use the fallback model.
const CompactionFallbackSkipped = "skipped"

// CompactionResultFailed is the CompactionResult of a compaction that failed
// and adopted no state. It is distinct from the token budget Status.
const CompactionResultFailed = "failed"

// Compaction request-block statuses. RequestBlockNew reports the verbatim latest
// user request taken from the summarized source, RequestBlockCarried reports one
// carried forward from the prior summary, and RequestBlockNone reports neither.
// RequestBlockDropped reports a block removed so the compaction could fit: the
// summary applies, the verbatim request does not.
const (
	RequestBlockNew     = "new"
	RequestBlockCarried = "carried"
	RequestBlockNone    = "none"
	RequestBlockDropped = "dropped"
)

// ContextDiagnosticsEvent is the legacy kitchen-sink diagnostic payload kept
// as a compatibility constructor/input shape while typed sub-events replace it
// as the emitted payloads.
type ContextDiagnosticsEvent struct {
	Kind                      string   `json:"kind"`
	Scope                     string   `json:"scope,omitempty"`
	Turn                      int      `json:"turn,omitempty"`
	Severity                  string   `json:"severity,omitempty"`
	SessionState              string   `json:"session_state,omitempty"`
	Action                    string   `json:"action,omitempty"`
	Reason                    string   `json:"reason,omitempty"`
	Path                      string   `json:"path,omitempty"`
	CompactionCount           int      `json:"compaction_count,omitempty"`
	RestartGuidance           string   `json:"restart_guidance,omitempty"`
	SummarizedMessages        int      `json:"summarized_messages,omitempty"`
	RetainedMessages          int      `json:"retained_messages,omitempty"`
	RetainedUnits             int      `json:"retained_units,omitempty"`
	RetainedUserMessages      int      `json:"retained_user_messages,omitempty"`
	RetainedSubAgentResults   int      `json:"retained_sub_agent_results,omitempty"`
	RequestBlock              string   `json:"request_block,omitempty"`
	RequestBlockBytes         int      `json:"request_block_bytes,omitempty"`
	SummaryTitle              string   `json:"summary_title,omitempty"`
	SummaryPreview            string   `json:"summary_preview,omitempty"`
	SummaryText               string   `json:"summary_text,omitempty"`
	SummaryBytes              int      `json:"summary_bytes,omitempty"`
	BudgetBytes               int      `json:"budget_bytes,omitempty"`
	UsedBytes                 int      `json:"used_bytes,omitempty"`
	CacheReadTokens           int      `json:"cache_read_tokens,omitempty"`
	InputTokens               int      `json:"input_tokens,omitempty"`
	CacheCreateTokens         int      `json:"cache_create_tokens,omitempty"`
	CacheReportedInputTokens  int      `json:"cache_reported_input_tokens,omitempty"`
	PromptTokens              int      `json:"prompt_tokens,omitempty"`
	RawPromptTokens           int      `json:"raw_prompt_tokens,omitempty"`
	ContextTokens             int      `json:"context_tokens,omitempty"`
	TotalTokens               int      `json:"total_tokens,omitempty"`
	Truncated                 bool     `json:"truncated,omitempty"`
	ContextWindow             int      `json:"context_window,omitempty"`
	ContextUsagePercent       float64  `json:"context_usage_percent,omitempty"`
	CompactionThreshold       float64  `json:"compaction_threshold,omitempty"`
	CompactionThresholdTokens int      `json:"compaction_threshold_tokens,omitempty"`
	EstimatorPadTokens        int      `json:"estimator_pad_tokens,omitempty"`
	Status                    string   `json:"status,omitempty"`
	Mode                      string   `json:"mode,omitempty"`
	BeforePromptTokens        int      `json:"before_prompt_tokens,omitempty"`
	BeforeRawPromptTokens     int      `json:"before_raw_prompt_tokens,omitempty"`
	BeforeUsagePercent        float64  `json:"before_usage_percent,omitempty"`
	AfterPromptTokens         int      `json:"after_prompt_tokens,omitempty"`
	AfterRawPromptTokens      int      `json:"after_raw_prompt_tokens,omitempty"`
	AfterUsagePercent         float64  `json:"after_usage_percent,omitempty"`
	SummaryTokenBudget        int      `json:"summary_token_budget,omitempty"`
	ThresholdAchieved         bool     `json:"threshold_achieved,omitempty"`
	CompactionModel           string   `json:"compaction_model,omitempty"`
	CompactionModelAlias      string   `json:"compaction_model_alias,omitempty"`
	FallbackStatus            string   `json:"fallback_status,omitempty"`
	FallbackReason            string   `json:"fallback_reason,omitempty"`
	CompactionResult          string   `json:"compaction_result,omitempty"`
	SummaryAttempts           int      `json:"summary_attempts,omitempty"`
	SummaryFailureReason      string   `json:"summary_failure_reason,omitempty"`
	SummaryTruncated          bool     `json:"summary_truncated,omitempty"`
	SummaryCondensed          bool     `json:"summary_condensed,omitempty"`
	ProviderErrorClass        string   `json:"provider_error_class,omitempty"`
	HTTPStatus                int      `json:"http_status,omitempty"`
	SummaryToolCalls          int      `json:"summary_tool_calls,omitempty"`
	Notes                     []string `json:"notes,omitempty"`

	// Stages sizes each compaction stage that applied, plus a failed stage when the
	// compaction could not run.
	Stages []ContextCompactionStage `json:"stages,omitempty"`

	// SummaryAttemptDetails lists each summary call of a failed compaction stage, in order.
	SummaryAttemptDetails []CompactionSummaryAttempt `json:"summary_attempt_details,omitempty"`
}

// ContextCompactionEvent records context compaction progress and results.
type ContextCompactionEvent struct {
	Scope                     string   `json:"scope,omitempty"`
	Turn                      int      `json:"turn,omitempty"`
	Severity                  string   `json:"severity,omitempty"`
	SessionState              string   `json:"session_state,omitempty"`
	CompactionCount           int      `json:"compaction_count,omitempty"`
	RestartGuidance           string   `json:"restart_guidance,omitempty"`
	SummarizedMessages        int      `json:"summarized_messages,omitempty"`
	RetainedMessages          int      `json:"retained_messages,omitempty"`
	RetainedUnits             int      `json:"retained_units,omitempty"`
	RetainedUserMessages      int      `json:"retained_user_messages,omitempty"`
	RetainedSubAgentResults   int      `json:"retained_sub_agent_results,omitempty"`
	RequestBlock              string   `json:"request_block,omitempty"`
	RequestBlockBytes         int      `json:"request_block_bytes,omitempty"`
	SummaryTitle              string   `json:"summary_title,omitempty"`
	SummaryPreview            string   `json:"summary_preview,omitempty"`
	SummaryText               string   `json:"summary_text,omitempty"`
	SummaryBytes              int      `json:"summary_bytes,omitempty"`
	CacheReadTokens           int      `json:"cache_read_tokens,omitempty"`
	InputTokens               int      `json:"input_tokens,omitempty"`
	CacheCreateTokens         int      `json:"cache_create_tokens,omitempty"`
	CacheReportedInputTokens  int      `json:"cache_reported_input_tokens,omitempty"`
	PromptTokens              int      `json:"prompt_tokens,omitempty"`
	RawPromptTokens           int      `json:"raw_prompt_tokens,omitempty"`
	ContextTokens             int      `json:"context_tokens,omitempty"`
	TotalTokens               int      `json:"total_tokens,omitempty"`
	Truncated                 bool     `json:"truncated,omitempty"`
	ContextWindow             int      `json:"context_window,omitempty"`
	ContextUsagePercent       float64  `json:"context_usage_percent,omitempty"`
	CompactionThreshold       float64  `json:"compaction_threshold,omitempty"`
	CompactionThresholdTokens int      `json:"compaction_threshold_tokens,omitempty"`
	EstimatorPadTokens        int      `json:"estimator_pad_tokens,omitempty"`
	Status                    string   `json:"status,omitempty"`
	Mode                      string   `json:"mode,omitempty"`
	BeforePromptTokens        int      `json:"before_prompt_tokens,omitempty"`
	BeforeRawPromptTokens     int      `json:"before_raw_prompt_tokens,omitempty"`
	BeforeUsagePercent        float64  `json:"before_usage_percent,omitempty"`
	AfterPromptTokens         int      `json:"after_prompt_tokens,omitempty"`
	AfterRawPromptTokens      int      `json:"after_raw_prompt_tokens,omitempty"`
	AfterUsagePercent         float64  `json:"after_usage_percent,omitempty"`
	SummaryTokenBudget        int      `json:"summary_token_budget,omitempty"`
	ThresholdAchieved         bool     `json:"threshold_achieved,omitempty"`
	CompactionModel           string   `json:"compaction_model,omitempty"`
	CompactionModelAlias      string   `json:"compaction_model_alias,omitempty"`
	FallbackStatus            string   `json:"fallback_status,omitempty"`
	FallbackReason            string   `json:"fallback_reason,omitempty"`
	CompactionResult          string   `json:"compaction_result,omitempty"`
	SummaryAttempts           int      `json:"summary_attempts,omitempty"`
	SummaryFailureReason      string   `json:"summary_failure_reason,omitempty"`
	SummaryTruncated          bool     `json:"summary_truncated,omitempty"`
	SummaryCondensed          bool     `json:"summary_condensed,omitempty"`
	ProviderErrorClass        string   `json:"provider_error_class,omitempty"`
	HTTPStatus                int      `json:"http_status,omitempty"`
	SummaryToolCalls          int      `json:"summary_tool_calls,omitempty"`
	Notes                     []string `json:"notes,omitempty"`

	// Stages sizes each compaction stage that applied, plus a failed stage when the
	// compaction could not run.
	Stages []ContextCompactionStage `json:"stages,omitempty"`

	// SummaryAttemptDetails lists each summary call of a failed compaction stage, in order.
	SummaryAttemptDetails []CompactionSummaryAttempt `json:"summary_attempt_details,omitempty"`
}

// CompactionSummaryAttempt records one summary call of a compaction stage as the
// provider reported it, with the classification the stage gave the reply. Reason
// is empty when the reply carried summary text. CompletionTokens is logged as
// reported and never used to classify the reply, because some providers exclude
// reasoning tokens from it. It is nil when the provider reported no usage.
type CompactionSummaryAttempt struct {
	Attempt          int    `json:"attempt"`
	FinishReason     string `json:"finish_reason,omitempty"`
	CompletionTokens *int   `json:"completion_tokens,omitempty"`
	ToolCalls        int    `json:"tool_calls,omitempty"`
	ReasoningContent bool   `json:"reasoning_content,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

// String renders the attempt as space-separated key=value pairs for notes and debug output.
func (a CompactionSummaryAttempt) String() string {
	parts := []string{fmt.Sprintf("attempt=%d", a.Attempt)}
	if a.FinishReason != "" {
		parts = append(parts, "finish_reason="+a.FinishReason)
	}
	if a.CompletionTokens != nil {
		parts = append(parts, fmt.Sprintf("completion_tokens=%d", *a.CompletionTokens))
	}
	parts = append(parts, fmt.Sprintf("tool_calls=%d", a.ToolCalls), fmt.Sprintf("reasoning_content=%t", a.ReasoningContent))
	if a.Reason != "" {
		parts = append(parts, "reason="+a.Reason)
	}
	return strings.Join(parts, " ")
}

// ContextCompactionStage sizes one compaction stage, as token counts. SourceTokens
// is the summary request's prompt, SummaryTokens the summary it produced, and
// RetainedTokens the kept tail without ProtectedTokens: the newest unit's tool and
// sub-agent results the model has not read yet. Failed marks a stage that could not
// run because its tail does not fit the main budget.
type ContextCompactionStage struct {
	Stage           string `json:"stage,omitempty"`
	RetainedUnits   int    `json:"retained_units,omitempty"`
	SourceTokens    int    `json:"source_tokens,omitempty"`
	SummaryTokens   int    `json:"summary_tokens,omitempty"`
	RetainedTokens  int    `json:"retained_tokens,omitempty"`
	ProtectedTokens int    `json:"protected_tokens,omitempty"`
	Failed          bool   `json:"failed,omitempty"`
}

// BudgetSnapshot returns the token-budget fields of a compaction event as a
// ContextBudgetEvent, so callers can refresh the context counter once
// compaction finishes.
func (e ContextCompactionEvent) BudgetSnapshot() ContextBudgetEvent {
	return ContextBudgetEvent{
		Turn:                      e.Turn,
		PromptTokens:              e.PromptTokens,
		RawPromptTokens:           e.RawPromptTokens,
		ContextTokens:             e.ContextTokens,
		TotalTokens:               e.TotalTokens,
		ContextWindow:             e.ContextWindow,
		ContextUsagePercent:       e.ContextUsagePercent,
		CompactionThreshold:       e.CompactionThreshold,
		CompactionThresholdTokens: e.CompactionThresholdTokens,
		EstimatorPadTokens:        e.EstimatorPadTokens,
		Status:                    e.Status,
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
	Scope                     string   `json:"scope,omitempty"`
	Turn                      int      `json:"turn,omitempty"`
	UsedBytes                 int      `json:"used_bytes,omitempty"`
	BudgetBytes               int      `json:"budget_bytes,omitempty"`
	PromptTokens              int      `json:"prompt_tokens,omitempty"`
	RawPromptTokens           int      `json:"raw_prompt_tokens,omitempty"`
	ContextTokens             int      `json:"context_tokens,omitempty"`
	TotalTokens               int      `json:"total_tokens,omitempty"`
	Truncated                 bool     `json:"truncated,omitempty"`
	ContextWindow             int      `json:"context_window,omitempty"`
	ContextUsagePercent       float64  `json:"context_usage_percent,omitempty"`
	CompactionThreshold       float64  `json:"compaction_threshold,omitempty"`
	CompactionThresholdTokens int      `json:"compaction_threshold_tokens,omitempty"`
	EstimatorPadTokens        int      `json:"estimator_pad_tokens,omitempty"`
	Status                    string   `json:"status,omitempty"`
	Notes                     []string `json:"notes,omitempty"`

	// legacyKind preserves legacy aliases such as session_loaded without adding
	// that compatibility detail to the wire payload.
	legacyKind string
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
	payload.Stages = append([]ContextCompactionStage(nil), payload.Stages...)
	payload.SummaryAttemptDetails = append([]CompactionSummaryAttempt(nil), payload.SummaryAttemptDetails...)
	return payload
}

func (payload ContextCompactionEvent) toLegacyContextDiagnostics() ContextDiagnosticsEvent {
	return ContextDiagnosticsEvent{
		Kind:                      "compaction",
		Scope:                     payload.Scope,
		Turn:                      payload.Turn,
		Severity:                  payload.Severity,
		SessionState:              payload.SessionState,
		CompactionCount:           payload.CompactionCount,
		RestartGuidance:           payload.RestartGuidance,
		SummarizedMessages:        payload.SummarizedMessages,
		RetainedMessages:          payload.RetainedMessages,
		RetainedUnits:             payload.RetainedUnits,
		RetainedUserMessages:      payload.RetainedUserMessages,
		RetainedSubAgentResults:   payload.RetainedSubAgentResults,
		RequestBlock:              payload.RequestBlock,
		RequestBlockBytes:         payload.RequestBlockBytes,
		SummaryTitle:              payload.SummaryTitle,
		SummaryPreview:            payload.SummaryPreview,
		SummaryText:               payload.SummaryText,
		SummaryBytes:              payload.SummaryBytes,
		CacheReadTokens:           payload.CacheReadTokens,
		InputTokens:               payload.InputTokens,
		CacheCreateTokens:         payload.CacheCreateTokens,
		CacheReportedInputTokens:  payload.CacheReportedInputTokens,
		PromptTokens:              payload.PromptTokens,
		RawPromptTokens:           payload.RawPromptTokens,
		ContextTokens:             payload.ContextTokens,
		TotalTokens:               payload.TotalTokens,
		Truncated:                 payload.Truncated,
		ContextWindow:             payload.ContextWindow,
		ContextUsagePercent:       payload.ContextUsagePercent,
		CompactionThreshold:       payload.CompactionThreshold,
		CompactionThresholdTokens: payload.CompactionThresholdTokens,
		EstimatorPadTokens:        payload.EstimatorPadTokens,
		Status:                    payload.Status,
		Mode:                      payload.Mode,
		BeforePromptTokens:        payload.BeforePromptTokens,
		BeforeRawPromptTokens:     payload.BeforeRawPromptTokens,
		BeforeUsagePercent:        payload.BeforeUsagePercent,
		AfterPromptTokens:         payload.AfterPromptTokens,
		AfterRawPromptTokens:      payload.AfterRawPromptTokens,
		AfterUsagePercent:         payload.AfterUsagePercent,
		SummaryTokenBudget:        payload.SummaryTokenBudget,
		ThresholdAchieved:         payload.ThresholdAchieved,
		CompactionModel:           payload.CompactionModel,
		CompactionModelAlias:      payload.CompactionModelAlias,
		FallbackStatus:            payload.FallbackStatus,
		FallbackReason:            payload.FallbackReason,
		CompactionResult:          payload.CompactionResult,
		SummaryAttempts:           payload.SummaryAttempts,
		SummaryFailureReason:      payload.SummaryFailureReason,
		SummaryTruncated:          payload.SummaryTruncated,
		SummaryCondensed:          payload.SummaryCondensed,
		ProviderErrorClass:        payload.ProviderErrorClass,
		HTTPStatus:                payload.HTTPStatus,
		SummaryToolCalls:          payload.SummaryToolCalls,
		SummaryAttemptDetails:     append([]CompactionSummaryAttempt(nil), payload.SummaryAttemptDetails...),
		Stages:                    append([]ContextCompactionStage(nil), payload.Stages...),
		Notes:                     append([]string(nil), payload.Notes...),
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
	kind := "budget"
	if payload.legacyKind != "" {
		kind = payload.legacyKind
	}
	return ContextDiagnosticsEvent{
		Kind:                      kind,
		Scope:                     payload.Scope,
		Turn:                      payload.Turn,
		BudgetBytes:               payload.BudgetBytes,
		UsedBytes:                 payload.UsedBytes,
		PromptTokens:              payload.PromptTokens,
		RawPromptTokens:           payload.RawPromptTokens,
		ContextTokens:             payload.ContextTokens,
		TotalTokens:               payload.TotalTokens,
		Truncated:                 payload.Truncated,
		ContextWindow:             payload.ContextWindow,
		ContextUsagePercent:       payload.ContextUsagePercent,
		CompactionThreshold:       payload.CompactionThreshold,
		CompactionThresholdTokens: payload.CompactionThresholdTokens,
		EstimatorPadTokens:        payload.EstimatorPadTokens,
		Status:                    payload.Status,
		Notes:                     append([]string(nil), payload.Notes...),
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

func contextDiagnosticPayloadForJSON(payload any) any {
	diagnostic, ok := payload.(contextDiagnosticPayload)
	if !ok {
		return payload
	}
	return diagnostic.toLegacyContextDiagnostics()
}

func contextDiagnosticFromLegacy(payload ContextDiagnosticsEvent) contextDiagnosticPayload {
	payload = payload.toLegacyContextDiagnostics()
	switch payload.Kind {
	case "compaction":
		return ContextCompactionEvent{
			Scope:                     payload.Scope,
			Turn:                      payload.Turn,
			Severity:                  payload.Severity,
			SessionState:              payload.SessionState,
			CompactionCount:           payload.CompactionCount,
			RestartGuidance:           payload.RestartGuidance,
			SummarizedMessages:        payload.SummarizedMessages,
			RetainedMessages:          payload.RetainedMessages,
			RetainedUnits:             payload.RetainedUnits,
			RetainedUserMessages:      payload.RetainedUserMessages,
			RetainedSubAgentResults:   payload.RetainedSubAgentResults,
			RequestBlock:              payload.RequestBlock,
			RequestBlockBytes:         payload.RequestBlockBytes,
			SummaryTitle:              payload.SummaryTitle,
			SummaryPreview:            payload.SummaryPreview,
			SummaryText:               payload.SummaryText,
			SummaryBytes:              payload.SummaryBytes,
			CacheReadTokens:           payload.CacheReadTokens,
			InputTokens:               payload.InputTokens,
			CacheCreateTokens:         payload.CacheCreateTokens,
			CacheReportedInputTokens:  payload.CacheReportedInputTokens,
			PromptTokens:              payload.PromptTokens,
			RawPromptTokens:           payload.RawPromptTokens,
			ContextTokens:             payload.ContextTokens,
			TotalTokens:               payload.TotalTokens,
			Truncated:                 payload.Truncated,
			ContextWindow:             payload.ContextWindow,
			ContextUsagePercent:       payload.ContextUsagePercent,
			CompactionThreshold:       payload.CompactionThreshold,
			CompactionThresholdTokens: payload.CompactionThresholdTokens,
			EstimatorPadTokens:        payload.EstimatorPadTokens,
			Status:                    payload.Status,
			Mode:                      payload.Mode,
			BeforePromptTokens:        payload.BeforePromptTokens,
			BeforeRawPromptTokens:     payload.BeforeRawPromptTokens,
			BeforeUsagePercent:        payload.BeforeUsagePercent,
			AfterPromptTokens:         payload.AfterPromptTokens,
			AfterRawPromptTokens:      payload.AfterRawPromptTokens,
			AfterUsagePercent:         payload.AfterUsagePercent,
			SummaryTokenBudget:        payload.SummaryTokenBudget,
			ThresholdAchieved:         payload.ThresholdAchieved,
			CompactionModel:           payload.CompactionModel,
			CompactionModelAlias:      payload.CompactionModelAlias,
			FallbackStatus:            payload.FallbackStatus,
			FallbackReason:            payload.FallbackReason,
			CompactionResult:          payload.CompactionResult,
			SummaryAttempts:           payload.SummaryAttempts,
			SummaryFailureReason:      payload.SummaryFailureReason,
			SummaryTruncated:          payload.SummaryTruncated,
			SummaryCondensed:          payload.SummaryCondensed,
			ProviderErrorClass:        payload.ProviderErrorClass,
			HTTPStatus:                payload.HTTPStatus,
			SummaryToolCalls:          payload.SummaryToolCalls,
			SummaryAttemptDetails:     append([]CompactionSummaryAttempt(nil), payload.SummaryAttemptDetails...),
			Stages:                    append([]ContextCompactionStage(nil), payload.Stages...),
			Notes:                     append([]string(nil), payload.Notes...),
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
		budget := ContextBudgetEvent{
			Scope:                     payload.Scope,
			Turn:                      payload.Turn,
			UsedBytes:                 payload.UsedBytes,
			BudgetBytes:               payload.BudgetBytes,
			PromptTokens:              payload.PromptTokens,
			RawPromptTokens:           payload.RawPromptTokens,
			ContextTokens:             payload.ContextTokens,
			TotalTokens:               payload.TotalTokens,
			Truncated:                 payload.Truncated,
			ContextWindow:             payload.ContextWindow,
			ContextUsagePercent:       payload.ContextUsagePercent,
			CompactionThreshold:       payload.CompactionThreshold,
			CompactionThresholdTokens: payload.CompactionThresholdTokens,
			EstimatorPadTokens:        payload.EstimatorPadTokens,
			Status:                    payload.Status,
			Notes:                     append([]string(nil), payload.Notes...),
		}
		if payload.Kind == "session_loaded" {
			budget.legacyKind = payload.Kind
		}
		return budget
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
