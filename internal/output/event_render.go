package output

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/luispabon/steiner/internal/usagestats"
)

func renderEvent(event Event) Segment {
	if renderer, ok := eventRenderers[reflect.TypeOf(event.Payload)]; ok {
		return renderer(event)
	}
	return renderUnknownEvent(event)
}

func typedRenderer[T any](render func(T) Segment) func(Event) Segment {
	return func(event Event) Segment {
		return render(event.Payload.(T))
	}
}

var eventRenderers = map[reflect.Type]func(Event) Segment{
	reflect.TypeOf(RunStartedEvent{}):         typedRenderer(renderRunStartedEvent),
	reflect.TypeOf(AssistantMessageEvent{}):   typedRenderer(renderAssistantMessageEvent),
	reflect.TypeOf(AssistantChunkEvent{}):     typedRenderer(renderAssistantChunkEvent),
	reflect.TypeOf(ThinkingChunkEvent{}):      typedRenderer(renderThinkingChunkEvent),
	reflect.TypeOf(ProviderDiagnosticEvent{}): typedRenderer(renderProviderDiagnosticEvent),
	reflect.TypeOf(ContextReportEvent{}): func(event Event) Segment {
		payload := event.Payload.(ContextReportEvent)
		return Segment{Channel: ChannelAssistant, Label: "context", Text: payload.Content}
	},
	reflect.TypeOf(DisplayFilePayload{}):          typedRenderer(renderDisplayFileEvent),
	reflect.TypeOf(ModelCallStartedEvent{}):       typedRenderer(renderModelCallStartedEvent),
	reflect.TypeOf(ModelCallFinishedEvent{}):      typedRenderer(renderModelCallFinishedEvent),
	reflect.TypeOf(ToolCallStartedEvent{}):        typedRenderer(renderToolCallStartedEvent),
	reflect.TypeOf(ToolCallFinishedEvent{}):       typedRenderer(renderToolCallFinishedEvent),
	reflect.TypeOf(DelegationCompleteEvent{}):     typedRenderer(renderDelegationCompleteEvent),
	reflect.TypeOf(AdvisorStartedEvent{}):         typedRenderer(renderAdvisorStartedEvent),
	reflect.TypeOf(AdvisorCompleteEvent{}):        typedRenderer(renderAdvisorCompleteEvent),
	reflect.TypeOf(AdvisorBudgetExhaustedEvent{}): typedRenderer(renderAdvisorBudgetExhaustedEvent),
	reflect.TypeOf(PhaseTransitionEvent{}):        typedRenderer(renderPhaseTransitionEvent),
	reflect.TypeOf(PhaseIndicatorEvent{}):         typedRenderer(renderPhaseIndicatorEvent),
	reflect.TypeOf(ApprovalEvent{}): func(event Event) Segment {
		return renderApprovalEvent(event, event.Payload.(ApprovalEvent))
	},
	reflect.TypeOf(WorkflowHandoffEvent{}):       typedRenderer(renderWorkflowHandoffEvent),
	reflect.TypeOf(StopReasonEvent{}):            typedRenderer(renderStopReasonEvent),
	reflect.TypeOf(UserInputEvent{}):             typedRenderer(renderUserInputEvent),
	reflect.TypeOf(APIRequestEvent{}):            typedRenderer(renderAPIRequestEvent),
	reflect.TypeOf(APIResponseEvent{}):           typedRenderer(renderAPIResponseEvent),
	reflect.TypeOf(ContextDiagnosticsEvent{}):    typedRenderer(renderLegacyContextDiagnosticsEvent),
	reflect.TypeOf(ContextCompactionEvent{}):     typedRenderer(renderContextCompactionEvent),
	reflect.TypeOf(ContextSessionHealthEvent{}):  typedRenderer(renderContextSessionHealthEvent),
	reflect.TypeOf(ContextBudgetEvent{}):         typedRenderer(renderContextBudgetEvent),
	reflect.TypeOf(ContextFileAnnotationEvent{}): typedRenderer(renderContextFileAnnotationEvent),
	reflect.TypeOf(ConfigWarningEvent{}):         typedRenderer(renderConfigWarningEvent),
}

func appendField(parts []string, key, value string) []string {
	if value == "" {
		return parts
	}
	return append(parts, fmt.Sprintf("%s=%s", key, value))
}

func appendIntField(parts []string, key string, value int) []string {
	if value <= 0 {
		return parts
	}
	return append(parts, fmt.Sprintf("%s=%d", key, value))
}

func errorChannel(hasErr bool) (Channel, string) {
	if hasErr {
		return ChannelError, "error"
	}
	return ChannelStatus, "status"
}

func advisorHeader(label, model string, useNumber, maxUses int) []string {
	parts := []string{label}
	parts = appendField(parts, "model", model)
	if maxUses > 0 {
		parts = append(parts, fmt.Sprintf("use=%d/%d", useNumber, maxUses))
	}
	return parts
}

