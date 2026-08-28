package agent

import (
	"sync"
	"sync/atomic"
)

// VisionState describes whether a model alias is known to support image input.
type VisionState int

// VisionUnknown means capability has not been derived or latched yet.
// VisionCapable means the model accepts images. VisionIncapable means the
// model rejects images, either by config/models.dev or a runtime 400 latch.
const (
	VisionUnknown VisionState = iota
	VisionCapable
	VisionIncapable
)

// visionNotificationState holds notification state shared by the session tracker
// and its active-run snapshots.
type visionNotificationState struct {
	mu      sync.Mutex
	aliases map[string]bool
}

// VisionCapabilities tracks per-model vision capability for one session.
type VisionCapabilities struct {
	mu                 sync.RWMutex
	byAlias            map[string]VisionState
	notified           *visionNotificationState
	subAgentConfigured atomic.Bool
	session            *VisionCapabilities
}

// NewVisionCapabilities creates a new vision capabilities tracker for a session.
// subAgentConfigured indicates whether a sub-agent is configured to handle vision requests.
func NewVisionCapabilities(subAgentConfigured bool) *VisionCapabilities {
	capabilities := &VisionCapabilities{
		byAlias:  make(map[string]VisionState),
		notified: &visionNotificationState{aliases: make(map[string]bool)},
	}
	capabilities.subAgentConfigured.Store(subAgentConfigured)
	return capabilities
}

// SnapshotWithSubAgentConfigured creates an independent active-run tracker
// with a frozen sub-agent configuration. Derived capability state is copied,
// runtime incapability latches are propagated back to the session tracker, and
// notification state remains shared for once-per-session diagnostics.
func (v *VisionCapabilities) SnapshotWithSubAgentConfigured(configured bool) *VisionCapabilities {
	if v == nil {
		return nil
	}

	v.mu.RLock()
	byAlias := make(map[string]VisionState, len(v.byAlias))
	for alias, state := range v.byAlias {
		byAlias[alias] = state
	}
	notified := v.notified
	v.mu.RUnlock()

	session := v
	if v.session != nil {
		session = v.session
	}
	snapshot := &VisionCapabilities{
		byAlias:  byAlias,
		notified: notified,
		session:  session,
	}
	snapshot.subAgentConfigured.Store(configured)
	return snapshot
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
	if current, exists := v.byAlias[alias]; exists && current == VisionIncapable {
		v.mu.Unlock()
		return false // Already latched
	}
	v.byAlias[alias] = VisionIncapable
	v.mu.Unlock()

	if v.session != nil {
		v.session.LatchIncapable(alias)
	}
	return true // State changed
}

// TakeNotify returns true exactly once per alias, then false on all subsequent calls.
// Used to emit "we discovered this model can't see images" diagnostics exactly once per session per alias.
func (v *VisionCapabilities) TakeNotify(alias string) bool {
	v.notified.mu.Lock()
	defer v.notified.mu.Unlock()
	if v.notified.aliases[alias] {
		return false
	}
	v.notified.aliases[alias] = true
	return true
}

// SetSubAgentConfigured updates whether a sub-agent is configured to handle vision requests.
func (v *VisionCapabilities) SetSubAgentConfigured(configured bool) {
	v.subAgentConfigured.Store(configured)
}

// SubAgentConfigured returns whether a sub-agent is configured to handle vision requests.
func (v *VisionCapabilities) SubAgentConfigured() bool {
	return v.subAgentConfigured.Load()
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
