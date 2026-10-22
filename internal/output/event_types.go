package output

import (
	"encoding/json"
	"sync"
	"time"
)

const (
	// EventTypeModelCallStarted marks the start of a provider call.
	EventTypeModelCallStarted = "model_call_started"
	// EventTypeModelCallFinished marks the end of a provider call.
	EventTypeModelCallFinished = "model_call_finished"
	// EventTypeToolCallStarted marks the start of a tool call.
	EventTypeToolCallStarted = "tool_call_started"
	// EventTypeToolCallFinished marks the end of a tool call.
	EventTypeToolCallFinished = "tool_call_finished"
	// EventTypeToolCallQueued marks a delegation tool call announced before it
	// acquires a parallelism slot. It carries the same payload shape as
	// EventTypeToolCallStarted and is emitted only for delegation-class calls.
	EventTypeToolCallQueued = "tool_call_queued"
	// EventTypeApprovalRequested marks a pending approval request.
	EventTypeApprovalRequested = "approval_requested"
	// EventTypeApprovalAccepted marks an accepted approval request.
	EventTypeApprovalAccepted = "approval_accepted"
	// EventTypeApprovalDenied marks a denied approval request.
	EventTypeApprovalDenied = "approval_denied"
	// EventTypeWorkflowHandoffRequested marks a pending workflow handoff request.
	EventTypeWorkflowHandoffRequested = "workflow_handoff_requested"
	// EventTypeWorkflowHandoffAccepted marks an accepted workflow handoff request.
	EventTypeWorkflowHandoffAccepted = "workflow_handoff_accepted"
	// EventTypeWorkflowHandoffDeclined marks a declined workflow handoff request.
	EventTypeWorkflowHandoffDeclined = "workflow_handoff_declined"
	// EventTypeStopReason records why a run stopped.
	EventTypeStopReason = "stop_reason"
	// EventTypeUserInput records user input entering the stream.
	EventTypeUserInput = "user_input"
	// EventTypeAPIRequest records an outbound provider request.
	EventTypeAPIRequest = "api_request"
	// EventTypeAPIResponse records an inbound provider response.
	EventTypeAPIResponse = "api_response"
	// EventTypeRunStarted marks the start of a top-level run.
	EventTypeRunStarted = "run_started"
	// EventTypeRunFinished marks the end of a top-level run.
	EventTypeRunFinished = "run_finished"
	// EventTypeOneshotFinished marks the end of a oneshot run.
	EventTypeOneshotFinished = "oneshot_finished"
	// EventTypeAssistantMessage records a completed assistant message.
	EventTypeAssistantMessage = "assistant_message"
	// EventTypeAssistantChunk records a streamed assistant chunk.
	EventTypeAssistantChunk = "assistant_chunk"
	// EventTypeThinkingChunk records a streamed reasoning chunk.
	EventTypeThinkingChunk = "thinking_chunk"
	// EventTypeProviderDiagnostic records provider retry or transport diagnostics.
	EventTypeProviderDiagnostic = "provider_diagnostic"
	// ProviderDiagnosticKindTransportOverride identifies a provider diagnostic
	// that is informational and safe to suppress from user-facing transcripts.
	ProviderDiagnosticKindTransportOverride = "transport_override"
	// EventTypeContextReport records the assembled context report.
	EventTypeContextReport = "context_report"
	// EventTypeHistoryLoaded records loaded conversation history.
	EventTypeHistoryLoaded = "history_loaded"
	// EventTypeContextDiagnostics records context assembly diagnostics.
	EventTypeContextDiagnostics = "context_diagnostics"
	// EventTypeDelegationAccepted marks a delegation call accepted for execution.
	EventTypeDelegationAccepted = "delegation_accepted"
	// EventTypeDelegationStarted marks the start of sub-agent delegation.
	EventTypeDelegationStarted = "delegation_started"
	// EventTypeDelegationQueued marks a sub-agent accepted but waiting for a running slot.
	EventTypeDelegationQueued = "delegation_queued"
	// EventTypeSubAgentsDelivered marks sub-agent results delivered to the model in one message.
	EventTypeSubAgentsDelivered = "sub_agents_delivered"
	// EventTypeSubAgentResultsUnanswered marks delivered sub-agent results the model never answered.
	EventTypeSubAgentResultsUnanswered = "sub_agent_results_unanswered"
	// EventTypeConversationState marks a conversation driver state or held change.
	EventTypeConversationState = "conversation_state"
	// EventTypeConversationWarning marks a non-fatal conversation driver failure.
	EventTypeConversationWarning = "conversation_warning"
	// EventTypeDelegationComplete marks successful sub-agent completion.
	EventTypeDelegationComplete = "delegation_complete"
	// EventTypeDelegationFailed marks failed sub-agent completion.
	EventTypeDelegationFailed = "delegation_failed"
	// EventTypeDelegationWorktreeDisposal records disposal of a cancelled code agent worktree.
	EventTypeDelegationWorktreeDisposal = "delegation_worktree_disposal"
	// EventTypeDelegationCacheWaiting marks a sub-agent delegation gated behind a shared prompt-cache dispatch slot.
	EventTypeDelegationCacheWaiting = "delegation_cache_waiting"
	// EventTypeAdvisorStarted marks the start of an advisor call.
	EventTypeAdvisorStarted = "advisor_started"
	// EventTypeAdvisorComplete marks the end of an advisor call.
	EventTypeAdvisorComplete = "advisor_complete"
	// EventTypeAdvisorBudgetExhausted marks a skipped advisor call because the
	// per-run budget was already exhausted.
	EventTypeAdvisorBudgetExhausted = "advisor_budget_exhausted"
	// EventTypePhaseTransition marks a oneshot phase transition.
	EventTypePhaseTransition = "phase_transition"
	// EventTypePhaseIndicator marks a oneshot phase status indicator.
	EventTypePhaseIndicator = "phase_indicator"

	// EventTypeDisplayFile is emitted when the agent wants the TUI to display a
	// file to the user. The event payload carries an explicit preview document so
	// the UI can render the slice without the model-visible tool result or
	// conversation history containing file contents.
	EventTypeDisplayFile = "display_file"

	// EventTypeSteerReceived is emitted when the agent loop consumes a queued
	// steering message and injects it into the conversation.
	EventTypeSteerReceived = "steer_received"
	// EventTypeModeChanged is emitted when the execution mode changes.
	EventTypeModeChanged = "mode_changed"
	// EventTypeOrchestrationLevelChanged is emitted when the orchestration
	// level changes.
	EventTypeOrchestrationLevelChanged = "orchestration_level_changed"
	// EventTypeSandboxStatus is emitted when the sandbox status is determined at startup.
	EventTypeSandboxStatus = "sandbox_status"
	// EventTypeConfigWarning is emitted when configuration carries a deprecated
	// key worth surfacing. It renders as a status line and never carries
	// sandbox or other sidebar state.
	EventTypeConfigWarning = "config_warning"
	// EventTypeSkillTruncated is emitted when an enabled skill's SKILL.md body
	// exceeds the skill content cap and is truncated. It renders as a status
	// line.
	EventTypeSkillTruncated = "skill_truncated"
	// EventTypeSkillState is emitted by session replay once per persisted skill
	// block. It renders as a status line.
	EventTypeSkillState = "skill_state"
	// EventTypeMCPStatus is emitted when the MCP server set changes state. The
	// payload is an immutable snapshot of the MCP surface, so display consumers
	// never read the registry or manager concurrently.
	EventTypeMCPStatus = "mcp_status"
	// EventTypeConversationReset is emitted when a saved session replaces the
	// live conversation (resume or fork), after the swap succeeded and before
	// the replayed transcript. It is display-only: consumers drop transcript
	// state tied to the previous conversation.
	EventTypeConversationReset = "conversation_reset"
	// EventTypeReplayStarted marks the start of a saved session replay, after
	// EventTypeConversationReset and before the replayed transcript. It is
	// display-only and renders no text.
	EventTypeReplayStarted = "replay_started"
	// EventTypeReplayFinished marks the end of a saved session replay. It is
	// emitted even when replay exits early or panics. It is display-only and
	// renders no text.
	EventTypeReplayFinished = "replay_finished"
)

