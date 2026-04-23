package delegation

import (
	"context"
)

// AgentRunRequest represents the request to run an agent.
// This is a minimal interface to avoid circular imports with internal/agent.
type AgentRunRequest interface{}

// AgentRunner defines the contract for executing an agent with a given request.
type AgentRunner interface {
	// Run executes the agent and returns the final state.
	Run(ctx context.Context, req AgentRunRequest) (RunState, error)
}

// SpawnDelegate executes a child agent with the given specification and runner.
// It scaffolds the child context and tool registry, invokes the runner,
// and builds the result. No events are emitted by this function.
func SpawnDelegate(ctx context.Context, spec DelegationSpec, runner AgentRunner) (DelegationResult, error) {
	// Scaffold child context
	childCtx, err := ScaffoldChildContext(ctx, spec)
	if err != nil {
		return DelegationResult{
			AgentID: spec.AgentID,
			Status:  StatusFailed,
			Error:   err.Error(),
		}, err
	}

	// Build child tool registry (from parent would be passed by the caller)
	// For now, we just return an empty result if the registry is not provided
	// The actual invocation will be done in agent/loop or a higher-level function

	// Note: The actual runner.Run() would need:
	// - req.Provider
	// - req.Executor
	// - req.Tools (from parent registry)
	// - req.Prompt (with systemPrompt from childCtx, conversation empty, etc.)
	// - req.Model (from spec.Model or override)
	// - req.Limits (from spec.Limits)
	// - req.Events (for child-agent events)
	//
	// This function delegates that assembly to the caller.

	_ = childCtx // Currently unused; will be used by caller when implementing RunRequest assembly
	_ = runner   // Currently unused; will be used when RunRequest is properly defined

	// Placeholder result for now
	return DelegationResult{
		AgentID: spec.AgentID,
		Status:  StatusPending,
	}, nil
}
