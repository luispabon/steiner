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
// signal handler: the driver's host owns cancellation. drainSteers is the
// legacy steer source, consulted whenever the driver's inbox has nothing.
func (r cliRunner) driverRun(skillNames []string, drainSteers func() []agent.SteerMessage, rec *driverRunRecord) agent.DriverRunFunc {
	legacy := agent.SteerInboxDrain(drainSteers)
	return func(ctx context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		drain := in.DrainInbox
		if legacy != nil {
			inner := in.DrainInbox
			drain = func() agent.InboxDrain {
				if d := inner(); d.Message != nil {
					return d
				}
				return legacy()
			}
		}
		res, err := r.run(ctx, in.Conversation, skillNames, runHooks{
			drainInbox:       drain,
			onToolBatchDone:  in.OnToolBatchDone,
			pendingSubAgents: in.PendingSubAgents,
			maxTokens:        in.MaxTokens,
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
