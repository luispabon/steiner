package delegation

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/advisor"
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
func buildContinuationRequest(base agent.RunRequest, conversation []agent.Message, message string, priorTurns int, freshLimits Limits) agent.RunRequest {
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
	if deps.ActiveController == nil {
		deps.ActiveController = NewActiveController()
	}
	return func(ctx context.Context, input map[string]any) (any, error) {
		return runFollowUp(ctx, input, deps)
	}
}

func runFollowUp(ctx context.Context, input map[string]any, deps SubAgentHandlerDeps) (any, error) {
	agentID, message, session, err := validateFollowUp(input, deps)
	if err != nil {
		return nil, err
	}
	childHasMutate := childHasMutateTool(session.Request)
	isCode := childHasMutate && session.Remediation != nil
	if err := denyFollowUpInPlanMode(ctx, childHasMutate); err != nil {
		return nil, err
	}
	freshLimits := DefaultLimits(deps.SubAgentCfg)
	req := buildContinuationRequest(session.Request, session.Conversation, message, session.TurnCount, freshLimits)

	spec := session.Spec
	spec.Limits = freshLimits
	spec.PriorTokenUsage = session.TokenUsage
	// ParentCallID must be this follow_up call's own CallID, not the
	// original delegate call's (carried over from session.Spec) — the TUI
	// binds each follow-up's display box to its DelegationStartedEvent by
	// matching CallID, and a stale ID causes it to fall back to FIFO
	// matching, misrouting streaming into an unrelated agent's box.
	spec.ParentCallID, _ = ctx.Value(tool.ExecutionCallIDKey{}).(string)
	advisorAvailable := childHasAdvisorTool(session.Request)
	spec.AdvisorBudget = effectiveAdvisorBudget(advisorAvailable, deps.AdvisorSubAgentBudget)

	worktree := CodeWorktree{}
	if isCode {
		worktree = CodeWorktree{
			Path:   session.Remediation.WorktreePath,
			Branch: session.Remediation.ExpectedBranch,
		}
	}
	childCtx, err := deps.ActiveController.Register(agentID, ctx, spec.AgentType, worktree)
	if err != nil {
		return nil, childSetupError(err)
	}
	defer deps.ActiveController.Unregister(agentID)
	emitDelegateStarted(deps.Events, spec, req.ResolvedModel.Alias, spec.AgentType)

	var opts []spawnOption
	if isCode {
		opts = append(opts, WithRemediation(session.Remediation))
	}
	opts = append(opts, withChildDone(func() { deps.ActiveController.MarkComplete(agentID) }))
	result, state, runUsage, err := SpawnDelegate(childCtx, spec, req, deps.Runner, deps.Events, deps.TraceLogger, opts...)
	if err == nil {
		updatedOK := deps.SessionStore.Update(agentID, SessionUpdateParams{
			Conversation:  state.Conversation,
			TurnCount:     state.TurnCount,
			TokenCount:    runUsage.OutputTokens,
			ToolCallCount: countToolCalls(state.Conversation),
			TokenUsage:    runUsage,
		})
		updated, ok := deps.SessionStore.Get(agentID)
		if !updatedOK || !ok {
			return nil, fmt.Errorf("follow_up: session disappeared for agent %q", agentID)
		}
		if delegationResult, ok := result.Value.(Result); ok {
			delegationResult.FollowUpCount = updated.FollowUpCount
			delegationResult.persisted = true
			result.Value = delegationResult
		}
	}
	applyFinalizeCancellation(deps.Events, deps.SessionStore, deps.ActiveController, deps.WorkDir, agentID, &result)
	if err != nil {
		if result != (tool.ExecutionResult{}) {
			return result, nil
		}
		return nil, fmt.Errorf("follow_up failed: %w", err)
	}

	return result, nil
}

func validateFollowUp(input map[string]any, deps SubAgentHandlerDeps) (string, string, *ChildSession, error) {
	agentID, _ := input["agent_id"].(string)
	if agentID == "" {
		return "", "", nil, fmt.Errorf("follow_up: agent_id is required")
	}
	message, _ := input["message"].(string)
	if message == "" {
		return "", "", nil, fmt.Errorf("follow_up: message is required")
	}
	if deps.SessionStore == nil {
		return "", "", nil, fmt.Errorf("follow_up: no session for agent %q", agentID)
	}
	session, ok := deps.SessionStore.Get(agentID)
	if !ok {
		return "", "", nil, fmt.Errorf("follow_up: no session for agent %q", agentID)
	}
	return agentID, message, session, nil
}

func denyFollowUpInPlanMode(ctx context.Context, childHasMutate bool) error {
	mode, ok := ctx.Value(tool.ExecutionModeKey{}).(config.ExecutionMode)
	if !ok || mode != config.ExecutionModePlan || !childHasMutate {
		return nil
	}
	return fmt.Errorf("follow_up: plan mode is active; the code sub-agent (which can mutate files) is unavailable. " +
		"Ask the user to switch to build mode, or call workflow_handoff when your plan is ready")
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

// childHasAdvisorTool reports whether the child session's request includes the "advisor" tool.
func childHasAdvisorTool(req agent.RunRequest) bool {
	for _, toolSpec := range req.Tools {
		if toolSpec.Function.Name == advisor.ToolName {
			return true
		}
	}
	return false
}
