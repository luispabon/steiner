package delegation

import (
	"context"
	"sort"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

// UnjoinedChild describes a child that did not exit before Shutdown's timeout.
type UnjoinedChild struct {
	AgentID      string
	AgentType    AgentType
	ParentCallID string
	WorktreePath string
}

// ShutdownReport contains the results of a Shutdown operation.
type ShutdownReport struct {
	Unjoined []UnjoinedChild
}

// Shutdown rejects new spawns, cancels every child and waits up to
// min(ctx, JoinTimeout) for execution, publication, and cancellation finalizers.
// Timeout outcomes remain authoritative if child work returns later. Shutdown
// is idempotent.
func (s *Supervisor) Shutdown(ctx context.Context, cause CancelCause) ShutdownReport {
	s.shutdown.Do(func() {
		waitCtx, cancel := context.WithTimeout(ctx, s.joinTimeout)
		defer cancel()

		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()

		s.CancelAll(cause)

		s.mu.Lock()
		var waits []chan struct{}
		for _, state := range s.jobs {
			if !state.published {
				waits = append(waits, state.publication)
			}
			if state.phase == phaseRunning {
				waits = append(waits, state.exited)
			}
			if state.cause != CancelCauseNone {
				waits = append(waits, state.settled)
			}
		}
		s.mu.Unlock()

		for _, wait := range waits {
			select {
			case <-wait:
			case <-waitCtx.Done():
				goto settle
			}
		}

	settle:

		s.mu.Lock()
		posts := s.settleShutdownLocked()
		s.mu.Unlock()
		posts.deliver()
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	return ShutdownReport{Unjoined: append([]UnjoinedChild(nil), s.report.Unjoined...)}
}

func (s *Supervisor) settleShutdownLocked() postList {
	var batch []agent.SubAgentCompletion
	for _, state := range s.jobs {
		s.settleShutdownJobLocked(state, &batch)
	}
	batch = append(batch, s.releaseAllGroupsLocked()...)
	sort.Slice(s.report.Unjoined, func(i, j int) bool { return s.report.Unjoined[i].AgentID < s.report.Unjoined[j].AgentID })
	sort.Slice(batch, func(i, j int) bool { return batch[i].Seq < batch[j].Seq })
	s.closed = true
	if s.sink == nil || len(batch) == 0 {
		return postList{}
	}
	return postList{sink: s.sink, batches: [][]agent.SubAgentCompletion{batch}}
}

func (s *Supervisor) settleShutdownJobLocked(state *jobState, batch *[]agent.SubAgentCompletion) {
	if state.phase == phaseRunning {
		path := state.worktree.Path
		s.report.Unjoined = append(s.report.Unjoined, UnjoinedChild{AgentID: state.job.AgentID, AgentType: state.job.AgentType, ParentCallID: state.job.ParentCallID, WorktreePath: path})
		if path != "" {
			s.protected[path] = struct{}{}
		}
	}
	if state.phase == phaseDone && state.finalized {
		return
	}
	state.shutdownTimedOut = true
	if state.published && state.completion == nil {
		completion := s.newCompletionLocked(state)
		completion.Status = string(StatusCancelled)
		completion.Quiet = true
		completion.Body = agent.FailureBody(completion.Status, "sub-agent did not stop before shutdown")
		state.completion = completion
		switch {
		case state.blocking:
		case state.group != nil:
			state.held = true
		default:
			state.routed = true
			*batch = append(*batch, *completion)
		}
	}
	if state.published {
		deliverLocked(state, tool.ExecutionResult{}, ErrSupervisorClosed)
	}
}

// releaseAllGroupsLocked force-releases every remaining group in creation
// order, returning the completions still held. Blocking jobs never post.
func (s *Supervisor) releaseAllGroupsLocked() []agent.SubAgentCompletion {
	if s.sink == nil {
		return nil
	}
	groups := make([]*jobGroup, 0, len(s.groups))
	for _, group := range s.groups {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].order < groups[j].order })
	var released []agent.SubAgentCompletion
	for _, group := range groups {
		for _, member := range group.members {
			if member.completion != nil && member.held && !member.blocking {
				released = append(released, *member.completion)
				member.held = false
				member.routed = true
			}
		}
		delete(s.groups, group.key)
	}
	return released
}
