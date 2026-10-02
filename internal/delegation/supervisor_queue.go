package delegation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
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

	if s.closing {
		s.mu.Unlock()
		return nil, ErrSupervisorClosed
	}
	if existing, ok := s.jobs[job.AgentID]; ok && !existing.acked {
		s.mu.Unlock()
		return nil, ErrAgentAlreadyActive
	}
	if outstanding := s.running + len(s.queue); outstanding >= 2*s.maxParallel {
		s.mu.Unlock()
		return nil, outstandingCapError{outstanding: outstanding}
	}
	batchID := agent.ToolBatchIDFrom(handlerCtx)
	groupName := NormalizeGroup(job.Group)
	job.Group = groupName
	if groupName != "" {
		if err := s.reserveGroupLocked(job.GroupScope, groupName, batchID); err != nil {
			s.mu.Unlock()
			return nil, err
		}
	}

	childCtx, cancel := context.WithCancel(context.WithoutCancel(handlerCtx))
	s.enqSeq++
	state := &jobState{
		job:         job,
		batchID:     batchID,
		childCtx:    childCtx,
		cancel:      cancel,
		phase:       phaseQueued,
		blocking:    blocking,
		order:       s.enqSeq,
		worktree:    job.Worktree,
		done:        make(chan struct{}),
		exited:      make(chan struct{}),
		publication: make(chan struct{}),
		settled:     make(chan struct{}),
	}
	if !blocking {
		s.enrollLocked(state, batchID)
	}
	s.jobs[job.AgentID] = state
	s.queue = append(s.queue, state)
	state.wasQueued = s.running >= s.maxParallel || len(s.queue) > 1
	s.mu.Unlock()

	if s.events != nil {
		s.events.Emit(output.NewDelegationAcceptedEvent(job.ParentCallID, job.AgentID, batchID, groupName))
	}

	s.mu.Lock()
	state.published = true
	close(state.publication)
	var latePosts postList
	if state.shutdownTimedOut {
		latePosts = s.settleLatePublicationLocked(state)
	}
	if state.cause != CancelCauseNone && state.phase == phaseQueued {
		s.removeQueuedLocked(state)
		state.phase = phaseDone
		state.cancel()
		go s.finishCancelled(state)
	}
	s.startQueuedLocked()
	s.mu.Unlock()
	latePosts.deliver()
	if state.wasQueued && s.events != nil {
		s.events.Emit(output.NewDelegationQueuedEvent(job.AgentID, job.ParentCallID, string(job.AgentType), job.ObjectivePreview))
	}
	return state, nil
}

func (s *Supervisor) newShutdownCompletionLocked(state *jobState) *agent.SubAgentCompletion {
	completion := s.newCompletionLocked(state)
	completion.Status = string(StatusCancelled)
	completion.Quiet = true
	completion.Body = agent.FailureBody(completion.Status, "sub-agent did not stop before shutdown")
	return completion
}

func (s *Supervisor) settleLatePublicationLocked(state *jobState) postList {
	if state.completion == nil && !state.blocking {
		state.completion = s.newShutdownCompletionLocked(state)
	}
	deliverLocked(state, tool.ExecutionResult{}, ErrSupervisorClosed)
	posts := s.routeShutdownCompletionLocked(state)
	if state.phase == phaseQueued {
		s.removeQueuedLocked(state)
		state.phase = phaseDone
		state.cancel()
		go s.finishCancelled(state)
	}
	return posts
}

// startQueuedLocked starts queued jobs in FIFO order while slots are free.
func (s *Supervisor) startQueuedLocked() {
	for !s.closing && s.running < s.maxParallel && len(s.queue) > 0 {
		state := s.queue[0]
		if !state.published {
			return
		}
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