func renderRunStartedEvent(payload RunStartedEvent) Segment {
	parts := []string{"run started"}
	parts = appendField(parts, "mode", payload.Mode)
	parts = appendField(parts, "model", payload.Model)
	parts = appendField(parts, "prompt", payload.Prompt)
	parts = appendIntField(parts, "turn_limit", payload.MaxTurns)
	parts = appendIntField(parts, "token_limit", payload.MaxTokens)
	return Segment{Channel: ChannelStatus, Label: "status", Text: strings.Join(parts, " ")}
}

func renderAssistantMessageEvent(payload AssistantMessageEvent) Segment {
	parts := []string{}
	parts = appendIntField(parts, "turn", payload.Turn)
	parts = appendField(parts, "role", payload.Role)
	parts = appendField(parts, "content", payload.Content)
	return Segment{Channel: ChannelAssistant, Label: "assistant", Text: strings.Join(parts, " ")}
}

func renderAssistantChunkEvent(payload AssistantChunkEvent) Segment {
	parts := []string{}
	parts = appendIntField(parts, "turn", payload.Turn)
	parts = appendField(parts, "source", string(payload.Source))
	parts = appendField(parts, "chunk", payload.Content)
	return Segment{Channel: ChannelAssistant, Label: "assistant", Text: strings.Join(parts, " ")}
}

func renderThinkingChunkEvent(payload ThinkingChunkEvent) Segment {
	parts := []string{}
	parts = appendIntField(parts, "turn", payload.Turn)
	parts = appendField(parts, "source", string(payload.Source))
	parts = appendField(parts, "thinking", payload.Content)
	return Segment{Channel: ChannelStatus, Label: "thinking", Text: strings.Join(parts, " ")}
}

func renderProviderDiagnosticEvent(payload ProviderDiagnosticEvent) Segment {
	if payload.suppressInTranscript() {
		return Segment{}
	}
	parts := []string{"provider"}
	parts = appendIntField(parts, "turn", payload.Turn)
	if payload.Severity != "" {
		parts = append(parts, payload.Severity)
	}
	parts = appendIntField(parts, "attempt", payload.Attempt)
	parts = appendIntField(parts, "max", payload.MaxAttempts)
	parts = appendField(parts, "delay", payload.Delay)
	if payload.Partial {
		parts = append(parts, "partial=true")
	}
	parts = appendField(parts, "message", payload.Message)
	return Segment{Channel: ChannelStatus, Label: "status", Text: strings.Join(parts, " ")}
}

func renderLegacyContextDiagnosticsEvent(payload ContextDiagnosticsEvent) Segment {
	return Segment{Channel: ChannelStatus, Label: "context", Text: formatContextDiagnosticsEvent(payload)}
}

func renderContextCompactionEvent(payload ContextCompactionEvent) Segment {
	return Segment{Channel: ChannelStatus, Label: "context", Text: formatAnyContextDiagnosticsEvent(payload)}
}

func renderContextSessionHealthEvent(payload ContextSessionHealthEvent) Segment {
	return Segment{Channel: ChannelStatus, Label: "context", Text: formatAnyContextDiagnosticsEvent(payload)}
}

func renderContextBudgetEvent(payload ContextBudgetEvent) Segment {
	return Segment{Channel: ChannelStatus, Label: "context", Text: formatAnyContextDiagnosticsEvent(payload)}
}

func renderContextFileAnnotationEvent(payload ContextFileAnnotationEvent) Segment {
	return Segment{Channel: ChannelStatus, Label: "context", Text: formatAnyContextDiagnosticsEvent(payload)}
}

func renderConfigWarningEvent(payload ConfigWarningEvent) Segment {
	return Segment{Channel: ChannelStatus, Label: "status", Text: payload.Message}
}

func renderDisplayFileEvent(payload DisplayFilePayload) Segment {
	parts := []string{"display"}
	parts = appendField(parts, "path", payload.Path)
	parts = appendField(parts, "syntax", payload.Preview.Language)
	parts = appendIntField(parts, "offset", payload.Offset)
	parts = appendIntField(parts, "limit", payload.Limit)
	return Segment{Channel: ChannelStatus, Label: "status", Text: strings.Join(parts, " ")}
}

func renderModelCallStartedEvent(payload ModelCallStartedEvent) Segment {
	return Segment{
		Channel: ChannelStatus,
		Label:   "status",
		Text:    fmt.Sprintf("model turn=%d started messages=%d model=%s", payload.Turn, payload.MessageCount, fallback(payload.Model, "unknown")),
	}
}

