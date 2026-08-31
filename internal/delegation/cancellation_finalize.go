package delegation

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

const cancellationCleanupTimeout = 30 * time.Second

const (
	cancelledSessionRetentionPhrase = "the child session is preserved and can be resumed with follow_up"
	cancelledSessionDiscardNotice   = "session discarded and code worktree removed by request"
)

func pruneCodeWorktree(projectRoot string, worktree CodeWorktree) (bool, error) {
	delegationBase := filepath.Join(projectRoot, ".steiner", "worktrees")
	relID, err := filepath.Rel(delegationBase, worktree.Path)
	if err != nil {
		return false, err
	}

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cancellationCleanupTimeout)
	defer cleanupCancel()
	return PruneCodeWorktree(cleanupCtx, projectRoot, relID)
}

func applyFinalizeCancellation(events output.EventSink, store *SessionStore, controller *ActiveController, projectRoot, agentID string, result *tool.ExecutionResult) {
	dr, ok := result.Value.(Result)
	if !ok {
		return
	}
	finalizeDelegateCancellation(events, store, controller, projectRoot, agentID, &dr)
	result.Value = dr
	if result.Retention != nil {
		result.Retention.Summary = dr.Summary
	}
}

// finalizeDelegateCancellation invalidates and optionally prunes a child
// after its runner has returned. It never derives cleanup from the child context.
// An explicit discard request is authoritative even when the returned result is
// complete because cancellation and completion are linearized by the controller.
func finalizeDelegateCancellation(events output.EventSink, store *SessionStore, controller *ActiveController, projectRoot, agentID string, result *Result) {
	if result == nil || controller == nil || !controller.DiscardRequested(agentID) {
		return
	}

	if store != nil {
		store.Invalidate(agentID)
	}
	result.SessionResumable = false
	result.clearPersistence()
	result.Output = strings.ReplaceAll(result.Output, cancelledSessionRetentionPhrase, cancelledSessionDiscardNotice)
	result.Summary = strings.ReplaceAll(result.Summary, cancelledSessionRetentionPhrase, cancelledSessionDiscardNotice)

	emitDisposal := func(removed bool, errMsg string) {
		if events == nil {
			return
		}
		event := output.NewDelegationWorktreeDisposalEvent(agentID, removed, errMsg)
		event = output.WithAgentScope(event, agentID)
		if agentType, ok := controller.TypeFor(agentID); ok {
			event = output.WithAgentTypeScope(event, string(agentType))
		}
		events.Emit(event)
	}

	worktree, ok := controller.WorktreeFor(agentID)
	if !ok || worktree.Path == "" {
		emitDisposal(false, "no owned code worktree to discard")
		return
	}

	removed, err := pruneCodeWorktree(projectRoot, worktree)
	if err != nil {
		emitDisposal(removed, err.Error())
		return
	}
	emitDisposal(removed, "")
}
