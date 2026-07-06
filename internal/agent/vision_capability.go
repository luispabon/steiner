package agent

import "sync"

type VisionState int

const (
	VisionUnknown VisionState = iota
	VisionCapable
	VisionIncapable
)

// VisionCapabilities tracks per-model vision capability for one session.
type VisionCapabilities struct {
	mu                 sync.RWMutex
	byAlias            map[string]VisionState
	notified           map[string]bool
	subAgentConfigured bool
}

// NewVisionCapabilities creates a new vision capabilities tracker for a session.
// subAgentConfigured indicates whether a sub-agent is configured to handle vision requests.
func NewVisionCapabilities(subAgentConfigured bool) *VisionCapabilities {
	return &VisionCapabilities{
		byAlias:            make(map[string]VisionState),
		notified:           make(map[string]bool),
		subAgentConfigured: subAgentConfigured,
	}
}

// SetDerived sets the vision state for an alias based on model resolution (config or models.dev).
// Does not override a prior VisionIncapable latch (runtime capability discovery takes precedence).
// If not already latched to VisionIncapable, updates the state (including overwriting Unknown/Capable).
func (v *VisionCapabilities) SetDerived(alias string, s VisionState) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if current, exists := v.byAlias[alias]; exists && current == VisionIncapable {
		// Latch prevents overwriting
		return
	}
	v.byAlias[alias] = s
}

// Get returns the vision state for an alias, defaulting to VisionUnknown if not yet set.
func (v *VisionCapabilities) Get(alias string) VisionState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if state, exists := v.byAlias[alias]; exists {
		return state
	}
	return VisionUnknown
}

// LatchIncapable sets the vision state to VisionIncapable for an alias.
// Returns true if this is a change (was not already VisionIncapable), false otherwise.
func (v *VisionCapabilities) LatchIncapable(alias string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if current, exists := v.byAlias[alias]; exists && current == VisionIncapable {
		return false // Already latched
	}
	v.byAlias[alias] = VisionIncapable
	return true // State changed
}

// TakeNotify returns true exactly once per alias, then false on all subsequent calls.
// Used to emit "we discovered this model can't see images" diagnostics exactly once per session per alias.
func (v *VisionCapabilities) TakeNotify(alias string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.notified[alias] {
		return false
	}
	v.notified[alias] = true
	return true
}

// SubAgentConfigured returns whether a sub-agent is configured to handle vision requests.
func (v *VisionCapabilities) SubAgentConfigured() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.subAgentConfigured
}

// VisionStateFromPtr is a helper that converts a *bool to VisionState.
// nil → VisionUnknown, *false → VisionIncapable, *true → VisionCapable.
func VisionStateFromPtr(p *bool) VisionState {
	if p == nil {
		return VisionUnknown
	}
	if *p {
		return VisionCapable
	}
	return VisionIncapable
}
