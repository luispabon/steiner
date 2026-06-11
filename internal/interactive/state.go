package interactive

import (
	"context"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// ActiveRunController manages the cancellation of the currently active model
// run and steer messaging during an interactive session.
type ActiveRunController struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	steerCh chan string
}

// NewActiveRunController creates a new ActiveRunController with an initialized
// steering channel.
func NewActiveRunController() *ActiveRunController {
	return &ActiveRunController{
		steerCh: make(chan string, 1),
	}
}

// Set records a new cancel function, replacing any existing one.
func (c *ActiveRunController) Set(cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancel = cancel
}

// Clear releases the current cancel function without calling it, and drains
// any pending steer message.
func (c *ActiveRunController) Clear() {
	c.mu.Lock()
	c.cancel = nil
	c.mu.Unlock()
	// Drain any pending steer so it doesn't leak to the next run.
	select {
	case <-c.steerCh:
	default:
	}
}

// Interrupt calls the current cancel function, if any.
func (c *ActiveRunController) Interrupt() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// HasCancel reports whether a cancel function is currently set.
func (c *ActiveRunController) HasCancel() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancel != nil
}

// Steer queues a steering message with latest-wins semantics: if the channel
// is full, drain the old message and send the new one.
func (c *ActiveRunController) Steer(text string) {
	select {
	case c.steerCh <- text:
	default:
		// Channel full: drain old, send new (latest-wins)
		select {
		case <-c.steerCh:
		default:
		}
		select {
		case c.steerCh <- text:
		default:
		}
	}
}

// Drain returns a pending steer message if one is available, or an empty
// string.
func (c *ActiveRunController) Drain() string {
	select {
	case text := <-c.steerCh:
		return text
	default:
		return ""
	}
}

// SteerCh returns the receive-only steering channel for the runner to consume.
func (c *ActiveRunController) SteerCh() <-chan string {
	return c.steerCh
}

// Skills tracks which skills are enabled during an interactive session and
// preserves their ordering.
type Skills struct {
	mu      sync.RWMutex
	enabled map[string]bool
	order   []string
}

// NewSkills creates a Skills tracker with the given skill names, all initially
// disabled in the order provided.
func NewSkills(skillNames []string) *Skills {
	enabled := make(map[string]bool, len(skillNames))
	for _, name := range skillNames {
		enabled[name] = false
	}
	return &Skills{
		enabled: enabled,
		order:   append([]string(nil), skillNames...),
	}
}

// Set enables or disables a skill by name.
func (s *Skills) Set(name string, enabled bool) {
	if s == nil || strings.TrimSpace(name) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enabled == nil {
		s.enabled = make(map[string]bool)
	}
	s.enabled[name] = enabled
}

// Reset disables all tracked skills.
func (s *Skills) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name := range s.enabled {
		s.enabled[name] = false
	}
}

// Active returns the first enabled skill in order, or an empty string when no
// skills are enabled.
func (s *Skills) Active() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, name := range s.order {
		if s.enabled[name] {
			return name
		}
	}
	return ""
}

// Snapshot returns the names of all currently enabled skills in order.
func (s *Skills) Snapshot() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.order))
	for _, name := range s.order {
		if s.enabled[name] {
			names = append(names, name)
		}
	}
	return names
}

// SnapshotStore provides thread-safe storage for the most recent request
// context snapshot during an interactive session.
type SnapshotStore struct {
	mu       sync.RWMutex
	snapshot *RequestContextSnapshot
}

// Store saves a deep copy of the given snapshot.
func (s *SnapshotStore) Store(snapshot RequestContextSnapshot) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := RequestContextSnapshot{
		Model:       snapshot.Model,
		Messages:    provider.CloneMessages(snapshot.Messages),
		Tools:       provider.CloneTools(snapshot.Tools),
		MaxTokens:   cloneOptionalInt(snapshot.MaxTokens),
		Blocks:      append([]prompt.ContextBlock(nil), snapshot.Blocks...),
		ModelBudget: snapshot.ModelBudget,
	}
	s.snapshot = &cloned
}

