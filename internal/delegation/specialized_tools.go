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
	// DefaultModel is the selected profile's fallback model alias.
	DefaultModel string
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
	alias := strings.TrimSpace(deps.AgentModels[string(agentType)])
	if alias == "" {
		if agentType == AgentTypeVision {
			return deps.Provider, deps.ResolvedModel, nil
		}
		alias = strings.TrimSpace(deps.DefaultModel)
		if alias == "" {
			return deps.Provider, deps.ResolvedModel, nil
		}
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
func provisionCodeWorktreeAndWarnings(ctx context.Context, workDir string, agentID string) (CodeWorktree, []string, error) {
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

	provisionedWorktree, err := ProvisionCodeWorktree(ctx, workDir, agentID)
	if err != nil {
		return provisionedWorktree, warnings, err
	}

	return provisionedWorktree, warnings, nil
}

// applyCodeWorktreeResult updates the delegation result with worktree path, branch, and warnings.
func applyCodeWorktreeResult(result tool.ExecutionResult, worktree CodeWorktree, warnings []string) tool.ExecutionResult {
	if delegationResult, ok := result.Value.(Result); ok {
		if worktree.Path != "" {
			delegationResult.WorktreePath = worktree.Path
			delegationResult.WorktreeBranch = worktree.Branch
		}
		delegationResult.Warnings = append(append([]string(nil), warnings...), delegationResult.Warnings...)
		result.Value = delegationResult
	}
	return result
}

func nonEmptyLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// resolveToolsAndModel resolves the allowed tools list and model for the agent type.
func resolveToolsAndModel(agentType AgentType, deps SpecializedToolDeps) ([]string, provider.Provider, provider.ResolvedModel, error) {
	allowedTools := AgentAllowedTools(agentType)
	if deps.ExtraAllowedTools != nil {
		allowedTools = mergedAllowedTools(allowedTools, deps.ExtraAllowedTools[agentType])
	}

	resolvedProvider, resolvedModel, err := resolveModel(agentType, deps)
	return allowedTools, resolvedProvider, resolvedModel, err
}

func specializedWorktree(ctx context.Context, agentType AgentType, workDir, agentID string) (CodeWorktree, []string, error) {
	if agentType != AgentTypeCode {
		return CodeWorktree{}, nil, nil
	}
	worktree, warnings, err := provisionCodeWorktreeAndWarnings(ctx, workDir, agentID)
	if err != nil {
		return CodeWorktree{}, nil, fmt.Errorf("%s: %w", agentType, err)
	}
	return worktree, warnings, nil
}

func codeRemediationConfig(worktree CodeWorktree) *RemediationConfig {
	if worktree.Path == "" {
		return nil
	}
	return &RemediationConfig{
		WorktreePath:   worktree.Path,
		ExpectedBranch: worktree.Branch,
		IsDirty: func(ctx context.Context) ([]string, error) {
			return DirtyPaths(ctx, worktree.Path)
		},
		Head: func(ctx context.Context) (string, error) {
			out, err := gitOutput(ctx, worktree.Path, "rev-parse", "HEAD")
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(out), nil
		},
		Committed: func(ctx context.Context, preHEAD string, initialDirty []string) (bool, error) {
			diffOut, err := gitOutput(ctx, worktree.Path, "diff", "--name-only", preHEAD+"..HEAD")
			if err != nil {
				return false, err
			}
			committedPaths := nonEmptyLines(diffOut)
			// Every initially-dirty path must appear in the committed diff.
			for _, p := range initialDirty {
				if !slices.Contains(committedPaths, p) {
					return false, nil
				}
			}
			// Tree must now be clean.
			stillDirty, err := DirtyPaths(ctx, worktree.Path)
			if err != nil {
				return false, err
			}
			return len(stillDirty) == 0, nil
		},
	}
}

func applySpecializedWorktreeResult(agentType AgentType, result tool.ExecutionResult, worktree CodeWorktree, warnings []string) tool.ExecutionResult {
	if agentType == AgentTypeCode {
		return applyCodeWorktreeResult(result, worktree, warnings)
	}
	return result
}

func cleanupRegistrationWorktree(agentType AgentType, workDir string, worktree CodeWorktree) {
	if agentType != AgentTypeCode || workDir == "" || worktree.Path == "" {
		return
	}
	_, _ = pruneCodeWorktree(workDir, worktree)
}

func specializedBootstrapDeps(agentType AgentType, deps SpecializedToolDeps, resolvedProvider provider.Provider, resolvedModel provider.ResolvedModel, allowedTools []string, worktree CodeWorktree) (SubAgentHandlerDeps, ChildBootstrapOverrides) {
	handlerDeps := deps.SubAgentHandlerDeps
	projectRoot := handlerDeps.WorkDir
	if agentType == AgentTypeCode && worktree.Path != "" {
		handlerDeps.WorkDir = worktree.Path
	}
	return handlerDeps, ChildBootstrapOverrides{
		AgentType:     agentType,
		AllowedTools:  allowedTools,
		Provider:      resolvedProvider,
		ResolvedModel: resolvedModel,
		ProjectRoot:   projectRoot,
	}
}

// newSpecializedHandler returns a handler for the given agent type.
// It uses the per-type system prompt and allowed-tool list, leaving other
// delegation parameters at their configured defaults.
//
//nolint:gocyclo // handler lifecycle branches cover setup, gating, execution, and cleanup.
func newSpecializedHandler(agentType AgentType, deps SpecializedToolDeps) func(ctx context.Context, input map[string]any) (any, error) {
	if deps.ActiveController == nil {
		deps.ActiveController = NewActiveController()
	}
	return func(ctx context.Context, input map[string]any) (any, error) {
		if err := checkPlanModeCodeDenial(ctx, agentType); err != nil {
			return nil, err
		}

		task, _ := input["task"].(string)
		if task == "" {
			return nil, fmt.Errorf("%s: task is required", agentType)
		}

		agentID := generateAgentID()
		callID, _ := ctx.Value(tool.ExecutionCallIDKey{}).(string)
		spec := Spec{
			Task:         task,
			AgentType:    agentType,
			SystemPrompt: AgentSystemPrompt(agentType),
			ParentCallID: callID,
			AgentID:      agentID,
		}
		if agentType == AgentTypeCode {
			spec.SystemSuffix = AgentSystemSuffix(agentType)
		}

		allowedTools, resolvedProvider, resolvedModel, err := resolveToolsAndModel(agentType, deps)
		if err != nil {
			emitDelegateFailed(deps.Events, spec, agentType, err.Error())
			return nil, err
		}

		provisionedWorktree, warnings, err := specializedWorktree(ctx, agentType, deps.WorkDir, agentID)
		if err != nil {
			emitDelegateFailed(deps.Events, spec, agentType, err.Error())
			return nil, err
		}

		handlerDeps, override := specializedBootstrapDeps(agentType, deps, resolvedProvider, resolvedModel, allowedTools, provisionedWorktree)

		req, limits, err := BuildChildRun(ctx, handlerDeps, override, spec)
		if err != nil {
			err = fmt.Errorf("%s: build child run: %w", agentType, err)
			emitDelegateFailed(deps.Events, spec, agentType, err.Error())
			return nil, err
		}
		spec.Limits = limits
		childCtx, err := deps.ActiveController.Register(agentID, ctx, agentType, provisionedWorktree)
		if err != nil {
			cleanupRegistrationWorktree(agentType, deps.WorkDir, provisionedWorktree)
			return nil, err
		}
		defer deps.ActiveController.Unregister(agentID)
		emitDelegateStarted(deps.Events, spec, req.ResolvedModel.Alias, agentType)
		var gateRelease func()
		req.Events, gateRelease = applyDispatchGate(childCtx, deps.CacheKeyStore, req.PromptCacheKey, spec.AgentID, spec.ParentCallID, deps.Events, req.Events)
		defer gateRelease()
		if childCtx.Err() != nil {
			emitDelegateStopped(deps.Events, spec, agentType)
			result := applySpecializedWorktreeResult(agentType, cancelledBeforeDispatchResult(spec.AgentID), provisionedWorktree, warnings)
			applyFinalizeCancellation(deps.Events, deps.SessionStore, deps.ActiveController, deps.WorkDir, spec.AgentID, &result)
			return result, nil
		}

		remediation := codeRemediationConfig(provisionedWorktree)

		var opts []spawnOption
		if remediation != nil {
			opts = append(opts, WithRemediation(remediation))
		}
		opts = append(opts, withChildDone(func() { deps.ActiveController.MarkComplete(spec.AgentID) }))
		result, state, runUsage, err := SpawnDelegate(childCtx, spec, req, deps.Runner, deps.Events, deps.TraceLogger, opts...)
		if err == nil && deps.SessionStore != nil {
			saveChildSession(deps.SessionStore, spec, req, state, runUsage, remediation)
		}
		if err != nil {
			if result != (tool.ExecutionResult{}) {
				return result, nil
			}
			return nil, fmt.Errorf("%s failed: %w", agentType, err)
		}

		result = applySpecializedWorktreeResult(agentType, result, provisionedWorktree, warnings)
		applyFinalizeCancellation(deps.Events, deps.SessionStore, deps.ActiveController, deps.WorkDir, spec.AgentID, &result)

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
