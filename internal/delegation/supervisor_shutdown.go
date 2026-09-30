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
// min(ctx, JoinTimeout) for their goroutines to exit. Children still running
// are reported as unjoined and their worktrees protected; their late results are
// dropped and their waiters receive ErrSupervisorClosed. Unjoined children are
// posted once to the completion sink as quiet cancelled completions before the
// supervisor closes, together with any results still held by a group that will
// never be sealed or completed; nothing is posted afterwards. Shutdown never
// finalises a child itself. It is idempotent: later calls return the first report.
func (s *Supervisor) Shutdown(ctx context.Context, cause CancelCause) ShutdownReport {
	s.shutdown.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()

		s.CancelAll(cause)

		s.mu.Lock()
		var exits []chan struct{}
		for _, state := range s.jobs {
			if state.phase == phaseRunning {
				exits = append(exits, state.exited)
			}
		}
		s.mu.Unlock()

		waitCtx, cancel := context.WithTimeout(ctx, s.joinTimeout)
		defer cancel()
	wait:
		for _, exited := range exits {
			select {
			case <-exited:
			case <-waitCtx.Done():
				break wait
			}
		}

		s.mu.Lock()
		var unjoined []*jobState
		for _, state := range s.jobs {
			if state.phase == phaseRunning {
				unjoined = append(unjoined, state)
			}
		}
		sort.Slice(unjoined, func(i, j int) bool {
			return unjoined[i].job.AgentID < unjoined[j].job.AgentID
		})
		var batch []agent.SubAgentCompletion
		for _, state := range unjoined {
			path := state.worktree.Path
			s.report.Unjoined = append(s.report.Unjoined, UnjoinedChild{
				AgentID:      state.job.AgentID,
				AgentType:    state.job.AgentType,
				ParentCallID: state.job.ParentCallID,
				WorktreePath: path,
			})
			if path != "" {
				s.protected[path] = struct{}{}
			}
			if state.completion == nil {
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
					batch = append(batch, *completion)
				}
			}
			deliverLocked(state, tool.ExecutionResult{}, ErrSupervisorClosed)
		}
		batch = append(batch, s.releaseAllGroupsLocked()...)
		sort.Slice(batch, func(i, j int) bool { return batch[i].Seq < batch[j].Seq })
		var posts postList
		if s.sink != nil && len(batch) > 0 {
			posts = postList{sink: s.sink, batches: [][]agent.SubAgentCompletion{batch}}
		}
		s.closed = true
		s.mu.Unlock()

		posts.deliver()
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	return ShutdownReport{Unjoined: append([]UnjoinedChild(nil), s.report.Unjoined...)}
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
			}
		}
		delete(s.groups, group.key)
	}
	return released
}
