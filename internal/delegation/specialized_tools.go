package delegation

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// SpecializedToolDeps holds dependencies shared by all specialized delegate tools.
// It embeds SubAgentHandlerDeps and adds a ModelResolver for per-type model resolution.
type SpecializedToolDeps struct {
	SubAgentHandlerDeps
	ModelResolver func(alias string) (provider.Provider, provider.ResolvedModel, error)
	// ImageStore provides image lookup for the vision sub-agent tool.
	ImageStore *agent.ImageStore
	// AgentModels maps agent type to an optional model alias override.
	AgentModels map[string]string
}

// SpecializedToolDef returns a ToolDef for the given agent type.
// The tool name matches the agent type string and accepts a "task" parameter.
// Vision uses an extended schema with an additional required "image_id" parameter
// and a dedicated handler that reads the image from the ImageStore.
func SpecializedToolDef(agentType AgentType, deps SpecializedToolDeps) tool.ToolDef {
	if agentType == AgentTypeVision {
		return tool.ToolDef{
			Name:        string(agentType),
			Description: specializedDescription(agentType),
			ParameterSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{
						"type":        "string",
						"description": "What to analyze or describe about the image.",
					},
					"image_id": map[string]any{
						"type":        "string",
						"description": "Required. The image ID to examine (e.g. 'img-1'). Shown in the image placeholder in the conversation.",
					},
				},
				"required": []any{"task", "image_id"},
			},
			Handler: newVisionHandler(deps),
		}
	}
	return tool.ToolDef{
		Name:        string(agentType),
		Description: specializedDescription(agentType),
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "Required. Self-contained task with objective, context, deliverable, constraints, success criteria, and checks to run.",
				},
			},
			"required": []any{"task"},
		},
		Handler: newSpecializedHandler(agentType, deps),
	}
}

// specializedDescription returns a short description for each agent type.
func specializedDescription(t AgentType) string {
	switch t {
	case AgentTypeExplore:
		return "Spawn a read-only exploration sub-agent to navigate the codebase and locate files, symbols, or patterns."
	case AgentTypeResearch:
		return "Spawn a research sub-agent to gather and synthesize information from the codebase or web."
	case AgentTypeCode:
		return "Spawn a coding sub-agent to implement a scoped change, run tests, and report results."
	case AgentTypeEvaluate:
		return "Spawn an analysis sub-agent to evaluate a scoped sub-problem and produce a structured recommendation."
	case AgentTypeSanityCheck:
		return "Spawn a verification sub-agent to run tests, lint, or build checks and report pass/fail results."
	case AgentTypeReview:
		return "Spawn a review sub-agent to examine code changes for bugs, regressions, missing tests, or plan adherence."
	case AgentTypeVision:
		return "Spawn a vision sub-agent to analyze an image. The sub-agent receives the image directly and describes or answers questions about it. After the initial call, use follow_up with the returned agent_id to ask additional questions about the same image — the image is cached server-side so follow-ups are cheap."
	default:
		return "Spawn a specialized sub-agent."
	}
}

// resolveModel resolves the provider and model for a specific agent type,
// applying per-type model alias overrides when configured.
func resolveModel(agentType AgentType, deps SpecializedToolDeps) (provider.Provider, provider.ResolvedModel, error) {
	if deps.ModelResolver == nil {
		return deps.Provider, deps.ResolvedModel, nil
	}
	alias, ok := deps.AgentModels[string(agentType)]
	if !ok || alias == "" {
		return deps.Provider, deps.ResolvedModel, nil
	}
	p, rm, err := deps.ModelResolver(alias)
	if err != nil {
		return nil, provider.ResolvedModel{}, fmt.Errorf("%s: resolve model %q: %w", agentType, alias, err)
	}
	return p, rm, nil
}

// mergedAllowedTools combines a base allowlist with per-agent-type extra tool
// names, returning a new sorted, deduplicated slice. The base slice is not
// mutated.
func mergedAllowedTools(base, extras []string) []string {
	seen := make(map[string]struct{}, len(base)+len(extras))
	merged := make([]string, 0, len(base)+len(extras))
	for _, name := range base {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, name)
	}
	for _, name := range extras {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, name)
	}
	slices.Sort(merged)
	return merged
}

// checkPlanModeCodeDenial returns an error if code agent is attempted in plan mode.
func checkPlanModeCodeDenial(ctx context.Context, agentType AgentType) error {
	if agentType == AgentTypeCode {
		if mode, ok := ctx.Value(tool.ExecutionModeKey{}).(config.ExecutionMode); ok && mode == config.ExecutionModePlan {
			return fmt.Errorf("code: plan mode is active; the code sub-agent (which can mutate files) is unavailable. " +
				"Ask the user to switch to build mode, or call workflow_handoff when your plan is ready")
		}
	}
	return nil
}

