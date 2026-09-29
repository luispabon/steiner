package delegation

import (
	"context"
	"time"
)

// UnjoinedChild describes a child that did not complete before Shutdown's timeout.
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

// Shutdown stops accepting new spawns, cancels all children, waits with a timeout,
// reports any unjoin and their protected worktrees, and closes the supervisor.
// It is idempotent and returns the same report on subsequent calls.
func (s *Supervisor) Shutdown(ctx context.Context, cause CancelCause) ShutdownReport {
	var report ShutdownReport

	s.shutdownOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.shuttingDown = true
		s.shutdownCause = cause
		s.mu.Unlock()

		s.CancelAll(cause)

		timeout := s.joinTimeout
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining < timeout {
				timeout = remaining
			}
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		s.waitForIdle(shutdownCtx)

		s.mu.Lock()
		for agentID, state := range s.jobs {
			if !state.finished {
				var worktreePath string
				if c, ok := s.controller.WorktreeFor(agentID); ok {
					worktreePath = c.Path
				}
				agentType, _ := s.controller.TypeFor(agentID)
				var parentCallID string
				if _, exists := s.jobs[agentID]; exists {
					for _, q := range s.queue {
						if q.job.AgentID == agentID {
							parentCallID = q.job.ParentCallID
							break
						}
					}
					if parentCallID == "" {
						parentCallID = agentID
					}
				}

				report.Unjoined = append(report.Unjoined, UnjoinedChild{
					AgentID:      agentID,
					AgentType:    agentType,
					ParentCallID: parentCallID,
					WorktreePath: worktreePath,
				})

				if worktreePath != "" {
					s.protected[worktreePath] = struct{}{}
				}

				if !state.finished {
					state.err = ErrSupervisorClosed
					if !state.doneClosed {
						state.doneClosed = true
						s.jobs[agentID] = state
						close(state.done)
					}
				}
			}
		}
		s.mu.Unlock()

		s.shutdownReport = &report
	})

	if s.shutdownReport != nil {
		return *s.shutdownReport
	}
	return report
}

// waitForIdle blocks until all running jobs have finished, or ctx times out.
func (s *Supervisor) waitForIdle(ctx context.Context) {
	select {
	case <-s.idle:
		return
	case <-ctx.Done():
		return
	}
}
