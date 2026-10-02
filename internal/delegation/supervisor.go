package delegation

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
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
//
// The completion record posted for an async job is derived from the final
// tool.ExecutionResult: a delegation Result supplies status and usage, and its
// provider projection supplies the body. A returned error becomes a failed (or,
// after a recorded cancel, cancelled) completion.
type ChildJob struct {
	AgentID      string
	AgentType    AgentType
	ParentCallID string
	// BatchID is the tool batch that issued the call; with ParentCallID it
	// identifies the delegation occurrence. Enqueue sets it from the handler context.
	BatchID string
	// Group is the optional sub_agent label used for turn-scoped group release.
	Group string
	// GroupScope identifies the runtime conversation that owns group names.
	GroupScope string
	// Events is the sink for this job's Accepted and Queued events; it is the
	// same sink the handler uses for Started, Complete and Failed. A nil sink
	// suppresses both.
	Events output.EventSink
	// ObjectivePreview is the short task preview carried on the completion.
	ObjectivePreview string
	Worktree         CodeWorktree
	// Prepare, when set, runs on the child goroutine at dequeue time, before the
	// job is registered as active. The worktree it returns replaces Worktree. A
	// failure yields the job's final result without calling Execute.
	Prepare                func(childCtx context.Context) (CodeWorktree, error)
	Execute                func(childCtx context.Context) (tool.ExecutionResult, error)
	OnCancelledBeforeStart func() tool.ExecutionResult
}

type jobPhase uint8

// Phases advance monotonically, except that Queued may jump to Cancelling.
// Accepting precedes publication of the Accepted event, which happens outside
// the lock: a job in that window is in the queue but cannot start.
const (
	phaseAccepting jobPhase = iota
	phaseQueued
	phaseRunning
	// phaseCancelling is a job cancelled before start whose finalizer has not
	// completed yet.
	phaseCancelling
	phaseDone
)

// finishing reports whether the job will never run again.
func (p jobPhase) finishing() bool { return p >= phaseCancelling }

// completionRoute tracks where a job's completion is on its way to the sink.
type completionRoute uint8

const (
	routeNone completionRoute = iota
	// routeHeld means a group is holding the completion until it releases.
	routeHeld
	routePosted
)

// jobState is guarded by Supervisor.mu. result and err are written once, before
// done is closed, and read only after <-done.
type jobState struct {
	job      ChildJob
	childCtx context.Context
	cancel   context.CancelFunc
	phase    jobPhase
	cause    CancelCause
	// delivered means the waiter's result was published; acked means the parent
	// consumed the completion, which ends pending tracking.
	delivered   bool
	acked       bool
	blocking    bool
	registered  bool
	publication chan struct{}
	settled     chan struct{}
	wasQueued   bool
	startErr    error
	order       uint64
	worktree    CodeWorktree
	startedAt   time.Time
	group       *jobGroup
	completion  *agent.SubAgentCompletion
	route       completionRoute
	done        chan struct{}
	exited      chan struct{}
	result      tool.ExecutionResult
	err         error
}

// Supervisor owns detached sub-agent lifecycles for one runtime.
type Supervisor struct {
	mu          sync.Mutex
	maxParallel int
	controller  *ActiveController
	joinTimeout time.Duration
	sink        agent.CompletionSink

	closing  bool
	closed   bool
	shutdown sync.Once
	report   ShutdownReport

	running   int
	queue     []*jobState
	jobs      map[string]*jobState
	protected map[string]struct{}

	seq      uint64
	enqSeq   uint64
	groupSeq uint64
	groups   map[groupKey]*jobGroup
	scopeSeq uint64
	scopes   map[string]*delegationGroupScope
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
		groups:      make(map[groupKey]*jobGroup),
		scopes:      make(map[string]*delegationGroupScope),
	}
}

// SpawnAndWait enqueues a job and waits for its result. If handlerCtx is
// cancelled it cancels the child with CancelCauseUser and keeps waiting for the
// final result, preserving blocking-cancel semantics. The caller consumes the
// result directly, so nothing is posted to the completion sink and the job is
// no longer pending on return.
func (s *Supervisor) SpawnAndWait(handlerCtx context.Context, job ChildJob) (tool.ExecutionResult, error) {
	state, err := s.enqueue(handlerCtx, job, true)
	if err != nil {
		return tool.ExecutionResult{}, tool.WithDelegationAdmission(err, admissionFor(handlerCtx, job, tool.DelegationAdmissionRejected))
	}

	cancelled := handlerCtx.Done()
	for {
		select {
		case <-state.done:
			s.mu.Lock()
			state.acked = true
			s.pruneLocked(state)
			result := state.result
			result.DelegationAdmission = admissionForState(state)
			err := tool.WithDelegationAdmission(state.err, result.DelegationAdmission)
			s.mu.Unlock()
			return result, err
		case <-cancelled:
			cancelled = nil
			s.CancelAgent(job.AgentID, false, CancelCauseUser)
		}
	}
}