// provisionCodeWorktreeAndWarnings checks for dirty changes and provisions an isolated
// worktree for code agents, collecting warnings for any issues encountered.
func provisionCodeWorktreeAndWarnings(ctx context.Context, workDir string, agentID string) (CodeWorktree, []string) {
	var warnings []string
	var provisionedWorktree CodeWorktree

	// Best-effort dirty-tree check; skip silently on error.
	if paths, err := DirtyPaths(ctx, workDir); err == nil && len(paths) > 0 {
		shown := paths
		if len(paths) > 10 {
			shown = paths[:10]
		}
		warnings = append(warnings, fmt.Sprintf(
			"parent working tree has %d uncommitted change(s) not visible to the isolated worktree: %s",
			len(paths), strings.Join(shown, ", ")))
		if len(paths) > 10 {
			warnings[len(warnings)-1] = warnings[len(warnings)-1] + fmt.Sprintf(
				"...and %d more", len(paths)-10)
		}
	}

	// Provision the worktree; on failure, fall back to the parent tree with a warning.
	var err error
	provisionedWorktree, err = ProvisionCodeWorktree(ctx, workDir, agentID)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf(
			"failed to provision isolated worktree, falling back to the shared working tree: %v", err))
	}

	return provisionedWorktree, warnings
}

// applyCodeWorktreeResult updates the delegation result with worktree path, branch, and warnings.
func applyCodeWorktreeResult(result tool.ExecutionResult, worktree CodeWorktree, warnings []string) tool.ExecutionResult {
	if delegationResult, ok := result.Value.(DelegationResult); ok {
		if worktree.Path != "" {
			delegationResult.WorktreePath = worktree.Path
			delegationResult.WorktreeBranch = worktree.Branch
		}
		delegationResult.Warnings = warnings
		result.Value = delegationResult
	}
	return result
}

// newSpecializedHandler returns a handler for the given agent type.
// It uses the per-type system prompt and allowed-tool list, leaving other
// delegation parameters at their configured defaults.
func newSpecializedHandler(agentType AgentType, deps SpecializedToolDeps) func(ctx context.Context, input map[string]any) (any, error) {
	return func(ctx context.Context, input map[string]any) (any, error) {
		if err := checkPlanModeCodeDenial(ctx, agentType); err != nil {
			return nil, err
		}

		task, _ := input["task"].(string)
		if task == "" {
			return nil, fmt.Errorf("%s: task is required", agentType)
		}

		allowedTools := AgentAllowedTools(agentType)
		if deps.ExtraAllowedTools != nil {
			allowedTools = mergedAllowedTools(allowedTools, deps.ExtraAllowedTools[agentType])
		}

		agentID := generateAgentID()

		// For code agents, check for dirty changes in the parent tree and
		// provision an isolated worktree.
		var warnings []string
		var provisionedWorktree CodeWorktree
		if agentType == AgentTypeCode {
			provisionedWorktree, warnings = provisionCodeWorktreeAndWarnings(ctx, deps.WorkDir, agentID)
		}

		// Resolve per-type model if configured.
		resolvedProvider, resolvedModel, err := resolveModel(agentType, deps)
		if err != nil {
			return nil, err
		}

		// Build bootstrap deps and override WorkDir for code agents that provisioned successfully.
		bdeps := handlerBootstrapDeps(agentType, deps.SubAgentHandlerDeps, resolvedProvider, resolvedModel, allowedTools, agentType != AgentTypeCode && agentType != AgentTypeReview && agentType != AgentTypeEvaluate, agentType == AgentTypeVision)
		if agentType == AgentTypeCode && provisionedWorktree.Path != "" {
			bdeps.WorkDir = provisionedWorktree.Path
		}

		spec := DelegationSpec{
			Task:         task,
			SystemPrompt: AgentSystemPrompt(agentType),
			AgentID:      agentID,
		}

		req, limits, err := BuildChildRun(ctx, bdeps, spec)
		if err != nil {
			return nil, fmt.Errorf("%s: build child run: %w", agentType, err)
		}
		spec.Limits = limits

		result, state, runUsage, err := SpawnDelegate(ctx, spec, req, deps.Runner, deps.Events, deps.TraceLogger)
		if err == nil && deps.SessionStore != nil {
			saveChildSession(deps.SessionStore, spec, req, state, runUsage)
		}
		if err != nil {
			if result != (tool.ExecutionResult{}) {
				return result, nil
			}
			return nil, fmt.Errorf("%s failed: %w", agentType, err)
		}

		// For code agents, set the result fields with worktree info and warnings.
		if agentType == AgentTypeCode {
			result = applyCodeWorktreeResult(result, provisionedWorktree, warnings)
		}

		return result, nil
	}
}

// AllSpecializedToolDefs returns ToolDefs for recognized agent types, skipping any in excludeTypes.
func AllSpecializedToolDefs(deps SpecializedToolDeps, excludeTypes []AgentType) []tool.ToolDef {
	excluded := make(map[AgentType]bool, len(excludeTypes))
	for _, t := range excludeTypes {
		excluded[t] = true
	}
	var defs []tool.ToolDef
	for _, t := range AllAgentTypes() {
		if !excluded[t] {
			defs = append(defs, SpecializedToolDef(t, deps))
		}
	}
	return defs
}
