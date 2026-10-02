package main

import (
	"context"
	"errors"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
)

// driverRunRecord accumulates what a driver hosts' run function saw, since
// agent.DriverRunOutput carries only the conversation-shaped part of a run.
type driverRunRecord struct {
	mu         sync.Mutex
	tokenCount int
	stopReason agent.StopReason
	err        error
	onFailure  func(error)
}

// runFailure returns the error of the most recent run, or nil when it
// succeeded or was cancelled.
func (r *driverRunRecord) runFailure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *driverRunRecord) totals() (int, agent.StopReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokenCount, r.stopReason
}

func (r *driverRunRecord) record(res runResult, err error) {
	r.mu.Lock()
	r.tokenCount += res.TokenCount
	r.stopReason = res.StopReason
	r.err = nil
	if err != nil && !errors.Is(err, context.Canceled) {
		r.err = err
	}
	failure, onFailure := r.err, r.onFailure
	r.mu.Unlock()
	if failure != nil && onFailure != nil {
		onFailure(failure)
	}
}

// driverRun adapts the runner to agent.DriverRunFunc without installing a
// signal handler: the driver's host owns cancellation.
//
// groupScope is the delegation group scope of the driver's run stream; every
// episode of the driver runs sequentially inside it.
func (r cliRunner) driverRun(skillNames []string, groupScope string, rec *driverRunRecord) agent.DriverRunFunc {
	return func(ctx context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		res, err := r.run(ctx, in.Conversation, skillNames, runHooks{
			drainInbox:           in.DrainInbox,
			onToolBatchDone:      in.OnToolBatchDone,
			pendingSubAgents:     in.PendingSubAgents,
			maxTokens:            in.MaxTokens,
			delegationGroupScope: groupScope,
		})
		if rec != nil {
			rec.record(res, err)
		}
		return agent.DriverRunOutput{
			Conversation: res.Conversation,
			Lineage:      res.Lineage,
			TokenCount:   res.TokenCount,
			StopReason:   res.StopReason,
		}, err
	}
}

// openGroupScope allocates a delegation group scope for one sequential run
// stream and returns it with its release func. A runtime without a supervisor
// has no delegation, so it yields an empty scope and a no-op release.
func (rt *cliRuntime) openGroupScope() (string, func()) {
	if rt.delegationSupervisor == nil {
		return "", func() {}
	}
	scope := rt.delegationSupervisor.NewGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
	return scope, func() { rt.delegationSupervisor.ReleaseGroupScope(scope) }
}
