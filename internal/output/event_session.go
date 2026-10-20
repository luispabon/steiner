package output

import (
	"fmt"
	"strings"
)

// HistoryLoadedEvent carries previously recorded prompt strings for display.
type HistoryLoadedEvent struct {
	Prompts []string `json:"prompts"`
}

// ConversationStateEvent is the conversation driver's state for the UI: State
// is "idle", "generating" or "waiting", Pending counts undelivered sub-agents.
type ConversationStateEvent struct {
	State           string `json:"state"`
	Held            bool   `json:"held"`
	Pending         int    `json:"pending"`
	BudgetExhausted bool   `json:"budget_exhausted"`
}

// ConversationWarningEvent records a non-fatal conversation driver failure.
type ConversationWarningEvent struct {
	Message string `json:"message"`
}

// DisplayFilePayload is the payload for EventTypeDisplayFile.
type DisplayFilePayload struct {
	Path    string          `json:"path"`
	Offset  int             `json:"offset,omitempty"`
	Limit   int             `json:"limit,omitempty"`
	Preview PreviewDocument `json:"preview"`
}

// ModeChangedEvent is emitted when the execution mode changes.
type ModeChangedEvent struct {
	Mode string `json:"mode"`
}

// OrchestrationLevelChangedEvent is emitted when the orchestration level changes.
type OrchestrationLevelChangedEvent struct {
	Level string `json:"level"`
}

// SandboxStatusEvent is emitted when the sandbox status is determined at startup.
type SandboxStatusEvent struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// ConfigWarningEvent carries a user-facing configuration warning message.
type ConfigWarningEvent struct {
	Message string `json:"message"`
}

// SkillTruncatedEvent is the payload for EventTypeSkillTruncated. It names the
// skill whose SKILL.md body exceeded the cap and reports the original body size
// and the cap that was applied.
type SkillTruncatedEvent struct {
	Name      string `json:"name"`
	SizeBytes int    `json:"size_bytes"`
	CapBytes  int    `json:"cap_bytes"`
}

// Message renders the user-facing truncation warning. The stream renderer and
// the TUI share it so the wording is defined once.
func (e SkillTruncatedEvent) Message() string {
	return fmt.Sprintf(
		"warning: skill %s is %d bytes, over the %d-byte skill cap; truncated to %d bytes",
		e.Name, e.SizeBytes, e.CapBytes, e.CapBytes,
	)
}

// SkillStateEnabled marks a skill as active in the latest effective set.
const SkillStateEnabled = "enabled"

// SkillStateDisabled marks a skill as no longer active.
const SkillStateDisabled = "disabled"

// SkillStateEvent is the payload for EventTypeSkillState. It records a
// persisted skill block's name and enabled/disabled state during replay.
type SkillStateEvent struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// ConversationResetEvent is the payload for EventTypeConversationReset. It
// carries no fields: the event itself is the signal.
type ConversationResetEvent struct{}

// NewHistoryLoadedEvent creates a new history loaded event.
func NewHistoryLoadedEvent(prompts []string) Event {
	return newEvent(EventTypeHistoryLoaded, HistoryLoadedEvent{Prompts: prompts})
}

// NewConversationStateEvent creates the event reporting the conversation
// driver's state, held flag and pending sub-agent count.
func NewConversationStateEvent(state string, held bool, pending int, budgetExhausted bool) Event {
	return newEvent(EventTypeConversationState, ConversationStateEvent{
		State:           state,
		Held:            held,
		Pending:         pending,
		BudgetExhausted: budgetExhausted,
	})
}

// NewConversationWarningEvent creates the event reporting a non-fatal
// conversation driver failure such as a failed save.
func NewConversationWarningEvent(message string) Event {
	return newEvent(EventTypeConversationWarning, ConversationWarningEvent{Message: message})
}

// NewDisplayFileEvent creates a DisplayFile event with an explicit preview
// payload for the TUI to render.
func NewDisplayFileEvent(payload DisplayFilePayload) Event {
	return newEvent(EventTypeDisplayFile, payload)
}

// NewModeChangedEvent creates a mode_changed event.
func NewModeChangedEvent(mode string) Event {
	return newEvent(EventTypeModeChanged, ModeChangedEvent{
		Mode: strings.TrimSpace(mode),
	})
}

// NewOrchestrationLevelChangedEvent creates an orchestration_level_changed event.
func NewOrchestrationLevelChangedEvent(level string) Event {
	return newEvent(EventTypeOrchestrationLevelChanged, OrchestrationLevelChangedEvent{
		Level: strings.TrimSpace(level),
	})
}

// NewSandboxStatusEvent creates a sandbox_status event.
func NewSandboxStatusEvent(status, message string) Event {
	return newEvent(EventTypeSandboxStatus, SandboxStatusEvent{
		Status:  strings.TrimSpace(status),
		Message: strings.TrimSpace(message),
	})
}

// NewConfigWarningEvent creates a config_warning event.
func NewConfigWarningEvent(message string) Event {
	return newEvent(EventTypeConfigWarning, ConfigWarningEvent{Message: strings.TrimSpace(message)})
}

// NewSkillTruncatedEvent creates a skill_truncated event warning that a skill's
// body exceeded capBytes and was truncated.
func NewSkillTruncatedEvent(name string, sizeBytes, capBytes int) Event {
	return newEvent(EventTypeSkillTruncated, SkillTruncatedEvent{
		Name:      strings.TrimSpace(name),
		SizeBytes: sizeBytes,
		CapBytes:  capBytes,
	})
}

// NewSkillStateEvent creates a skill_state event recording a persisted skill
// block's enabled/disabled state.
func NewSkillStateEvent(name, state string) Event {
	return newEvent(EventTypeSkillState, SkillStateEvent{
		Name:  strings.TrimSpace(name),
		State: strings.TrimSpace(state),
	})
}

// NewConversationResetEvent creates the conversation_reset event that tells
// display consumers a loaded session is about to be replayed over the previous
// conversation.
func NewConversationResetEvent() Event {
	return newEvent(EventTypeConversationReset, ConversationResetEvent{})
}
