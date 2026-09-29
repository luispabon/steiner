package interactive

import (
	"context"
	"errors"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

// manualCompaction returns the compaction the driver runs when it is next not
// generating. The compaction only ever adds a lineage generation to the
// driver's own lineage, so earlier generations survive. A failed or empty
// compaction leaves the conversation as it was.
func (s *Session) manualCompaction(drv *agent.ConversationDriver, steering string) func(context.Context, []agent.Message) ([]agent.Message, agent.ConversationLineage, error) {
	return func(ctx context.Context, conversation []agent.Message) ([]agent.Message, agent.ConversationLineage, error) {
		lineage := drv.Snapshot().Lineage
		compacted, ok := s.compactConversation(ctx, conversation, steering)
		if !ok {
			return conversation, lineage, nil
		}
		prefix, rest := splitSummaryPrefix(compacted)
		lineage = lineage.WithNewGeneration(prefix, rest)
		return lineage.FullMessages(), lineage, nil
	}
}

// compactConversation runs one manual compaction and reports whether it
// produced a new conversation.
func (s *Session) compactConversation(ctx context.Context, conversation []agent.Message, steering string) ([]agent.Message, bool) {
	if len(conversation) == 0 {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", "No conversation to compact."))
		return nil, false
	}
	if !manualCompactionHasSource(conversation) {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", "Nothing to compact yet; need at least two conversation turns."))
		return nil, false
	}

	compacted, err := s.runManualCompaction(ctx, s.CurrentModelAlias(), s.compactRunner(conversation, steering))
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.emitCompactError(err)
		}
		return nil, false
	}
	return compacted, true
}
