package delegation

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/tool"
)

// ErrSupervisorClosed indicates that the supervisor has been shut down.
var ErrSupervisorClosed = errors.New("supervisor is closed")

// SupervisorOptions configures a new Supervisor.
type SupervisorOptions struct {
	MaxParallel int // >=1, already validated
	Controller  *ActiveController
	JoinTimeout time.Duration // 10s when zero
}

// ChildJob is one child to run. Execute performs ALL child work and finalisation
// (session save, worktree handling, trace, Delegation* events) and returns the final
// tool result. The supervisor never finalises on a job's behalf.
type ChildJob struct {
	AgentID                string
	AgentType              AgentType
	ParentCallID           string
	Worktree               CodeWorktree
	Execute                func(childCtx context.Context) (tool.ExecutionResult, error)
	OnCancelledBeforeStart func() tool.ExecutionResult
}

type jobPhase uint8

const (
	phaseQueued jobPhase = iota
	phaseRunning
	phaseDone
)

// jobState is guarded by Supervisor.mu. result and err are written once, before
// done is closed, and read only after <-done.
type jobState struct {
	job       ChildJob
	childCtx  context.Context
	cancel    context.CancelFunc
	phase     jobPhase
	cause     CancelCause
	delivered bool
	done      chan struct{}
	exited    chan struct{}
	result    tool.ExecutionResult
	err       error
}

// Supervisor owns detached sub-agent lifecycles for one runtime.
type Supervisor struct {
	mu          sync.Mutex
	maxParallel int
	controller  *ActiveController
	joinTimeout time.Duration

	closing  bool
	closed   bool
	shutdown sync.Once
	report   ShutdownReport

	running   int
	queue     []*jobState
	jobs      map[string]*jobState
	protected map[string]struct{}
}

// NewSupervisor returns an initialized Supervisor.
func NewSupervisor(opts SupervisorOptions) *Supervisor {
	timeout := opts.JoinTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	controller := opts.Controller
	if controller == nil {
		controller = NewActiveController()
	}
	return &Supervisor{
		maxParallel: opts.MaxParallel,
		controller:  controller,
		joinTimeout: timeout,
		jobs:        make(map[string]*jobState),
		protected:   make(map[string]struct{}),
	}
}

// SpawnAndWait enqueues a job and waits for its result. If handlerCtx is
// cancelled it cancels the child with CancelCauseUser and keeps waiting for the
// final result, preserving blocking-cancel semantics.
func (s *Supervisor) SpawnAndWait(handlerCtx context.Context, job ChildJob) (tool.ExecutionResult, error) {
	state, err := s.enqueue(handlerCtx, job)
	if err != nil {
		return tool.ExecutionResult{}, err
	}

	cancelled := handlerCtx.Done()
	for {
		select {
		case <-state.done:
			return state.result, state.err
		case <-cancelled:
			cancelled = nil
			s.CancelAgent(job.AgentID, false, CancelCauseUser)
		}
	}
}

// run is the single goroutine for a started job.
func (s *Supervisor) run(state *jobState) {
	defer close(state.exited)

	result, err := state.job.Execute(state.childCtx)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.controller.MarkComplete(state.job.AgentID)
	s.controller.Unregister(state.job.AgentID)
	state.cancel()
	state.phase = phaseDone
	s.running--
	if s.closed {
		result, err = tool.ExecutionResult{}, ErrSupervisorClosed
	}
	deliverLocked(state, result, err)
	s.startQueuedLocked()
}

// finishCancelled completes a job that was cancelled while queued. The caller
// must already have removed it from the queue and marked it done.
func (s *Supervisor) finishCancelled(state *jobState) {
	var result tool.ExecutionResult
	if state.job.OnCancelledBeforeStart != nil {
		result = state.job.OnCancelledBeforeStart()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.controller.MarkComplete(state.job.AgentID)
	s.controller.Unregister(state.job.AgentID)
	deliverLocked(state, result, nil)
}

func deliverLocked(state *jobState, result tool.ExecutionResult, err error) {
	if state.delivered {
		return
	}
	state.delivered = true
	state.result = result
	state.err = err
	close(state.done)
}

// CancelAgent cancels a child by ID, recording the cause before cancelling.
func (s *Supervisor) CancelAgent(agentID string, discard bool, cause CancelCause) CancelOutcome {
	s.mu.Lock()
	state, ok := s.jobs[agentID]
	if !ok {
		s.mu.Unlock()
		return CancelNotActive
	}
	if state.phase == phaseDone {
		s.mu.Unlock()
		return CancelAlreadyFinished
	}
	if state.cause == CancelCauseNone {
		state.cause = cause
	}

	if state.phase == phaseQueued {
		s.removeQueuedLocked(state)
		state.phase = phaseDone
		state.cancel()
		s.mu.Unlock()
		s.finishCancelled(state)
		return CancelAccepted
	}

	outcome := s.controller.CancelAgentWithDiscard(agentID, discard)
	s.mu.Unlock()
	return outcome
}

// CancelAll cancels every running and queued child, recording cause first.
func (s *Supervisor) CancelAll(cause CancelCause) {
	s.mu.Lock()
	var cancelled []*jobState
	for _, state := range s.jobs {
		if state.phase == phaseDone {
			continue
		}
		if state.cause == CancelCauseNone {
			state.cause = cause
		}
		if state.phase == phaseQueued {
			state.phase = phaseDone
			cancelled = append(cancelled, state)
		}
		state.cancel()
	}
	s.queue = nil
	s.mu.Unlock()

	for _, state := range cancelled {
		s.finishCancelled(state)
	}
}

// CauseFor returns the recorded cancellation cause for a child.
func (s *Supervisor) CauseFor(agentID string) CancelCause {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state, ok := s.jobs[agentID]; ok {
		return state.cause
	}
	return CancelCauseNone
}

// Closed reports whether Shutdown has completed.
func (s *Supervisor) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// ProtectedWorktrees returns worktree paths of unjoined children, which must not be pruned.
func (s *Supervisor) ProtectedWorktrees() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	paths := make([]string, 0, len(s.protected))
	for path := range s.protected {
		paths = append(paths, path)
	}
	return paths
}
