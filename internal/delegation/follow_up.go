package delegation

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// FollowUpToolName is the registered name of the follow-up tool.
const FollowUpToolName = "follow_up"

// FollowUpToolDef returns a ToolDef for resuming a delegated child session.
func FollowUpToolDef(handler func(ctx context.Context, input map[string]any) (any, error)) tool.ToolDef {
	return tool.ToolDef{
		Name:        FollowUpToolName,
		Description: "Continue work with an existing sub-agent by sending a follow-up message. Use this to resume a suitable warm agent for the same bounded deliverable in the same live workspace, sequentially, to guide incomplete work, request refinements, make related corrections with the responsible implementation agent, or request a narrow re-check from the original reviewer. Use fresh delegation for unavailable or non-resumable sessions, material lane or scope changes, independent or wider review, or removed worktrees; workflow handoffs are not safe continuation boundaries.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{"type": "string", "description": "Required. The delegated agent ID to resume."},
				"message":  map[string]any{"type": "string", "description": "Required. The follow-up user message to append."},
			},
			"required": []any{"agent_id", "message"},
		},
		Handler: handler,
	}
}

// buildContinuationRequest prepares a request for a continuation agent run.
func buildContinuationRequest(base agent.RunRequest, conversation []agent.Message, message string, priorTurns int, freshLimits DelegationLimits) agent.RunRequest {
	base.Prompt.Conversation = agent.ToReplaySafeProviderMessages(conversation)
	base.Prompt.Conversation = append(base.Prompt.Conversation, provider.Message{
		Role:    provider.MessageRoleUser,
		Content: message,
	})
	base.Limits.MaxTurns = priorTurns + freshLimits.MaxTurns
	base.Limits.MaxTokens = freshLimits.OutputLimitTokens
	base.Limits.TurnTimeout = freshLimits.Timeout
	return base
}

// NewFollowUpHandler returns the in-process handler for the follow-up tool.
func NewFollowUpHandler(deps SubAgentHandlerDeps) func(ctx context.Context, input map[string]any) (any, error) {
	return func(ctx context.Context, input map[string]any) (any, error) {
		agentID, _ := input["agent_id"].(string)
		if agentID == "" {
			return nil, fmt.Errorf("follow_up: agent_id is required")
		}

		message, _ := input["message"].(string)
		if message == "" {
			return nil, fmt.Errorf("follow_up: message is required")
		}

		if deps.SessionStore == nil {
			return nil, fmt.Errorf("follow_up: no session for agent %q", agentID)
		}

		session, ok := deps.SessionStore.Get(agentID)
		if !ok {
			return nil, fmt.Errorf("follow_up: no session for agent %q", agentID)
		}

		// Deny follow_up of code children in plan mode (they can mutate files).
		if mode, ok := ctx.Value(tool.ExecutionModeKey{}).(config.ExecutionMode); ok && mode == config.ExecutionModePlan {
			if childHasMutateTool(session.Request) {
				return nil, fmt.Errorf("follow_up: plan mode is active; the code sub-agent (which can mutate files) is unavailable. " +
					"Ask the user to switch to build mode, or call workflow_handoff when your plan is ready")
			}
		}
		freshLimits := DefaultLimits(deps.SubAgentCfg)
		req := buildContinuationRequest(session.Request, session.Conversation, message, session.TurnCount, freshLimits)

		spec := session.Spec
		spec.Limits = freshLimits
		spec.PriorCacheUsage = session.CacheUsage

		var opts []spawnOption
		if childHasMutateTool(session.Request) && session.Remediation != nil {
			opts = append(opts, WithRemediation(session.Remediation))
		}
		result, state, runUsage, err := SpawnDelegate(ctx, spec, req, deps.Runner, deps.Events, deps.TraceLogger, opts...)
		if err == nil {
			deps.SessionStore.Update(agentID, SessionUpdateParams{
				Conversation:  state.Conversation,
				TurnCount:     state.TurnCount,
				TokenCount:    state.TokenCount,
				ToolCallCount: countToolCalls(state.Conversation),
				CacheUsage:    runUsage,
			})
			updated, ok := deps.SessionStore.Get(agentID)
			if !ok {
				return nil, fmt.Errorf("follow_up: session disappeared for agent %q", agentID)
			}
			if delegationResult, ok := result.Value.(DelegationResult); ok {
				delegationResult.FollowUpCount = updated.FollowUpCount
				result.Value = delegationResult
			}
		}
		if err != nil {
			if result != (tool.ExecutionResult{}) {
				return result, nil
			}
			return nil, fmt.Errorf("follow_up failed: %w", err)
		}

		return result, nil
	}
}

// childHasMutateTool reports whether the child session's request includes the "mutate" tool.
// Only the code agent has mutate in its allowlist, so this discriminates code children.
func childHasMutateTool(req agent.RunRequest) bool {
	for _, toolSpec := range req.Tools {
		if toolSpec.Function.Name == "mutate" {
			return true
		}
	}
	return false
}