func renderModelCallFinishedEvent(payload ModelCallFinishedEvent) Segment {
	parts := []string{
		fmt.Sprintf("model turn=%d finished", payload.Turn),
	}
	parts = appendField(parts, "model", payload.Model)
	parts = appendField(parts, "finish", payload.FinishReason)
	parts = appendIntField(parts, "tool_calls", payload.ToolCalls)
	parts = appendIntField(parts, "tokens", payload.CompletionTokens)
	channel, label := errorChannel(payload.Error != "")
	if payload.Error != "" {
		parts = append(parts, fmt.Sprintf("error=%s", payload.Error))
	}
	return Segment{Channel: channel, Label: label, Text: strings.Join(parts, " ")}
}

func renderToolCallStartedEvent(payload ToolCallStartedEvent) Segment {
	parts := []string{
		fmt.Sprintf("turn=%d start", payload.Turn),
	}
	parts = appendField(parts, "tool", payload.Tool)
	parts = appendField(parts, "id", payload.CallID)
	if len(payload.Arguments) > 0 {
		parts = append(parts, fmt.Sprintf("args=%s", CompactJSON(payload.Arguments)))
	}
	return Segment{Channel: ChannelTool, Label: "tool", Text: strings.Join(parts, " ")}
}

func renderToolCallFinishedEvent(payload ToolCallFinishedEvent) Segment {
	parts := []string{
		fmt.Sprintf("turn=%d end", payload.Turn),
	}
	parts = appendField(parts, "tool", payload.Tool)
	parts = appendField(parts, "id", payload.CallID)
	parts = appendField(parts, "result", payload.Result)
	channel := ChannelTool
	label := "tool"
	if payload.Error != "" {
		channel, label = errorChannel(true)
		parts = append(parts, fmt.Sprintf("error=%s", payload.Error))
	}
	return Segment{Channel: channel, Label: label, Text: strings.Join(parts, " ")}
}

func renderDelegationCompleteEvent(payload DelegationCompleteEvent) Segment {
	parts := []string{payload.AgentID}
	parts = appendField(parts, "status", payload.Status)
	parts = appendIntField(parts, "turns", payload.TurnCount)
	parts = appendIntField(parts, "tokens", payload.TokenCount)
	parts = appendIntField(parts, "tool_calls", payload.ToolCallCount)
	if rate, ok := usagestats.HitRate(payload.CacheReadTokens, payload.InputTokens, payload.CacheCreateTokens); ok {
		parts = appendField(parts, "cache", fmt.Sprintf("%.1f%%", rate*100))
	}
	return Segment{Channel: ChannelStatus, Label: "delegation complete", Text: strings.Join(parts, " ")}
}

func renderAdvisorStartedEvent(payload AdvisorStartedEvent) Segment {
	parts := advisorHeader("advisor started", payload.Model, payload.UseNumber, payload.MaxUses)
	return Segment{Channel: ChannelStatus, Label: "advisor", Text: strings.Join(parts, " ")}
}

func renderAdvisorCompleteEvent(payload AdvisorCompleteEvent) Segment {
	parts := advisorHeader("advisor complete", payload.Model, payload.UseNumber, payload.MaxUses)
	parts = appendField(parts, "note", TruncateWithEllipsis(payload.Note, 240))
	if payload.Truncated {
		parts = append(parts, "truncated=true")
	}
	if rate, ok := usagestats.HitRate(payload.CacheReadTokens, payload.InputTokens, payload.CacheCreateTokens); ok {
		parts = appendField(parts, "cache", fmt.Sprintf("%.1f%%", rate*100))
	}
	channel := ChannelStatus
	label := "advisor"
	if payload.Error != "" {
		channel, label = errorChannel(true)
		parts = append(parts, fmt.Sprintf("error=%s", payload.Error))
	}
	return Segment{Channel: channel, Label: label, Text: strings.Join(parts, " ")}
}

func renderAdvisorBudgetExhaustedEvent(payload AdvisorBudgetExhaustedEvent) Segment {
	parts := []string{"advisor budget exhausted"}
	parts = appendField(parts, "model", payload.Model)
	if payload.MaxUses > 0 {
		parts = append(parts, fmt.Sprintf("use=%d/%d", payload.Used, payload.MaxUses))
	}
	parts = appendField(parts, "message", payload.Message)
	return Segment{Channel: ChannelStatus, Label: "advisor", Text: strings.Join(parts, " ")}
}

func renderPhaseTransitionEvent(payload PhaseTransitionEvent) Segment {
	parts := []string{"phase transition"}
	if payload.From != "" || payload.To != "" {
		parts = append(parts, fmt.Sprintf("%s -> %s", fallback(payload.From, "?"), fallback(payload.To, "?")))
	}
	if payload.Status != "" {
		parts = append(parts, payload.Status)
	}
	if payload.Model != "" {
		parts = append(parts, fmt.Sprintf("model=%s", payload.Model))
	}
	if payload.SessionID != "" {
		parts = append(parts, fmt.Sprintf("session=%s", payload.SessionID))
	}
	if payload.RunID != "" {
		parts = append(parts, fmt.Sprintf("run=%s", payload.RunID))
	}
	return Segment{Channel: ChannelStatus, Label: "phase", Text: strings.Join(parts, " ")}
}