// Event is the timestamped envelope emitted by the runtime event stream.
type Event struct {
	Type      string     `json:"type"`
	Timestamp time.Time  `json:"timestamp"`
	Payload   any        `json:"payload,omitempty"`
	Scope     EventScope `json:"scope,omitempty"`
}

// EventScope identifies the transcript scope for a runtime event.
type EventScope struct {
	AgentID   string `json:"agent_id,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
}

// MarshalJSON omits empty scope metadata so top-level events keep their
// existing JSON shape.
func (e Event) MarshalJSON() ([]byte, error) {
	payload := e.Payload
	if e.Type == EventTypeContextDiagnostics {
		payload = contextDiagnosticPayloadForJSON(payload)
	}
	if e.Scope.AgentID == "" && e.Scope.AgentType == "" {
		return json.Marshal(struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Payload   any       `json:"payload,omitempty"`
		}{
			Type:      e.Type,
			Timestamp: e.Timestamp,
			Payload:   payload,
		})
	}

	return json.Marshal(struct {
		Type      string     `json:"type"`
		Timestamp time.Time  `json:"timestamp"`
		Payload   any        `json:"payload,omitempty"`
		Scope     EventScope `json:"scope,omitempty"`
	}{
		Type:      e.Type,
		Timestamp: e.Timestamp,
		Payload:   payload,
		Scope:     e.Scope,
	})
}

// EventSink receives structured runtime events.
type EventSink interface {
	Emit(Event)
}

// SinkFunc adapts a function into an EventSink.
type SinkFunc func(Event)

// Emit forwards the event to the wrapped function.
func (f SinkFunc) Emit(event Event) {
	if f != nil {
		f(event)
	}
}

// NoopSink discards all events.
type NoopSink struct{}

// Emit discards the event.
func (NoopSink) Emit(Event) {}

// ForwardSink is a thread-safe EventSink whose target can be swapped at runtime.
// It starts with a NoopSink and forwards events to whatever target is set via Set.
// This is used to wire an event sink into tool environments before the full sink
// chain (including the TUI sink) is assembled.
type ForwardSink struct {
	mu     sync.RWMutex
	target EventSink
}

// NewForwardSink creates a ForwardSink that starts with a NoopSink target.
func NewForwardSink() *ForwardSink {
	return &ForwardSink{target: NoopSink{}}
}

// Set replaces the forwarding target. It is safe to call concurrently.
func (f *ForwardSink) Set(sink EventSink) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sink == nil {
		f.target = NoopSink{}
	} else {
		f.target = sink
	}
}

// Emit forwards the event to the current target. It is safe to call concurrently.
func (f *ForwardSink) Emit(event Event) {
	f.mu.RLock()
	t := f.target
	f.mu.RUnlock()
	t.Emit(event)
}
