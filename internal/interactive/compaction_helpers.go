package interactive

import (
	"context"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

func (s *Session) compactRunner(conversation []agent.Message, steering string) func(context.Context) ([]agent.Message, error) {
	return func(ctx context.Context) ([]agent.Message, error) {
		return s.deps.Runner.Compact(ctx, conversation, s.skills.Snapshot(), snapshotTools(s.snapshots), steering)
	}
}

func (s *Session) setCompactedConversation(conversation []agent.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conversation = cloneMessages(conversation)
	s.lineage = agent.ConversationLineage{
		Generations:      []agent.ConversationGeneration{{ID: 1, Messages: cloneMessages(conversation)}},
		NextGenerationID: 2,
	}
}

func (s *Session) runManualCompaction(ctx context.Context, model string, run func(context.Context) ([]agent.Message, error)) (result []agent.Message, err error) {
	runCtx, cancel := context.WithCancel(ctx)
	s.runController.Set(cancel)
	defer func() {
		cancel()
		s.runController.Clear()

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

// snapshotTools returns the tools the last request sent, so the compaction is
// built from the same prefix a normal turn would replay. The runExecutor seam
// (cliRunner.Compact) clones them before handing them to the agent runner; here
// we pass the reference without duplicating it.
func snapshotTools(store *SnapshotStore) []provider.ToolSpec {
	snapshot, ok := store.Snapshot()
	if !ok {
		return nil
	}
	return snapshot.Tools
}
