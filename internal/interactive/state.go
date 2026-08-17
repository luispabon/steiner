package interactive

import (
	"context"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// ActiveRunController manages the cancellation of the currently active model
// run and steer messaging during an interactive session.
type ActiveRunController struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	steers []agent.SteerMessage
}

// NewActiveRunController creates a new ActiveRunController.
func NewActiveRunController() *ActiveRunController {
	return &ActiveRunController{}
}

// Set records a new cancel function, replacing any existing one.
func (c *ActiveRunController) Set(cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancel = cancel
}

// Clear releases the current cancel function without calling it, and drops
// any pending steer messages.
func (c *ActiveRunController) Clear() {
	c.mu.Lock()
	c.cancel = nil
	c.steers = nil
	c.mu.Unlock()
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

// Steer queues a steering message with its attached images.
func (c *ActiveRunController) Steer(text string, images []agent.ImageBlock) {
	c.mu.Lock()
	c.steers = append(c.steers, agent.SteerMessage{Text: text, Images: images})
	c.mu.Unlock()
}

// DrainSteers returns all pending steer messages and clears the queue.
func (c *ActiveRunController) DrainSteers() []agent.SteerMessage {
	c.mu.Lock()
	steers := c.steers
	c.steers = nil
	c.mu.Unlock()
	return steers
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
		Model:    snapshot.Model,
		Messages: provider.CloneMessages(snapshot.Messages),
		Tools:    provider.CloneTools(snapshot.Tools),
		MaxTokens: func() *int {
			if snapshot.MaxTokens == nil {
				return nil
			}
			cloned := *snapshot.MaxTokens
			return &cloned
		}(),
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
		Model:    s.snapshot.Model,
		Messages: provider.CloneMessages(s.snapshot.Messages),
		Tools:    provider.CloneTools(s.snapshot.Tools),
		MaxTokens: func() *int {
			if s.snapshot.MaxTokens == nil {
				return nil
			}
			cloned := *s.snapshot.MaxTokens
			return &cloned
		}(),
		Blocks:      append([]prompt.ContextBlock(nil), s.snapshot.Blocks...),
		ModelBudget: s.snapshot.ModelBudget,
	}
	return cloned, true
}

// pendingApproval represents an outstanding mutation-tool approval that the
// TUI (or other client) has not yet responded to.
type pendingApproval struct {
	identity string
	toolName string
	mode     string
	kind     string // ApprovalKind string value
	agentID  string
	claimed  bool
	response chan SubmitApproval
}

// ApprovalCoordinator manages the lifecycle of pending approval requests
// during an interactive session.
type ApprovalCoordinator struct {
	mu    sync.Mutex
	queue []*pendingApproval
}

// Begin registers a new pending approval request and returns a channel that
// will receive the decision once the client submits one.
func (c *ApprovalCoordinator) Begin(identity, toolName, mode, kind, agentID string) chan SubmitApproval {
	response := make(chan SubmitApproval, 1)
	c.mu.Lock()
	c.queue = append(c.queue, &pendingApproval{
		identity: identity,
		toolName: toolName,
		mode:     mode,
		kind:     kind,
		agentID:  agentID,
		response: response,
	})
	c.mu.Unlock()
	return response
}

// Finish unregisters the pending approval associated with the given response
// channel.
func (c *ApprovalCoordinator) Finish(response chan SubmitApproval) {
	c.mu.Lock()
	for i, pending := range c.queue {
		if pending.response == response {
			copy(c.queue[i:], c.queue[i+1:])
			c.queue = c.queue[:len(c.queue)-1]
			break
		}
	}
	c.mu.Unlock()
}

// Submit delivers a user's approval decision to the first unclaimed request,
// if the tool and mode match. The response send happens outside the mutex so a
// duplicate submission cannot block coordinator lifecycle operations.
func (c *ApprovalCoordinator) Submit(submission SubmitApproval) {
	c.mu.Lock()
	var response chan SubmitApproval
	for _, pending := range c.queue {
		if pending.claimed {
			continue
		}
		if pending.identity == "" || submission.Identity == "" || submission.Identity != pending.identity {
			continue
		}
		if pending.toolName != "" && submission.Tool != "" && submission.Tool != pending.toolName {
			break
		}
		if pending.mode != "" && submission.Mode != "" && submission.Mode != pending.mode {
			break
		}
		pending.claimed = true
		response = pending.response
		break
	}
	c.mu.Unlock()
	if response != nil {
		response <- submission
	}
}

// HeadIdentity returns the identity of the first approval not yet claimed by a
// decision, or an empty string when no such approval exists.
func (c *ApprovalCoordinator) HeadIdentity() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, pending := range c.queue {
		if !pending.claimed {
			return pending.identity
		}
	}
	return ""
}

// HasPending reports whether an approval request is currently awaiting a
// decision.
func (c *ApprovalCoordinator) HasPending() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queue) > 0
}

// PendingDepth reports the number of approval requests awaiting a decision.
func (c *ApprovalCoordinator) PendingDepth() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queue)
}

type pendingWorkflowHandoff struct {
	request  tool.WorkflowHandoffRequest
	response chan SubmitWorkflowHandoff
}

// WorkflowHandoffCoordinator manages pending workflow handoff requests during
// an interactive session. It intentionally keeps a single pending request because
// handoffs are parent-only and cannot be concurrent.
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
