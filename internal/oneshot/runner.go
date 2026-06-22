package oneshot

import (
	"context"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// PhaseRunner executes one autonomous phase run with caller-provided context.
type PhaseRunner interface {
	RunPhase(ctx context.Context, conversation []agent.Message, skillNames []string, drainSteers func() []agent.SteerMessage) (RunResult, error)
}

// RunResult captures the outcome of a single phase run.
type RunResult struct {
	Conversation    []agent.Message
	Reply           string
	Diagnostics     []output.Event
	WorkflowHandoff *tool.WorkflowHandoffTransition
	Lineage         agent.ConversationLineage
}
