package interactive

import "context"

// Action is the interface for all interactive-mode user actions.
type Action interface {
	isInteractiveAction()
}

// Controller is the interface for handling interactive-mode user actions.
// Implementations process actions such as prompt submission, approvals,
// interrupts, and model switches.
type Controller interface {
	Handle(ctx context.Context, action Action) error
}

// SubmitPrompt represents a user submitting a new prompt during an interactive
// session.
type SubmitPrompt struct{ Text string }

func (SubmitPrompt) isInteractiveAction() {}

// RequestContextReport represents a user request to view the current context
// report during an interactive session.
type RequestContextReport struct{}

func (RequestContextReport) isInteractiveAction() {}

// RequestConfigReport represents a user request to view the current
// configuration during an interactive session.
type RequestConfigReport struct{}

func (RequestConfigReport) isInteractiveAction() {}

// SubmitApproval represents a user decision on an outstanding approval
// request during an interactive session.
type SubmitApproval struct {
	Tool     string
	Mode     string
	Decision string
}

func (SubmitApproval) isInteractiveAction() {}

// InterruptActiveRun represents a user request to interrupt the currently
// active model run during an interactive session.
type InterruptActiveRun struct{}

func (InterruptActiveRun) isInteractiveAction() {}

// RequestExit represents a user request to exit the interactive session.
type RequestExit struct{}

func (RequestExit) isInteractiveAction() {}

// SetSkillEnabled represents a user request to enable or disable a skill
// during an interactive session.
type SetSkillEnabled struct {
	Name    string
	Enabled bool
}

func (SetSkillEnabled) isInteractiveAction() {}

// SwitchModel represents a user request to switch the active model during an
// interactive session.
type SwitchModel struct{ Name string }

func (SwitchModel) isInteractiveAction() {}

// ClearConversation represents a user request to clear the conversation
// history during an interactive session.
type ClearConversation struct{}

func (ClearConversation) isInteractiveAction() {}

// TriggerManualCompaction represents a user request to manually compact the
// conversation history during an interactive session.
type TriggerManualCompaction struct{}

func (TriggerManualCompaction) isInteractiveAction() {}

// LoadSession represents a user request to load a previously saved session
// into the current interactive session, replacing the current conversation.
type LoadSession struct{ SessionID string }

func (LoadSession) isInteractiveAction() {}

// RequestSessionPicker represents a user request to select and load a saved
// session. The TUI interprets this and emits the LoadSession action.
type RequestSessionPicker struct{}

func (RequestSessionPicker) isInteractiveAction() {}
