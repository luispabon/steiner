package delegation

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

const cancellationCleanupTimeout = 30 * time.Second

const (
	cancelledSessionRetentionPhrase = "the child session is preserved and can be resumed with follow_up"
	cancelledSessionDiscardNotice   = "session discarded and code worktree removed by request"
)

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

	delegationBase := filepath.Join(projectRoot, ".steiner", "worktrees")
	relID, err := filepath.Rel(delegationBase, worktree.Path)
	if err != nil {
		emitDisposal(false, err.Error())
		return
	}

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cancellationCleanupTimeout)
	defer cleanupCancel()
	removed, err := PruneCodeWorktree(cleanupCtx, projectRoot, relID)
	if err != nil {
		emitDisposal(removed, err.Error())
		return
	}
	emitDisposal(removed, "")
}
