package delegation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/tool"
)

// ErrSupervisorClosed indicates that the supervisor has been shut down.
var ErrSupervisorClosed = errors.New("supervisor is closed")

// ErrOutstandingCap indicates that too many sub-agents are already outstanding.
var ErrOutstandingCap = errors.New("sub-agent outstanding cap exceeded")

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

// Supervisor owns detached sub-agent lifecycles for one runtime.
type Supervisor struct {
	mu             sync.Mutex
	maxParallel    int
	controller     *ActiveController
	joinTimeout    time.Duration
	closing        bool
	shuttingDown   bool
	shutdownOnce   sync.Once
	shutdownReport *ShutdownReport
	shutdownCause  CancelCause

	running    int
	idleClosed bool
	queue      []*queuedJob
	jobs       map[string]*jobState
	idle       chan struct{}
	causes     map[string]CancelCause
	protected  map[string]struct{}
}

type jobState struct {
	queued      bool
	running     bool
	finished    bool
	delivered   bool
	doneClosed  bool
	done        chan struct{}
	result      tool.ExecutionResult
	err         error
}

type queuedJob struct {
	job        ChildJob
	startFn    func()
	handlerCtx context.Context
}

// NewSupervisor returns an initialized Supervisor.
func NewSupervisor(opts SupervisorOptions) *Supervisor {
	timeout := opts.JoinTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &Supervisor{
		maxParallel: opts.MaxParallel,
		controller:  opts.Controller,
		joinTimeout: timeout,
		jobs:        make(map[string]*jobState),
		causes:      make(map[string]CancelCause),
		protected:   make(map[string]struct{}),
		idle:        make(chan struct{}),
	}
}

// SpawnAndWait enqueues a job and waits for its result.
// If handlerCtx is cancelled, it calls CancelAgent(id, false, CancelCauseUser)
// and keeps waiting for the final result, which preserves blocking cancel semantics.
func (s *Supervisor) SpawnAndWait(handlerCtx context.Context, job ChildJob) (tool.ExecutionResult, error) {
	state, err := s.enqueue(handlerCtx, job)
	if err != nil {
		return tool.ExecutionResult{}, err
	}
	return s.wait(handlerCtx, state, job.AgentID)
}

// enqueue checks caps, registers the job, and either starts it or queues it.
func (s *Supervisor) enqueue(handlerCtx context.Context, job ChildJob) (*jobState, error) {
	startFn := func() { s.startJob(handlerCtx, job) }

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closing {
		return nil, ErrSupervisorClosed
	}

	if _, exists := s.jobs[job.AgentID]; exists {
		return nil, ErrAgentAlreadyActive
	}

	if s.running+len(s.queue) >= 2*s.maxParallel {
		return nil, fmt.Errorf("%d sub-agents already outstanding; wait for results before dispatching more: %w", s.running+len(s.queue), ErrOutstandingCap)
	}

	state := &jobState{
		done: make(chan struct{}),
	}
	s.jobs[job.AgentID] = state

	if s.running < s.maxParallel {
		s.running++
		state.running = true
		go startFn()
	} else {
		state.queued = true
		s.queue = append(s.queue, &queuedJob{
			job:        job,
			startFn:    startFn,
			handlerCtx: handlerCtx,
		})
	}

	return state, nil
}

