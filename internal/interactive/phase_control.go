package interactive

import (
	"context"
	"errors"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
)

// PhaseControl routes user control to an active oneshot phase.
type PhaseControl interface {
	Submit(text string, images []agent.ImageBlock)
	NotifySteer()
	StopTurn()
	CancelAgent(agentID string, discard bool) error
	CancelAll() error
}

// phaseHandle identifies one SetActivePhaseControl registration, so a release
// of a superseded handle cannot clear its successor.
type phaseHandle struct {
	pc PhaseControl
}

// SetActivePhaseControl routes prompts, steers, stop-turn and delegate
// cancellation to pc until the returned release runs. The session's own driver
// receives none of them and is detached from the steer queue, which the phase's
// driver reads instead; release re-attaches it. Setting while another handle is
// active replaces it atomically, and the replaced handle's release does
// nothing. release is idempotent.
func (s *Session) SetActivePhaseControl(pc PhaseControl) (release func()) {
	h := &phaseHandle{pc: pc}
	s.mu.Lock()
	if s.phase == nil {
		s.driver.drv.DetachSteers()
	}
	s.phase = h
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.phase != h {
				return
			}
			s.phase = nil
			s.driver.drv.AttachSteers(s.runController.SteerQueue())
		})
	}
}

// activePhase returns the phase control in effect, or nil.
func (s *Session) activePhase() PhaseControl {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.phase == nil {
		return nil
	}
	return s.phase.pc
}

// routeToPhase sends the actions a phase control owns to it while one is set.
func (s *Session) routeToPhase(action Action) (bool, error) {
	pc := s.activePhase()
	if pc == nil {
		return false, nil
	}
	switch a := action.(type) {
	case SubmitPrompt:
		pc.Submit(a.Text, a.Images)
	case NotifySteer:
		pc.NotifySteer()
	case InterruptActiveRun:
		pc.StopTurn()
	case CancelDelegate:
		return true, pc.CancelAgent(a.AgentID, a.Discard)
	case CancelAllDelegates:
		return true, pc.CancelAll()
	default:
		return false, nil
	}
	return true, nil
}

// steersForNewDriverLocked is the steer queue a new driver reads: none while a
// phase owns it. The caller must hold s.mu.
func (s *Session) steersForNewDriverLocked() *agent.SteerQueue {
	if s.phase != nil {
		return nil
	}
	return s.runController.SteerQueue()
}

// PhaseRouter is a PhaseControl that follows a sequence of phases. Registering
// the router once with SetActivePhaseControl for a whole oneshot run keeps
// control routed to the run between phases: steers wait in the queue for the
// next phase instead of reaching the session's driver.
type PhaseRouter struct {
	mu      sync.Mutex
	current PhaseControl
}

var errNoActivePhase = errors.New("no active oneshot phase")

// Register makes pc the router's target and returns a release that clears it
// unless a later Register replaced it.
func (r *PhaseRouter) Register(pc PhaseControl) (release func()) {
	r.mu.Lock()
	r.current = pc
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.current == pc {
				r.current = nil
			}
		})
	}
}

func (r *PhaseRouter) target() PhaseControl {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// Submit implements PhaseControl. Without an active phase the prompt is dropped.
func (r *PhaseRouter) Submit(text string, images []agent.ImageBlock) {
	if pc := r.target(); pc != nil {
		pc.Submit(text, images)
	}
}

// NotifySteer implements PhaseControl. Without an active phase the steer stays
// queued for the next one.
func (r *PhaseRouter) NotifySteer() {
	if pc := r.target(); pc != nil {
		pc.NotifySteer()
	}
}

// StopTurn implements PhaseControl.
func (r *PhaseRouter) StopTurn() {
	if pc := r.target(); pc != nil {
		pc.StopTurn()
	}
}

// CancelAgent implements PhaseControl.
func (r *PhaseRouter) CancelAgent(agentID string, discard bool) error {
	if pc := r.target(); pc != nil {
		return pc.CancelAgent(agentID, discard)
	}
	return errNoActivePhase
}

// CancelAll implements PhaseControl.
func (r *PhaseRouter) CancelAll() error {
	if pc := r.target(); pc != nil {
		return pc.CancelAll()
	}
	return errNoActivePhase
}

// RunBackground runs fn on a goroutine the session tracks, so WaitRuns and
// quit wait for it. ctx ends when CancelBackground is called.
func (s *Session) RunBackground(fn func(ctx context.Context)) {
	s.runs.Add(1)
	go func() {
		defer s.runs.Done()
		fn(s.background)
	}()
}

// CancelBackground cancels the context of every RunBackground goroutine.
func (s *Session) CancelBackground() {
	s.cancelBackground()
}
