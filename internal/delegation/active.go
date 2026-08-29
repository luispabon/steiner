package delegation

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrAgentAlreadyActive indicates that an agent ID is already registered.
var ErrAgentAlreadyActive = errors.New("agent is already active")

type activeDelegate struct {
	cancel    context.CancelFunc
	agentType AgentType
	worktree  CodeWorktree
}

// ActiveController tracks active delegates and provides cancellation for each one.
type ActiveController struct {
	mu        sync.Mutex
	delegates map[string]activeDelegate
	order     []string
}

// NewActiveController returns an initialized ActiveController.
func NewActiveController() *ActiveController {
	return &ActiveController{
		delegates: make(map[string]activeDelegate),
	}
}

// Register registers an agent and returns its dedicated child context.
//
//revive:disable-next-line context-as-argument
func (c *ActiveController) Register(agentID string, parent context.Context, agentType AgentType, worktree CodeWorktree) (context.Context, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.delegates[agentID]; ok {
		return nil, ErrAgentAlreadyActive
	}
	if parent == nil {
		return nil, fmt.Errorf("register active agent: parent context is nil")
	}

	child, cancel := context.WithCancel(parent)
	if c.delegates == nil {
		c.delegates = make(map[string]activeDelegate)
	}
	c.delegates[agentID] = activeDelegate{
		cancel:    cancel,
		agentType: agentType,
		worktree:  worktree,
	}
	c.order = append(c.order, agentID)
	return child, nil
}

// CancelAgent cancels the registered child context for agentID.
func (c *ActiveController) CancelAgent(agentID string) bool {
	c.mu.Lock()
	delegate, ok := c.delegates[agentID]
	c.mu.Unlock()
	if !ok {
		return false
	}

	delegate.cancel()
	return true
}

// CancelAll cancels every registered child context without removing registrations.
func (c *ActiveController) CancelAll() {
	c.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(c.order))
	for _, agentID := range c.order {
		if delegate, ok := c.delegates[agentID]; ok {
			cancels = append(cancels, delegate.cancel)
		}
	}
	c.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}

// Unregister removes agentID from the active delegate set.
func (c *ActiveController) Unregister(agentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.delegates[agentID]; !ok {
		return
	}
	delete(c.delegates, agentID)
	for i, registeredID := range c.order {
		if registeredID == agentID {
			copy(c.order[i:], c.order[i+1:])
			c.order = c.order[:len(c.order)-1]
			break
		}
	}
}

// ActiveAgentIDs returns active agent IDs in registration order.
func (c *ActiveController) ActiveAgentIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	ids := make([]string, 0, len(c.delegates))
	for _, agentID := range c.order {
		if _, ok := c.delegates[agentID]; ok {
			ids = append(ids, agentID)
		}
	}
	return ids
}

// WorktreeFor returns the managed worktree for an active agent.
func (c *ActiveController) WorktreeFor(agentID string) (CodeWorktree, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delegate, ok := c.delegates[agentID]
	if !ok {
		return CodeWorktree{}, false
	}
	return delegate.worktree, true
}

// TypeFor returns the type for an active agent.
func (c *ActiveController) TypeFor(agentID string) (AgentType, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delegate, ok := c.delegates[agentID]
	if !ok {
		return "", false
	}
	return delegate.agentType, true
}
