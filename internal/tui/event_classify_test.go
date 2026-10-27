package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
)

func TestDevLogParamsFor(t *testing.T) {
	t.Parallel()
	scoped := output.EventScope{AgentID: "child-1", AgentType: "explore"}
	tests := []struct {
		name     string
		event    output.Event
		scope    output.EventScope
		want     bool
		severity devLogSeverity
		source   string
		message  string
		agentID  string
		callID   string
	}{
		{
			name:     "provider retry is a warning",
			event:    output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Kind: output.DiagnosticKindRetry, Severity: "warning", Message: "retrying turn", Attempt: 1, MaxAttempts: 3}),
			want:     true,
			severity: devLogWarning,
			source:   "provider",
			message:  "retrying turn",
		},
		{
			name:     "tool panic is an agent error with stack",
			event:    output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Kind: output.DiagnosticKindToolPanic, Severity: "error", Message: "tool \"bash\" panicked: boom\ngoroutine 1 [running]:"}),
			want:     true,
			severity: devLogError,
			source:   "agent",
			message:  "goroutine 1",
		},
		{
			name:     "provider error diagnostic",
			event:    output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Severity: "error", Message: "rate limited"}),
			want:     true,
			severity: devLogError,
			source:   "provider",
			message:  "rate limited",
		},
		{
			name:  "suppressible transport override info is silent",
			event: output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Kind: output.ProviderDiagnosticKindTransportOverride, Severity: "info", Suppressible: true, Message: "override"}),
		},
		{
			name:     "stop reason error",
			event:    output.NewStopReasonEvent(1, "error", errors.New("boom")),
			want:     true,
			severity: devLogError,
			source:   "agent",
			message:  "boom",
		},
		{
			name:  "stop reason without error is silent",
			event: output.NewStopReasonEvent(1, "completed", nil),
		},
		{
			name:     "failed tool call",
			event:    output.NewToolCallFinishedEvent(1, "bash", "call-1", "", errors.New("exit 1")),
			want:     true,
			severity: devLogError,
			source:   "tool",
			message:  "exit 1",
			callID:   "call-1",
		},
		{
			name:     "failed mcp tool call",
			event:    output.NewToolCallFinishedEvent(1, "mcp__files__read", "call-2", "", errors.New("denied")),
			want:     true,
			severity: devLogError,
			source:   "mcp",
			message:  "denied",
			callID:   "call-2",
		},
		{
			name:     "failed sub-agent tool call",
			event:    output.NewToolCallFinishedEvent(1, delegation.SubAgentToolName, "call-3", "", errors.New("spawn failed")),
			want:     true,
			severity: devLogError,
			source:   "delegation",
			message:  "spawn failed",
			callID:   "call-3",
		},
		{
			name:  "successful tool call is silent",
			event: output.NewToolCallFinishedEvent(1, "bash", "call-4", "ok", nil),
		},
		{
			name:     "delegation failure",
			event:    output.Event{Payload: output.DelegationFailedEvent{Error: "child crashed"}},
			want:     true,
			severity: devLogError,
			source:   "delegation",
			message:  "child crashed",
		},
		{
			name:     "advisor error",
			event:    output.Event{Payload: output.AdvisorCompleteEvent{Error: "timeout"}},
			want:     true,
			severity: devLogError,
			source:   "delegation",
			message:  "timeout",
		},
		{
			name:     "config warning",
			event:    output.Event{Payload: output.ConfigWarningEvent{Message: "unknown key"}},
			want:     true,
			severity: devLogWarning,
			source:   "config",
			message:  "unknown key",
		},
		{
			name:     "conversation save warning",
			event:    output.NewConversationWarningEvent(output.ConversationWarningKindSave, "save failed"),
			want:     true,
			severity: devLogWarning,
			source:   "session",
			message:  "save failed",
		},
		{
			name:     "conversation run failure",
			event:    output.NewConversationWarningEvent(output.ConversationWarningKindRun, "run failed"),
			want:     true,
			severity: devLogError,
			source:   "session",
			message:  "run failed",
		},
		{
			name:     "session health warning",
			event:    output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{Kind: "session_health", Scope: output.SessionHealthScopeSave, Severity: "warning", Notes: []string{"save degraded"}}),
			want:     true,
			severity: devLogWarning,
			source:   "session",
			message:  "save degraded",
		},
		{
			name:     "session health critical",
			event:    output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{Kind: "session_health", Scope: output.SessionHealthScopeSave, Severity: "critical", Notes: []string{"save failed"}}),
			want:     true,
			severity: devLogError,
			source:   "session",
			message:  "save failed",
		},
		{
			name:     "typed session health critical keeps notes",
			event:    output.Event{Payload: output.ContextSessionHealthEvent{Severity: "critical", Notes: []string{"compacted often"}, RestartGuidance: "restart"}},
			want:     true,
			severity: devLogError,
			source:   "session",
			message:  "compacted often",
		},
		{
			name:     "typed session health warning falls back to restart guidance",
			event:    output.Event{Payload: output.ContextSessionHealthEvent{Severity: "warning", RestartGuidance: "restart the session"}},
			want:     true,
			severity: devLogWarning,
			source:   "session",
			message:  "restart the session",
		},
		{
			name:  "session health info is silent",
			event: output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{Kind: "session_health", Severity: "info", Notes: []string{"ok"}}),
		},
		{
			name:     "scoped retry keeps agent id",
			event:    output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Kind: output.DiagnosticKindRetry, Severity: "warning", Message: "retrying"}),
			scope:    scoped,
			want:     true,
			severity: devLogWarning,
			source:   "provider",
			message:  "retrying",
			agentID:  "child-1",
		},
		{
			name:     "bash artifact truncation is a tool warning",
			event:    output.NewBashArtifactTruncatedEvent(2, "call-5", ".steiner/artifacts/call-5.txt", "warning: bash artifact truncated: partial output saved to .steiner/artifacts/call-5.txt"),
			want:     true,
			severity: devLogWarning,
			source:   "tool",
			message:  "partial output saved to",
			callID:   "call-5",
		},
		{
			name:     "scoped bash artifact truncation keeps agent id",
			event:    output.NewBashArtifactTruncatedEvent(2, "call-6", "", "warning: bash artifact truncated; saved output is partial"),
			scope:    scoped,
			want:     true,
			severity: devLogWarning,
			source:   "tool",
			message:  "saved output is partial",
			agentID:  "child-1",
			callID:   "call-6",
		},
		{
			name:  "assistant chunk is not error-bearing",
			event: output.NewAssistantChunkEventWithSource(1, "hello", ""),
		},
		{
			name:  "unlisted event type is silent",
			event: output.Event{Type: "unlisted_type"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			event := tc.event
			event.Scope = tc.scope
			got, ok := devLogParamsFor(event)
			if ok != tc.want {
				t.Fatalf("ok = %v, want %v", ok, tc.want)
			}
			if !ok {
				return
			}
			if got.Severity != tc.severity {
				t.Errorf("severity = %v, want %v", got.Severity, tc.severity)
			}
			if got.Source != tc.source {
				t.Errorf("source = %q, want %q", got.Source, tc.source)
			}
			if !strings.Contains(got.Message, tc.message) {
				t.Errorf("message = %q, want it to contain %q", got.Message, tc.message)
			}
			if got.AgentID != tc.agentID {
				t.Errorf("agent id = %q, want %q", got.AgentID, tc.agentID)
			}
			if got.CallID != tc.callID {
				t.Errorf("call id = %q, want %q", got.CallID, tc.callID)
			}
		})
	}
}

func TestBashArtifactTruncatedDevLogRecordsAgentType(t *testing.T) {
	t.Parallel()
	event := output.NewBashArtifactTruncatedEvent(2, "call-6", "", "warning: bash artifact truncated; saved output is partial")
	event.Scope = output.EventScope{AgentID: "child-1", AgentType: "explore"}
	got, ok := devLogParamsFor(event)
	if !ok {
		t.Fatal("devLogParamsFor() ok = false, want true")
	}
	if got.AgentID != "child-1" || got.AgentType != "explore" {
		t.Errorf("agent = %q/%q, want child-1/explore", got.AgentID, got.AgentType)
	}
}
