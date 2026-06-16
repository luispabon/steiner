package interactive

import (
	"context"

	"github.com/luispabon/steiner/internal/agent"
)

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
type SubmitPrompt struct {
	Text   string
	Images []agent.ImageBlock
}

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

// SubmitWorkflowHandoff represents a user decision on an outstanding workflow
// handoff request during an interactive session.
type SubmitWorkflowHandoff struct {
	Decision string
}

func (SubmitWorkflowHandoff) isInteractiveAction() {}

// WorkflowHandoffModelSelection describes the model alias preselected for a
// pending workflow handoff along with the concise source label shown in the UI.
type WorkflowHandoffModelSelection struct {
	ModelAlias  string
	SourceLabel string
}

// WorkflowHandoffModelSelector resolves the preselected model for a workflow
// handoff destination without mutating the active session model.
type WorkflowHandoffModelSelector interface {
	WorkflowHandoffModelSelection(destination string) WorkflowHandoffModelSelection
}

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

// ToggleCaveHuman represents a user request to toggle cave_human mode on or
// off during an interactive session.
type ToggleCaveHuman struct{}

func (ToggleCaveHuman) isInteractiveAction() {}

// LoadSession represents a user request to load a previously saved session
// into the current interactive session, replacing the current conversation.
type LoadSession struct{ SessionID string }

func (LoadSession) isInteractiveAction() {}

// SteerPrompt represents a user steering an in-progress run by queuing a
// message.
type SteerPrompt struct{ Text string }

func (SteerPrompt) isInteractiveAction() {}

type requestSessionPicker struct{}

func (requestSessionPicker) isInteractiveAction() {}

// RotateSession generates a new session ID and clears the session title,
// giving the next workflow a clean identity independent of the current session.
type RotateSession struct{}

func (RotateSession) isInteractiveAction() {}

// ForkSession forks the current live session, saving it first before forking.
type ForkSession struct{}

func (ForkSession) isInteractiveAction() {}

// ForkSavedSession forks a previously saved session by ID.
type ForkSavedSession struct{ SessionID string }

func (ForkSavedSession) isInteractiveAction() {}
