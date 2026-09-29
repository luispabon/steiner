package delegation

import (
	"context"
	"sort"

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
// dropped and their waiters receive ErrSupervisorClosed. Shutdown never
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
		defer s.mu.Unlock()
		for _, state := range s.jobs {
			if state.phase != phaseRunning {
				continue
			}
			s.report.Unjoined = append(s.report.Unjoined, UnjoinedChild{
				AgentID:      state.job.AgentID,
				AgentType:    state.job.AgentType,
				ParentCallID: state.job.ParentCallID,
				WorktreePath: state.job.Worktree.Path,
			})
			if state.job.Worktree.Path != "" {
				s.protected[state.job.Worktree.Path] = struct{}{}
			}
			deliverLocked(state, tool.ExecutionResult{}, ErrSupervisorClosed)
		}
		sort.Slice(s.report.Unjoined, func(i, j int) bool {
			return s.report.Unjoined[i].AgentID < s.report.Unjoined[j].AgentID
		})
		s.closed = true
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	return ShutdownReport{Unjoined: append([]UnjoinedChild(nil), s.report.Unjoined...)}
}
