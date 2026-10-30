package interactive

import (
	"context"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

// driverRun is the ConversationDriver's run function: it adapts the session's
// runExecutor to one turn sequence. A run error is reported as a stop-reason
// event here unless the runner already emitted one (agent.ReportedError), and
// is not returned, so the driver does not warn a second time.
func (s *Session) driverRun(ctx context.Context, in agent.DriverRunInput, scope string) (agent.DriverRunOutput, error) {
	result, err := s.currentRunner().Run(ctx, RunInput{
		Conversation:         in.Conversation,
		Lineage:              in.Lineage,
		DrainInbox:           in.DrainInbox,
		PendingSubAgents:     in.PendingSubAgents,
		MaxTokens:            in.MaxTokens,
		DelegationGroupScope: scope,
		ParentUsage:          in.ParentUsage,
		LastMainRequest:      in.LastMainRequest,
	})
	if err != nil {
		var reported *agent.ReportedError
		if !errors.As(err, &reported) {
			s.events.Emit(output.NewStopReasonEvent(0, fmt.Sprintf("Error: %v", err), err))
		}
		if !errors.Is(err, context.Canceled) {
			conv := result.Conversation
			if len(conv) == 0 {
				conv = in.Conversation
			}
			s.emitUnansweredResults(conv, err.Error())
		}
	}

	// The driver adopts the output even on error (it receives a nil error), so
	// its last-main-request carry rule is applied here.
	lastMainRequest := agent.CarryLastMainRequest(in.LastMainRequest, result.LastMainRequest, err)
	out := agent.DriverRunOutput{
		Conversation:    result.Conversation,
		TokenCount:      result.TokenCount,
		StopReason:      result.StopReason,
		SkipAdoption:    result.WorkflowHandoff != nil,
		ParentUsage:     result.ParentUsage,
		LastMainRequest: lastMainRequest,
	}
	runLineage := result.Lineage
	// A run that returns no lineage falls back to its conversation. That fallback
	// carries no backend state, so the prior state is cleared and the next run
	// reseeds. Failed and cancelled runs return their lineage and keep its state.
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
// compacting are appended. Earlier generations are kept untouched. The latest
// generation's backend state is the run's, so a state the run cleared or
// replaced is not resurrected from prior.
func mergeRunLineage(prior, run agent.ConversationLineage) agent.ConversationLineage {
	if prior.Empty() {
		return run.Clone()
	}
	prefix, rest := splitSummaryPrefix(run.Generations[0].FullMessages())
	merged := prior.Clone()
	latest := &merged.Generations[len(merged.Generations)-1]
	latest.SummaryPrefix = cloneMessages(prefix)
	latest.Messages = cloneMessages(rest)
	merged = merged.WithLatestBackendState(run.Generations[0].BackendState)
	for _, generation := range run.Generations[1:] {
		merged = merged.WithNewGenerationState(generation.SummaryPrefix, generation.Messages, generation.BackendState)
	}
	return merged
}
