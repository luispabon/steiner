package delegation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

// ErrOutstandingCap indicates that too many sub-agents are already outstanding.
var ErrOutstandingCap = errors.New("sub-agent outstanding cap exceeded")

type outstandingCapError struct{ outstanding int }

func (e outstandingCapError) Error() string {
	return fmt.Sprintf("%d sub-agents already outstanding; wait for results before dispatching more", e.outstanding)
}

func (e outstandingCapError) Is(target error) bool { return target == ErrOutstandingCap }

// enqueue applies the outstanding cap, registers the job in the supervisor's
// table and either starts it (running < MaxParallel) or appends it to the FIFO
// queue. The controller only ever sees started jobs.
func (s *Supervisor) enqueue(handlerCtx context.Context, job ChildJob, blocking bool) (*jobState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closing {
		return nil, ErrSupervisorClosed
	}
	if existing, ok := s.jobs[job.AgentID]; ok && !existing.acked {
		return nil, ErrAgentAlreadyActive
	}
	if outstanding := s.running + len(s.queue); outstanding >= 2*s.maxParallel {
		return nil, outstandingCapError{outstanding: outstanding}
	}

	childCtx, cancel := context.WithCancel(context.WithoutCancel(handlerCtx))
	s.enqSeq++
	state := &jobState{
		job:      job,
		childCtx: childCtx,
		cancel:   cancel,
		phase:    phaseQueued,
		blocking: blocking,
		order:    s.enqSeq,
		worktree: job.Worktree,
		done:     make(chan struct{}),
		exited:   make(chan struct{}),
	}
	if !blocking {
		s.enrollLocked(state, agent.ToolBatchIDFrom(handlerCtx))
	}
	s.jobs[job.AgentID] = state
	s.queue = append(s.queue, state)
	s.startQueuedLocked()
	state.wasQueued = state.phase == phaseQueued
	return state, nil
}

// startQueuedLocked starts queued jobs in FIFO order while slots are free.
func (s *Supervisor) startQueuedLocked() {
	for s.running < s.maxParallel && len(s.queue) > 0 {
		state := s.queue[0]
		s.queue = s.queue[1:]
		state.phase = phaseRunning
		state.startedAt = time.Now()
		s.running++
		if state.job.Prepare == nil {
			err := s.controller.RegisterWithCancel(state.job.AgentID, state.cancel, state.job.AgentType, state.job.Worktree)
			state.registered = err == nil
			state.startErr = err
		}
		go s.run(state)
	}
}

func (s *Supervisor) removeQueuedLocked(state *jobState) {
	for i, queued := range s.queue {
		if queued == state {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			return
		}
	}
}