func renderPhaseIndicatorEvent(payload PhaseIndicatorEvent) Segment {
	parts := []string{"phase"}
	if payload.Phase != "" {
		parts = append(parts, payload.Phase)
	}
	if payload.State != "" {
		parts = append(parts, payload.State)
	}
	if payload.Message != "" {
		parts = append(parts, payload.Message)
	}
	if payload.RunID != "" {
		parts = append(parts, fmt.Sprintf("run=%s", payload.RunID))
	}
	return Segment{Channel: ChannelStatus, Label: "phase", Text: strings.Join(parts, " ")}
}

func renderApprovalEvent(event Event, payload ApprovalEvent) Segment {
	status := "requested"
	switch event.Type {
	case EventTypeApprovalAccepted:
		status = "accepted"
	case EventTypeApprovalDenied:
		status = "denied"
	}
	parts := []string{
		fmt.Sprintf("turn=%d %s", payload.Turn, status),
	}
	parts = appendField(parts, "tool", payload.Tool)
	parts = appendField(parts, "mode", payload.Mode)
	parts = appendField(parts, "args", payload.Preview)
	parts = appendField(parts, "message", payload.Message)
	return Segment{Channel: ChannelApproval, Label: "approval", Text: strings.Join(parts, " ")}
}

func renderWorkflowHandoffEvent(payload WorkflowHandoffEvent) Segment {
	parts := []string{"workflow handoff"}
	if payload.Decision != "" {
		parts = append(parts, payload.Decision)
	}
	parts = appendField(parts, "next", payload.Next)
	parts = appendField(parts, "target", payload.Target)
	parts = appendField(parts, "message", payload.Message)
	return Segment{Channel: ChannelStatus, Label: "handoff", Text: strings.Join(parts, " ")}
}

func renderStopReasonEvent(payload StopReasonEvent) Segment {
	parts := []string{}
	if payload.Summary != "" {
		parts = append(parts, payload.Summary)
	}
	if payload.Summary == "" && payload.Reason != "" {
		parts = append(parts, "reason="+payload.Reason)
	}
	if payload.Action != "" {
		parts = append(parts, "next: "+payload.Action)
	}
	channel := ChannelStatus
	label := "status"
	if payload.Error != "" {
		channel = ChannelError
		label = "error"
		parts = append(parts, "error: "+payload.Error)
	}
	return Segment{Channel: channel, Label: label, Text: strings.Join(parts, " ")}
}

func renderUserInputEvent(payload UserInputEvent) Segment {
	parts := []string{}
	parts = appendField(parts, "mode", payload.Mode)
	parts = appendField(parts, "content", payload.Content)
	return Segment{Channel: ChannelStatus, Label: "input", Text: strings.Join(parts, " ")}
}

func renderAPIRequestEvent(payload APIRequestEvent) Segment {
	parts := []string{}
	parts = appendField(parts, "model", payload.Model)
	return Segment{Channel: ChannelStatus, Label: "api", Text: joinOrFallback(parts, "request")}
}

func renderAPIResponseEvent(payload APIResponseEvent) Segment {
	parts := []string{}
	parts = appendField(parts, "finish", payload.FinishReason)
	channel := ChannelStatus
	label := "api"
	if payload.Error != "" {
		channel, label = errorChannel(true)
		parts = append(parts, fmt.Sprintf("error=%s", payload.Error))
	}
	text := joinOrFallback(parts, "response")
	return Segment{Channel: channel, Label: label, Text: text}
}

func renderUnknownEvent(event Event) Segment {
	if event.Type == "" {
		return Segment{}
	}
	if event.Payload == nil {
		return Segment{Channel: ChannelStatus, Label: "status", Text: event.Type}
	}
	return Segment{Channel: ChannelStatus, Label: "status", Text: fmt.Sprintf("%s %s", event.Type, CompactJSON(event.Payload))}
}

// FormatEvent renders an event into the plain-text line shown in stream output.
func FormatEvent(event Event) string {
	return formatSegment(renderEvent(event))
}

func formatSegment(segment Segment) string {
	text := strings.TrimSpace(segment.Text)
	if text == "" {
		return ""
	}
	label := strings.TrimSpace(segment.Label)
	if label == "" {
		return text
	}
	return label + ": " + text
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func joinOrFallback(parts []string, fallback string) string {
	if len(parts) == 0 {
		return fallback
	}
	return strings.Join(parts, " ")
}
