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
// compaction leaves the conversation as it was. A compaction that applied no
// stage keeps the conversation and generation IDs, but still drops backend
// state the runner reported as cleared.
func (s *Session) manualCompaction(drv *agent.ConversationDriver, steering string) func(context.Context, []agent.Message) ([]agent.Message, agent.ConversationLineage, error) {
	return func(ctx context.Context, conversation []agent.Message) ([]agent.Message, agent.ConversationLineage, error) {
		lineage := drv.Snapshot().Lineage
		lastMainRequest := drv.LastMainRequest()
		compacted, cleared, ok := s.compactConversation(ctx, conversation, lineage, lastMainRequest, steering)
		if cleared {
			// The provider retired the live transcript this generation was summarized
			// from, so its stored state must go even when no stage applied.
			lineage = lineage.WithoutLatestBackendState()
		}
		if !ok {
			return conversation, lineage, nil
		}
		prefix, rest := splitSummaryPrefix(compacted)
		lineage = lineage.WithNewGeneration(prefix, rest)
		return lineage.FullMessages(), lineage, nil
	}
}

// compactConversation runs one manual compaction. It reports the new
// conversation, whether the provider cleared the lineage's latest backend state,
// and whether a compaction stage applied. A compaction that applied nothing
// reports false, so the caller keeps its conversation and generation IDs. An
// error leaves the lineage unchanged and reports cleared false: a clear the
// runner reported before the error is not surfaced.
func (s *Session) compactConversation(ctx context.Context, conversation []agent.Message, lineage agent.ConversationLineage, lastMainRequest agent.LastMainRequest, steering string) ([]agent.Message, bool, bool) {
	if len(conversation) == 0 {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", "No conversation to compact."))
		return nil, false, false
	}
	if !manualCompactionHasSource(conversation) {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", "Nothing to compact yet; need at least two conversation turns."))
		return nil, false, false
	}

	compacted, cleared, applied, err := s.runManualCompaction(ctx, s.CurrentModelAlias(), s.compactRunner(conversation, lineage, lastMainRequest, steering))
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.emitCompactError(err)
		}
		return nil, false, false
	}
	return compacted, cleared, applied
}
