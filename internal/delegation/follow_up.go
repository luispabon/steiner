package delegation

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// FollowUpToolName is the registered name of the follow-up tool.
const FollowUpToolName = "follow_up"

// FollowUpToolDef returns a ToolDef for resuming a delegated child session.
func FollowUpToolDef(handler func(ctx context.Context, input map[string]any) (any, error)) tool.ToolDef {
	return tool.ToolDef{
		Name:        FollowUpToolName,
		Description: "Continue work with an existing sub-agent by sending a follow-up message. Use this to guide incomplete work, ask for refinements, or request additional investigation from a previous delegation. The child session persists across follow-ups: even if a follow-up is cancelled, returns no work, or hits a budget limit, the session is preserved in the store and can be resumed with follow_up using the same agent_id. When a follow-up result has status=\"cancelled\" and session_resumable=true, the session is still warm; do not conclude the session is gone.",
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
func NewFollowUpHandler(deps DelegateHandlerDeps) func(ctx context.Context, input map[string]any) (any, error) {
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

		result, state, err := SpawnDelegate(ctx, spec, req, deps.Runner, deps.Events, deps.TraceLogger)
		if err == nil {
			deps.SessionStore.Update(agentID, state.Conversation, state.TurnCount, state.TokenCount, countToolCalls(state.Conversation))
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
