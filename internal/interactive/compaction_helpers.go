package interactive

import (
	"context"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

func (s *Session) compactRunner(conversation []agent.Message, lineage agent.ConversationLineage, steering string) func(context.Context) ([]agent.Message, bool, error) {
	return func(ctx context.Context) ([]agent.Message, bool, error) {
		runner := s.currentRunner()
		if lc, ok := runner.(lineageCompactor); ok {
			return lc.CompactWithLineage(ctx, conversation, lineage, snapshotTools(s.snapshots), steering)
		}
		compacted, err := runner.Compact(ctx, conversation, snapshotTools(s.snapshots), steering)
		return compacted, false, err
	}
}

func splitSummaryPrefix(messages []agent.Message) (prefix, rest []agent.Message) {
	for i, msg := range messages {
		if msg.Role != agent.MessageRoleSummary {
			return messages[:i], messages[i:]
		}
	}
	return messages, nil
}

func (s *Session) runManualCompaction(ctx context.Context, model string, run func(context.Context) ([]agent.Message, bool, error)) (result []agent.Message, cleared bool, err error) {
	runCtx, cancel := context.WithCancel(ctx)
	token := s.runController.Set(cancel)
	defer func() {
		cancel()
		s.runController.Release(token)

		reason := "complete"
		if err != nil {
			if errors.Is(err, context.Canceled) {
				reason = "cancelled"
			} else {
				reason = "error"
			}
		}
		s.events.Emit(output.NewRunFinishedEvent(0, reason, "", "", err))
	}()

	s.events.Emit(output.NewRunStartedEvent("interactive", model, "", 0, 0))
	s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind: "compaction", Scope: "conversation", Severity: "compacting", Notes: []string{"starting compaction"},
	}))
	return run(runCtx)
}

func (s *Session) emitCompactError(err error) {
	s.events.Emit(output.Event{Type: output.EventTypeStopReason, Payload: output.StopReasonEvent{Reason: fmt.Sprintf("Compaction error: %v", err)}})
}

func manualCompactionHasSource(messages []agent.Message) bool {
	return agent.HasCompactionSource(messages)
}

// snapshotTools returns the tools the last parent request sent, so manual
// compaction is built from the same prefix a normal turn would replay. Child
// sub-agent requests carry restricted tool sets and must not change the
// compaction prefix, so this reads the parent-only snapshot rather than the
// latest-overall one. The runExecutor seam (cliRunner.Compact) clones them
// before handing them to the agent runner; here we pass the reference without
// duplicating it. Returns nil when no parent request has been recorded.
func snapshotTools(store *SnapshotStore) []provider.ToolSpec {
	snapshot, ok := store.ParentSnapshot()
	if !ok {
		return nil
	}
	return snapshot.Tools
}
