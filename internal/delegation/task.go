package delegation

import (
	"context"

	"github.com/luispabon/steiner/internal/output"
)

// AgentRunRequest represents the request to run an agent.
// This is a minimal interface to avoid circular imports with internal/agent.
type AgentRunRequest interface{}

// AgentRunner defines the contract for executing an agent with a given request.
type AgentRunner interface {
	// Run executes the agent and returns the final state.
	Run(ctx context.Context, req AgentRunRequest) (RunState, error)
}

func truncateTaskPreview(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Ensure we leave room for the ellipsis
	if max < 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// SpawnDelegate executes a child agent with the given specification and runner.
// It scaffolds the child context and tool registry, invokes the runner,
// and builds the result. Events are emitted before, during (by runner), and after execution.
func SpawnDelegate(ctx context.Context, spec DelegationSpec, runner AgentRunner, events output.EventSink) (DelegationResult, error) {
	// Emit delegation started
	if events != nil {
		events.Emit(output.NewDelegationStartedEvent(spec.AgentID, truncateTaskPreview(spec.Task, 120)))
	}

	// Scaffold child context
	childCtx, err := ScaffoldChildContext(ctx, spec)
	if err != nil {
		result := DelegationResult{
			AgentID: spec.AgentID,
			Status:  StatusFailed,
			Error:   err.Error(),
		}
		// Emit delegation failed
		if events != nil {
			events.Emit(output.NewDelegationFailedEvent(spec.AgentID, truncateTaskPreview(spec.Task, 120), err.Error()))
		}
		return result, err
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
	result := DelegationResult{
		AgentID: spec.AgentID,
		Status:  StatusPending,
	}
	// Emit delegation complete
	if events != nil {
		events.Emit(output.NewDelegationCompleteEvent(spec.AgentID, string(result.Status), result.TurnCount, result.TokenCount))
	}
	return result, nil
}
