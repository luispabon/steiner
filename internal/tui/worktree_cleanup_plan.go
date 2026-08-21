package tui

import (
	"context"
	"sync"
)

// WorktreeCleanupPlan coordinates offering to prune this process's delegate
// worktrees on session exit. The TUI counts and records user intent; the actual
// prune is executed later at teardown after runs are joined.
type WorktreeCleanupPlan struct {
	list   func(context.Context) (int, error)
	prune  func(context.Context) (int, error)
	mu     sync.Mutex
	intent bool
}

// NewWorktreeCleanupPlan creates a worktree cleanup plan from count and prune functions.
func NewWorktreeCleanupPlan(list func(context.Context) (int, error), prune func(context.Context) (int, error)) *WorktreeCleanupPlan {
	return &WorktreeCleanupPlan{list: list, prune: prune}
}

// Count returns the number of worktrees available for cleanup. It passes ctx to
// the list operation so callers can bound or cancel the count.
func (p *WorktreeCleanupPlan) Count(ctx context.Context) (int, error) {
	if p == nil || p.list == nil {
		return 0, nil
	}
	return p.list(ctx)
}

// Request records the user's intent to prune worktrees at session teardown.
func (p *WorktreeCleanupPlan) Request() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.intent = true
	p.mu.Unlock()
}

// ShouldPrune reports whether the user requested cleanup at session teardown.
func (p *WorktreeCleanupPlan) ShouldPrune() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.intent
}

// Prune runs the configured cleanup operation.
func (p *WorktreeCleanupPlan) Prune(ctx context.Context) (int, error) {
	if p == nil || p.prune == nil {
		return 0, nil
	}
	return p.prune(ctx)
}
