package oneshot

import (
	"context"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// PhaseRunner executes one autonomous phase run with caller-provided context.
type PhaseRunner interface {
	RunPhase(ctx context.Context, in PhaseRunInput) (RunResult, error)
}

// PhaseRunInput is the input of one phase run.
type PhaseRunInput struct {
	Conversation []agent.Message
	SkillNames   []string
	// Session is the phase session, created and linked in the manifest before
	// the phase runs; the runner saves every driver snapshot to it.
	Session PhaseSession
	// RegisterControl exposes the phase to user control for as long as it runs.
	// The runner calls it once its driver is live and calls the returned
	// release when the phase ends. Nil when nothing takes control, as in a
	// headless run.
	RegisterControl func(PhaseControl) (release func())
}

// PhaseControl routes user control to an active phase.
type PhaseControl interface {
	Submit(text string, images []agent.ImageBlock)
	NotifySteer()
	StopTurn()
	CancelAgent(agentID string, discard bool) error
	CancelAll() error
}

// PhaseSession identifies a phase's persistent session and saves to it.
type PhaseSession struct {
	ID   string
	Save func(ctx context.Context, snap agent.DriverSnapshot) error
}

// RunResult captures the outcome of a single phase run.
type RunResult struct {
	Conversation    []agent.Message
	Reply           string
	Diagnostics     []output.Event
	WorkflowHandoff *tool.WorkflowHandoffTransition
	Lineage         agent.ConversationLineage
	TokenCount      int
	StopReason      agent.StopReason
}