// startJob creates a child context and runs Execute.
func (s *Supervisor) startJob(handlerCtx context.Context, job ChildJob) {
	childCtx, cancel := context.WithCancel(context.WithoutCancel(handlerCtx))
	if err := s.controller.RegisterWithCancel(job.AgentID, cancel, job.AgentType, job.Worktree); err != nil {
		s.mu.Lock()
		state := s.jobs[job.AgentID]
		state.err = err
		state.finished = true
		close(state.done)
		s.running--
		s.dequeueAndStart()
		s.mu.Unlock()
		return
	}

	result, err := job.Execute(childCtx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.markFinished(job.AgentID, result, err) {
		return
	}

	state := s.jobs[job.AgentID]
	if !state.doneClosed {
		state.doneClosed = true
		s.jobs[job.AgentID] = state
		close(state.done)
	}

	s.controller.MarkComplete(job.AgentID)
	s.controller.Unregister(job.AgentID)
	cancel()
	s.running--
	s.dequeueAndStart()
}

// markFinished updates job state under the lock.
// Returns false if the job was already marked as unjoined by Shutdown.
func (s *Supervisor) markFinished(agentID string, result tool.ExecutionResult, err error) bool {
	state, exists := s.jobs[agentID]
	if !exists {
		return false
	}

	if state.finished {
		return false
	}

	state.finished = true
	state.result = result
	state.err = err
	s.jobs[agentID] = state

	return true
}

// dequeueAndStart removes one job from the queue and starts it, if any remain and we're not closing.
// Must be called with s.mu held.
func (s *Supervisor) dequeueAndStart() {
	if s.closing || len(s.queue) == 0 {
		if s.running == 0 && !s.idleClosed {
			s.idleClosed = true
			close(s.idle)
		}
		return
	}

	queued := s.queue[0]
	s.queue = s.queue[1:]

	state := s.jobs[queued.job.AgentID]
	state.queued = false
	state.running = true

	s.running++
	go queued.startFn()
}

// wait blocks for the job result, forwarding handlerCtx cancellation.
func (s *Supervisor) wait(handlerCtx context.Context, state *jobState, agentID string) (tool.ExecutionResult, error) {
	for {
		select {
		case <-state.done:
			s.mu.Lock()
			result := state.result
			err := state.err
			s.mu.Unlock()
			return result, err

		case <-handlerCtx.Done():
			s.CancelAgent(agentID, false, CancelCauseUser)
		}
	}
}

// CancelAgent cancels a child by ID, recording the cause.
// Returns the outcome: NotActive, Accepted, or AlreadyFinished.
func (s *Supervisor) CancelAgent(agentID string, discard bool, cause CancelCause) CancelOutcome {
	s.mu.Lock()

	state, exists := s.jobs[agentID]
	if !exists {
		s.mu.Unlock()
		return CancelNotActive
	}

	if state.finished {
		s.mu.Unlock()
		return CancelAlreadyFinished
	}

	s.causes[agentID] = cause

	var result tool.ExecutionResult
	var deliverResult bool
	var queued *ChildJob

	if state.queued {
		for i, q := range s.queue {
			if q.job.AgentID == agentID {
				queued = &q.job
				copy(s.queue[i:], s.queue[i+1:])
				s.queue = s.queue[:len(s.queue)-1]
				break
			}
		}
		if queued != nil {
			if queued.OnCancelledBeforeStart != nil {
				result = queued.OnCancelledBeforeStart()
			}
			deliverResult = true
			state.queued = false
		}
	}

	if state.running {
		s.controller.CancelAgentWithDiscard(agentID, discard)
	}

	s.mu.Unlock()

	if deliverResult {
		state.result = result
		if !state.doneClosed {
			close(state.done)
		}
	}

	return CancelAccepted
}

// CancelAll cancels every child, recording the cause.
func (s *Supervisor) CancelAll(cause CancelCause) {
	s.mu.Lock()

	for agentID := range s.jobs {
		s.causes[agentID] = cause
	}

	queuedJobs := make([]*queuedJob, len(s.queue))
	copy(queuedJobs, s.queue)
	s.queue = s.queue[:0]

	s.controller.CancelAll()

	s.mu.Unlock()

	for _, queued := range queuedJobs {
		result := tool.ExecutionResult{}
		if queued.job.OnCancelledBeforeStart != nil {
			result = queued.job.OnCancelledBeforeStart()
		}
		s.mu.Lock()
		if state, exists := s.jobs[queued.job.AgentID]; exists && !state.finished {
			state.queued = false
			state.result = result
			if !state.doneClosed {
				state.doneClosed = true
				s.jobs[queued.job.AgentID] = state
				close(state.done)
			}
		}
		s.mu.Unlock()
	}
}

// CauseFor returns the cancellation cause for a child.
func (s *Supervisor) CauseFor(agentID string) CancelCause {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.causes[agentID]
}

// Closed reports whether the supervisor is closed.
func (s *Supervisor) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}

// ProtectedWorktrees returns the list of worktree paths that should not be pruned.
func (s *Supervisor) ProtectedWorktrees() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]string, 0, len(s.protected))
	for path := range s.protected {
		if path != "" {
			result = append(result, path)
		}
	}
	return result
}