// Snapshot returns the stored snapshot if one exists, with a boolean
// indicating whether data was available.
func (s *SnapshotStore) Snapshot() (RequestContextSnapshot, bool) {
	if s == nil {
		return RequestContextSnapshot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snapshot == nil {
		return RequestContextSnapshot{}, false
	}
	cloned := RequestContextSnapshot{
		Model:       s.snapshot.Model,
		Messages:    provider.CloneMessages(s.snapshot.Messages),
		Tools:       provider.CloneTools(s.snapshot.Tools),
		MaxTokens:   cloneOptionalInt(s.snapshot.MaxTokens),
		Blocks:      append([]prompt.ContextBlock(nil), s.snapshot.Blocks...),
		ModelBudget: s.snapshot.ModelBudget,
	}
	return cloned, true
}

// pendingApproval represents an outstanding mutation-tool approval that the
// TUI (or other client) has not yet responded to.
type pendingApproval struct {
	toolName string
	mode     string
	response chan SubmitApproval
}

// ApprovalCoordinator manages the lifecycle of pending approval requests
// during an interactive session.
type ApprovalCoordinator struct {
	mu      sync.Mutex
	pending *pendingApproval
}

// Begin registers a new pending approval request and returns a channel that
// will receive the decision once the client submits one.
func (c *ApprovalCoordinator) Begin(toolName, mode string) chan SubmitApproval {
	response := make(chan SubmitApproval, 1)
	c.mu.Lock()
	c.pending = &pendingApproval{
		toolName: toolName,
		mode:     mode,
		response: response,
	}
	c.mu.Unlock()
	return response
}

// Finish unregisters the pending approval associated with the given response
// channel.
func (c *ApprovalCoordinator) Finish(response chan SubmitApproval) {
	c.mu.Lock()
	if c.pending != nil && c.pending.response == response {
		c.pending = nil
	}
	c.mu.Unlock()
}

// Submit delivers a user's approval decision to the pending request, if the
// tool and mode match.
func (c *ApprovalCoordinator) Submit(submission SubmitApproval) {
	c.mu.Lock()
	pending := c.pending
	c.mu.Unlock()
	if pending == nil {
		return
	}
	if pending.toolName != "" && submission.Tool != "" && submission.Tool != pending.toolName {
		return
	}
	if pending.mode != "" && submission.Mode != "" && submission.Mode != pending.mode {
		return
	}
	select {
	case pending.response <- submission:
	default:
	}
}

// HasPending reports whether an approval request is currently awaiting a
// decision.
func (c *ApprovalCoordinator) HasPending() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending != nil
}

type pendingWorkflowHandoff struct {
	request  tool.WorkflowHandoffRequest
	response chan SubmitWorkflowHandoff
}

// WorkflowHandoffCoordinator manages pending workflow handoff requests during
// an interactive session.
type WorkflowHandoffCoordinator struct {
	mu      sync.Mutex
	pending *pendingWorkflowHandoff
}

// Begin registers a pending workflow handoff request and returns a channel
// that receives the eventual user decision.
func (c *WorkflowHandoffCoordinator) Begin(req tool.WorkflowHandoffRequest) chan SubmitWorkflowHandoff {
	response := make(chan SubmitWorkflowHandoff, 1)
	c.mu.Lock()
	c.pending = &pendingWorkflowHandoff{
		request:  req,
		response: response,
	}
	c.mu.Unlock()
	return response
}

// Finish unregisters the pending workflow handoff associated with the given
// response channel.
func (c *WorkflowHandoffCoordinator) Finish(response chan SubmitWorkflowHandoff) {
	c.mu.Lock()
	if c.pending != nil && c.pending.response == response {
		c.pending = nil
	}
	c.mu.Unlock()
}

// Submit delivers a workflow handoff decision to the pending request, if one
// exists.
func (c *WorkflowHandoffCoordinator) Submit(submission SubmitWorkflowHandoff) {
	c.mu.Lock()
	pending := c.pending
	c.mu.Unlock()
	if pending == nil {
		return
	}
	select {
	case pending.response <- submission:
	default:
	}
}

// HasPending reports whether a workflow handoff request is currently awaiting
// a decision.
func (c *WorkflowHandoffCoordinator) HasPending() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending != nil
}

func cloneOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
