package interactive

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

// driverRun is the ConversationDriver's run function: it adapts the session's
// runExecutor to one turn sequence. A run error is reported as a stop-reason
// event here and not returned, so the driver does not warn a second time.
func (s *Session) driverRun(ctx context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
	result, err := s.currentRunner().Run(ctx, RunInput{
		Conversation:     in.Conversation,
		DrainInbox:       in.DrainInbox,
		OnToolBatchDone:  in.OnToolBatchDone,
		PendingSubAgents: in.PendingSubAgents,
		MaxTokens:        in.MaxTokens,
	})
	if err != nil {
		s.events.Emit(output.NewStopReasonEvent(0, fmt.Sprintf("Error: %v", err), err))
	}

	out := agent.DriverRunOutput{
		Conversation: result.Conversation,
		TokenCount:   result.TokenCount,
		StopReason:   result.StopReason,
		SkipAdoption: result.WorkflowHandoff != nil,
	}
	runLineage := result.Lineage
	if runLineage.Empty() && len(result.Conversation) > 0 {
		runLineage = lineageFromMessages(result.Conversation)
	}
	if !runLineage.Empty() {
		out.Lineage = mergeRunLineage(in.Lineage, runLineage)
	}
	return out, nil
}

// prepareTurn supplies the mode notice and skill deltas prefixed to the first
// message of every sequence the driver starts.
func (s *Session) prepareTurn(ctx context.Context, conv []agent.Message) agent.DeliveryParts {
	return agent.DeliveryParts{
		ModeNotice:  s.modeNotice(),
		SkillBlocks: s.skillDeltaBlocks(ctx, conv),
	}
}

// lineageFromMessages is the single-generation lineage of messages.
func lineageFromMessages(messages []agent.Message) agent.ConversationLineage {
	return agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{ID: 1, SummaryPrefix: nil, Messages: cloneMessages(messages)},
		},
		NextGenerationID: 2,
	}
}

// mergeRunLineage folds a run's lineage into the conversation's full lineage.
// The runner always starts from a single flat generation, so its first
// generation replaces the prior latest one and any generations it added by
// compacting are appended. Earlier generations are kept untouched.
func mergeRunLineage(prior, run agent.ConversationLineage) agent.ConversationLineage {
	if prior.Empty() {
		return run.Clone()
	}
	prefix, rest := splitSummaryPrefix(run.Generations[0].FullMessages())
	merged := prior.Clone()
	latest := &merged.Generations[len(merged.Generations)-1]
	latest.SummaryPrefix = cloneMessages(prefix)
	latest.Messages = cloneMessages(rest)
	for _, generation := range run.Generations[1:] {
		merged = merged.WithNewGeneration(generation.SummaryPrefix, generation.Messages)
	}
	return merged
}
