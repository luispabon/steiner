package tui

import (
	"strings"

	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
)

// devLogClass is the dev log classification of one error-bearing event.
type devLogClass struct {
	severity devLogSeverity
	source   string
	message  string
	callID   string
}

// devLogParamsFor returns the dev log params for an error-bearing event. It
// reports false for every other event type, and for the error-bearing types
// whose event carries no error. It reads only event, so it is safe to call on
// every event.
func devLogParamsFor(event output.Event) (devLogParams, bool) {
	class, ok := classifyDevLog(event)
	if !ok {
		return devLogParams{}, false
	}
	return devLogParams{
		Severity:  class.severity,
		Source:    class.source,
		AgentID:   event.Scope.AgentID,
		AgentType: event.Scope.AgentType,
		CallID:    class.callID,
		EventType: event.Type,
		Message:   class.message,
	}, true
}

// classifyDevLog maps an event from the D12 error set to its dev log class.
func classifyDevLog(event output.Event) (devLogClass, bool) {
	switch payload := event.Payload.(type) {
	case output.ProviderDiagnosticEvent:
		return classifyProviderDiagnostic(payload)
	case output.StopReasonEvent:
		return errorClassFor(payload.Error, "agent")
	case output.ToolCallFinishedEvent:
		class, ok := errorClassFor(payload.Error, toolDevLogSource(payload.Tool))
		class.callID = payload.CallID
		return class, ok
	case output.DelegationFailedEvent:
		return devLogClass{severity: devLogError, source: "delegation", message: payload.Error}, true
	case output.AdvisorCompleteEvent:
		return errorClassFor(payload.Error, "delegation")
	case output.ConfigWarningEvent:
		return devLogClass{severity: devLogWarning, source: "config", message: payload.Message}, true
	case output.SkillTruncatedEvent:
		return devLogClass{severity: devLogWarning, source: "config", message: payload.Message()}, true
	case output.ConversationWarningEvent:
		return classifyConversationWarning(payload), true
	}
	if health, ok := output.AsContextSessionHealthEvent(event.Payload); ok {
		return classifySessionHealth(health)
	}
	return devLogClass{}, false
}

// classifyProviderDiagnostic classifies retry and tool panic diagnostics, and
// other diagnostics that carry a warning or error severity. Info diagnostics
// are not error-bearing.
func classifyProviderDiagnostic(payload output.ProviderDiagnosticEvent) (devLogClass, bool) {
	severity := devLogWarning
	if payload.Severity == "error" || payload.Kind == output.DiagnosticKindToolPanic {
		severity = devLogError
	}
	switch {
	case payload.Kind == output.DiagnosticKindToolPanic:
		return devLogClass{severity: severity, source: "agent", message: payload.Message}, true
	case payload.Kind == output.DiagnosticKindRetry,
		payload.Severity == "warning", payload.Severity == "error":
		return devLogClass{severity: severity, source: "provider", message: payload.Message}, true
	}
	return devLogClass{}, false
}

// classifyConversationWarning gives a failed run error severity. A failed save
// or compaction is a warning.
func classifyConversationWarning(payload output.ConversationWarningEvent) devLogClass {
	severity := devLogWarning
	if payload.Kind == output.ConversationWarningKindRun {
		severity = devLogError
	}
	return devLogClass{severity: severity, source: "session", message: payload.Message}
}

// classifySessionHealth classifies a session health diagnostic. Severities
// other than warning and critical are not error-bearing.
func classifySessionHealth(health output.ContextSessionHealthEvent) (devLogClass, bool) {
	severity, ok := sessionHealthSeverity(health)
	if !ok {
		return devLogClass{}, false
	}
	message := strings.Join(health.Notes, "\n")
	if message == "" {
		message = health.RestartGuidance
	}
	return devLogClass{severity: severity, source: "session", message: message}, true
}

// sessionHealthSeverity maps a session health severity to a dev log severity.
// It reports false for severities that are not warnings or errors.
func sessionHealthSeverity(health output.ContextSessionHealthEvent) (devLogSeverity, bool) {
	switch health.Severity {
	case "warning":
		return devLogWarning, true
	case "critical":
		return devLogError, true
	}
	return 0, false
}

// errorClassFor classifies an event whose error text is errText. An empty
// error means the event is not error-bearing.
func errorClassFor(errText, source string) (devLogClass, bool) {
	if errText == "" {
		return devLogClass{}, false
	}
	return devLogClass{severity: devLogError, source: source, message: errText}, true
}

// toolDevLogSource names the dev log source for a failed tool call.
func toolDevLogSource(tool string) string {
	name := normalizeToolName(tool)
	switch {
	case name == delegation.SubAgentToolName || name == delegation.FollowUpToolName:
		return "delegation"
	case strings.HasPrefix(name, "mcp__"):
		return "mcp"
	case strings.HasPrefix(name, "lsp_"):
		return "lsp"
	}
	return "tool"
}