func admissionFor(ctx context.Context, job ChildJob, status string) *tool.DelegationAdmission {
	return &tool.DelegationAdmission{Status: status, BatchID: agent.ToolBatchIDFrom(ctx), Group: agent.NormalizeDelegationGroup(job.Group), AgentID: job.AgentID}
}

func admissionForState(state *jobState) *tool.DelegationAdmission {
	return &tool.DelegationAdmission{Status: tool.DelegationAdmissionAccepted, BatchID: state.job.BatchID, Group: state.job.Group, AgentID: state.job.AgentID}
}

// run is the single goroutine for a started job.
func (s *Supervisor) run(state *jobState) {
	defer close(state.exited)

	result, err := s.execute(state)

	s.mu.Lock()
	s.controller.MarkComplete(state.job.AgentID)
	s.controller.Unregister(state.job.AgentID)
	state.cancel()
	state.phase = phaseDone
	s.running--
	var posts postList
	if s.closed {
		// Shutdown settled this job while it was still running.
		result, err = tool.ExecutionResult{}, ErrSupervisorClosed
		s.completeLocked(state, result, nil)
		posts = s.routeShutdownCompletionLocked(state)
	} else {
		s.completeLocked(state, result, err)
		posts = s.routeLocked(state)
	}
	result.DelegationAdmission = admissionForState(state)
	deliverLocked(state, result, err)
	s.pruneLocked(state)
	s.startQueuedLocked()
	s.mu.Unlock()

	posts.deliver()
	close(state.settled)
}

// execute provisions the job's worktree at dequeue time when it has a Prepare
// hook, registers it as active, then runs it.
func (s *Supervisor) execute(state *jobState) (tool.ExecutionResult, error) {
	if state.startErr != nil {
		return tool.ExecutionResult{}, state.startErr
	}
	if state.job.Prepare != nil {
		worktree, err := state.job.Prepare(state.childCtx)
		if err != nil {
			return tool.ExecutionResult{}, err
		}
		s.mu.Lock()
		state.worktree = worktree
		err = s.controller.RegisterWithCancel(state.job.AgentID, state.cancel, state.job.AgentType, worktree)
		state.registered = err == nil
		s.mu.Unlock()
		if err != nil {
			return tool.ExecutionResult{}, err
		}
	}
	return state.job.Execute(state.childCtx)
}

// finishCancelled completes a job that was cancelled while queued. The caller
// must already have removed it from the queue and moved it to phaseCancelling.
func (s *Supervisor) finishCancelled(state *jobState) {
	var result tool.ExecutionResult
	if state.job.OnCancelledBeforeStart != nil {
		result = state.job.OnCancelledBeforeStart()
	}

	s.mu.Lock()
	s.controller.MarkComplete(state.job.AgentID)
	s.controller.Unregister(state.job.AgentID)
	var posts postList
	s.completeLocked(state, result, nil)
	if s.closed {
		posts = s.routeShutdownCompletionLocked(state)
	} else {
		posts = s.routeLocked(state)
	}
	state.phase = phaseDone
	deliverLocked(state, result, nil)
	s.pruneLocked(state)
	s.mu.Unlock()

	posts.deliver()
	close(state.settled)
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

// pruneLocked drops a job from the table once it is both acknowledged and
// finished. The identity check keeps a stale state from removing a newer job
// that reused the agent ID.
func (s *Supervisor) pruneLocked(state *jobState) {
	if state.acked && state.phase == phaseDone && s.jobs[state.job.AgentID] == state {
		delete(s.jobs, state.job.AgentID)
		s.maybeDeleteScopeLocked(state.job.GroupScope)
	}
}

// CancelAgent cancels a child by ID, recording the cause before cancelling.
func (s *Supervisor) CancelAgent(agentID string, discard bool, cause CancelCause) CancelOutcome {
	s.mu.Lock()
	state, ok := s.jobs[agentID]
	if !ok {
		s.mu.Unlock()
		return CancelNotActive
	}
	if state.phase.finishing() {
		s.mu.Unlock()
		return CancelAlreadyFinished
	}
	if state.cause == CancelCauseNone {
		state.cause = cause
	}

	if state.phase == phaseAccepting {
		// enqueue finishes the cancellation once Accepted is published.
		state.cancel()
		s.mu.Unlock()
		return CancelAccepted
	}
	if state.phase == phaseQueued {
		s.removeQueuedLocked(state)
		state.phase = phaseCancelling
		state.cancel()
		s.mu.Unlock()
		go s.finishCancelled(state)
		return CancelAccepted
	}

	if !state.registered {
		state.cancel()
		s.mu.Unlock()
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
		if state.phase.finishing() {
			continue
		}
		if state.cause == CancelCauseNone {
			state.cause = cause
		}
		if state.phase == phaseQueued {
			state.phase = phaseCancelling
			cancelled = append(cancelled, state)
		}
		state.cancel()
	}
	kept := s.queue[:0]
	for _, state := range s.queue {
		if state.phase == phaseAccepting {
			kept = append(kept, state)
		}
	}
	s.queue = kept
	s.mu.Unlock()

	for _, state := range cancelled {
		go s.finishCancelled(state)
	}
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
