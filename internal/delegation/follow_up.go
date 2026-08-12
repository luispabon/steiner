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
		Description: "Continue work with an existing sub-agent by sending a follow-up message. Use this to guide incomplete work, ask for refinements, or request additional investigation from a previous delegation.",
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
		req := session.Request
		req.Prompt.Conversation = agent.ToReplaySafeProviderMessages(session.Conversation)
		req.Prompt.Conversation = append(req.Prompt.Conversation, provider.Message{
			Role:    provider.MessageRoleUser,
			Content: message,
		})

		freshLimits := DefaultLimits(deps.SubAgentCfg)
		// Include prior turn count so runner does not immediately stop with StopReasonMaxTurns.
		req.Limits.MaxTurns = session.TurnCount + freshLimits.MaxTurns
		req.Limits.MaxTokens = freshLimits.OutputLimitTokens
		req.Limits.TurnTimeout = freshLimits.Timeout

		spec := session.Spec
		spec.Limits = freshLimits
		spec.PriorCacheUsage = session.CacheUsage

		result, state, runUsage, err := SpawnDelegate(ctx, spec, req, deps.Runner, deps.Events, deps.TraceLogger)
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
