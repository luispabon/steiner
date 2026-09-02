package delegation

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/luispabon/steiner/internal/advisor"
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

// SubAgentToolDef returns a unified ToolDef for all sub-agent types with a type enum parameter.
// It accepts structured task fields: type, objective, context, deliverable, constraints,
// success_criteria, checks, and optionally image_id. Vision requires image_id.
func SubAgentToolDef(deps SpecializedToolDeps, excludeTypes []AgentType) tool.ToolDef {
	excluded := make(map[AgentType]bool, len(excludeTypes))
	for _, t := range excludeTypes {
		excluded[t] = true
	}

	var enumValues []any
	var typeDescriptionParts []string
	for _, agentType := range AllAgentTypes() {
		if !excluded[agentType] {
			enumValues = append(enumValues, string(agentType))
			typeDescriptionParts = append(typeDescriptionParts, fmt.Sprintf("%s: %s", agentType, specializedDescription(agentType)))
		}
	}

	typeDescription := strings.Join(typeDescriptionParts, " | ")

	return tool.ToolDef{
		Name:        SubAgentToolName,
		Description: "Spawn a specialized sub-agent of the given type; see the type parameter for what each type does.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type": map[string]any{
					"type":        "string",
					"enum":        enumValues,
					"description": typeDescription,
				},
				"objective": map[string]any{
					"type":        "string",
					"description": "The single outcome this child must achieve.",
				},
				"context": map[string]any{
					"type":        "string",
					"description": "Relevant paths, symbols, excerpts and background: why this task exists, how it fits the caller's larger plan, decisions already made and approaches ruled out. The child cannot see the caller's conversation and will not otherwise learn any of this.",
				},
				"deliverable": map[string]any{
					"type":        "string",
					"description": "The exact artifact or answer to return, and its shape.",
				},
				"constraints": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Boundaries, preserved behaviour, allowed scope, prohibited actions.",
				},
				"success_criteria": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Observable conditions for completion.",
				},
				"checks": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Applicable commands or validations to run.",
				},
				"image_id": map[string]any{
					"type":        "string",
					"description": "Required when type is \"vision\". The image ID to examine (e.g. 'img-1'). Shown in the image placeholder in the conversation.",
				},
			},
			"required": []any{"type", "objective", "context", "deliverable", "constraints", "success_criteria", "checks"},
		},
		Handler: newSubAgentDispatchHandler(deps, excluded),
	}
}

// newSubAgentDispatchHandler returns a handler that routes to the appropriate
// sub-agent handler based on the type parameter.
func newSubAgentDispatchHandler(deps SpecializedToolDeps, excluded map[AgentType]bool) func(ctx context.Context, input map[string]any) (any, error) {
	return func(ctx context.Context, input map[string]any) (any, error) {
		rawType, _ := input["type"].(string)
		rawType = strings.TrimSpace(rawType)
		if rawType == "" {
			return nil, fmt.Errorf("sub_agent: type is required and must be non-empty")
		}

		if !ValidAgentType(rawType) {
			validTypes := availableAgentTypeNames(excluded)
			return nil, fmt.Errorf("sub_agent: unknown or unavailable type %q; valid types: %s", rawType, strings.Join(validTypes, ", "))
		}

		agentType := AgentType(rawType)
		if excluded[agentType] {
			validTypes := availableAgentTypeNames(excluded)
			return nil, fmt.Errorf("sub_agent: type %q is unavailable; valid types: %s", rawType, strings.Join(validTypes, ", "))
		}

		if agentType == AgentTypeVision {
			imageID, _ := input["image_id"].(string)
			imageID = strings.TrimSpace(imageID)
			if imageID == "" {
				return nil, fmt.Errorf("sub_agent: type is \"vision\" but image_id is missing or empty")
			}
			return newVisionHandler(deps)(ctx, input)
		}
		return newSpecializedHandler(agentType, deps)(ctx, input)
	}
}

// availableAgentTypeNames returns the string names of all agent types not in
// excluded, in AllAgentTypes order.
func availableAgentTypeNames(excluded map[AgentType]bool) []string {
	validTypes := make([]string, 0, len(AllAgentTypes())-len(excluded))
	for _, t := range AllAgentTypes() {
		if !excluded[t] {
			validTypes = append(validTypes, string(t))
		}
	}
	return validTypes
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

// structuredBrief holds the decoded fields from a structured task dispatch.
type structuredBrief struct {
	Objective       string   `json:"objective"`
	Ctx             string   `json:"context"`
	Deliverable     string   `json:"deliverable"`
	Constraints     []string `json:"constraints"`
	SuccessCriteria []string `json:"success_criteria"`
	Checks          []string `json:"checks"`
}

// assembleTaskContent renders a structuredBrief into a deterministic markdown
// message for the child agent. The output always uses the fixed field order
// (Objective, Context, Deliverable, Constraints, Success criteria, Checks)
// and omits empty optional sections entirely. This text becomes part of the
// cached prompt prefix, so determinism is essential.
func assembleTaskContent(b structuredBrief) string {
	var buf strings.Builder

	buf.WriteString("## Objective\n\n")
	buf.WriteString(b.Objective)
	buf.WriteString("\n\n")

	buf.WriteString("## Context\n\n")
	buf.WriteString(b.Ctx)
	buf.WriteString("\n\n")

	buf.WriteString("## Deliverable\n\n")
	buf.WriteString(b.Deliverable)

	if len(b.Constraints) > 0 {
		buf.WriteString("\n\n## Constraints\n\n")
		for i, item := range b.Constraints {
			if i > 0 {
				buf.WriteString("\n")
			}
			buf.WriteString("- ")
			buf.WriteString(item)
		}
	}

	if len(b.SuccessCriteria) > 0 {
		buf.WriteString("\n\n## Success criteria\n\n")
		for i, item := range b.SuccessCriteria {
			if i > 0 {
				buf.WriteString("\n")
			}
			buf.WriteString("- ")
			buf.WriteString(item)
		}
	}

	if len(b.Checks) > 0 {
		buf.WriteString("\n\n## Checks\n\n")
		for i, item := range b.Checks {
			if i > 0 {
				buf.WriteString("\n")
			}
			buf.WriteString("- ")
			buf.WriteString(item)
		}
	}

	return buf.String()
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

		brief := structuredBrief{
			Constraints:     []string{},
			SuccessCriteria: []string{},
			Checks:          []string{},
		}

		objective, _ := input["objective"].(string)
		objective = strings.TrimSpace(objective)
		if objective == "" {
			return nil, fmt.Errorf("%s: objective is required and must be non-empty", agentType)
		}
		brief.Objective = objective

		contextStr, _ := input["context"].(string)
		contextStr = strings.TrimSpace(contextStr)
		if contextStr == "" {
			return nil, fmt.Errorf("%s: context is required and must be non-empty", agentType)
		}
		brief.Ctx = contextStr

		deliverable, _ := input["deliverable"].(string)
		deliverable = strings.TrimSpace(deliverable)
		if deliverable == "" {
			return nil, fmt.Errorf("%s: deliverable is required and must be non-empty", agentType)
		}
		brief.Deliverable = deliverable

		if constraintsRaw, ok := input["constraints"].([]any); ok {
			for i, item := range constraintsRaw {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("%s: constraints[%d] is not a string", agentType, i)
				}
				brief.Constraints = append(brief.Constraints, s)
			}
		}

		if criteriaRaw, ok := input["success_criteria"].([]any); ok {
			for i, item := range criteriaRaw {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("%s: success_criteria[%d] is not a string", agentType, i)
				}
				brief.SuccessCriteria = append(brief.SuccessCriteria, s)
			}
		}

		if checksRaw, ok := input["checks"].([]any); ok {
			for i, item := range checksRaw {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("%s: checks[%d] is not a string", agentType, i)
				}
				brief.Checks = append(brief.Checks, s)
			}
		}

		task := assembleTaskContent(brief)

		agentID := generateAgentID()
		callID, _ := ctx.Value(tool.ExecutionCallIDKey{}).(string)
		spec := Spec{
			Task:         task,
			AgentType:    agentType,
			SystemPrompt: AgentSystemPrompt(agentType),
			ParentCallID: callID,
			AgentID:      agentID,
		}

		allowedTools, resolvedProvider, resolvedModel, err := resolveToolsAndModel(agentType, deps)
		if err != nil {
			emitDelegateFailed(deps.Events, spec, agentType, err.Error())
			return nil, childSetupError(err)
		}

		advisorAvailable := slices.Contains(allowedTools, advisor.ToolName) && deps.AdvisorForChild != nil
		spec.SystemSuffix = AgentSystemSuffix(agentType, advisorAvailable)
		spec.AdvisorBudget = effectiveAdvisorBudget(advisorAvailable, deps.AdvisorSubAgentBudget)

		provisionedWorktree, warnings, err := specializedWorktree(ctx, agentType, deps.WorkDir, agentID)
		if err != nil {
			emitDelegateFailed(deps.Events, spec, agentType, err.Error())
			return nil, childSetupError(err)
		}

		handlerDeps, override := specializedBootstrapDeps(agentType, deps, resolvedProvider, resolvedModel, allowedTools, provisionedWorktree)

		req, limits, err := BuildChildRun(ctx, handlerDeps, override, spec)
		if err != nil {
			err = fmt.Errorf("%s: build child run: %w", agentType, err)
			emitDelegateFailed(deps.Events, spec, agentType, err.Error())
			return nil, childSetupError(err)
		}
		spec.Limits = limits
		childCtx, err := deps.ActiveController.Register(agentID, ctx, agentType, provisionedWorktree)
		if err != nil {
			cleanupRegistrationWorktree(agentType, deps.WorkDir, provisionedWorktree)
			return nil, childSetupError(err)
		}
		defer deps.ActiveController.Unregister(agentID)
		emitDelegateStarted(deps.Events, spec, req.ResolvedModel.Alias, agentType)
		var gateRelease func()
		req.Events, gateRelease = applyDispatchGate(childCtx, deps.CacheKeyStore, req.PromptCacheKey, spec.AgentID, spec.ParentCallID, deps.Events, req.Events)
		defer gateRelease()
		if childCtx.Err() != nil {
			removeAndCloseToolCallTraceWriter(spec.AgentID)
			emitDelegateStopped(deps.Events, spec, agentType)
			result := applySpecializedWorktreeResult(agentType, cancelledBeforeDispatchResult(spec.AgentID), provisionedWorktree, warnings)
			if dr, ok := result.Value.(Result); ok {
				dr.AdvisorBudget = spec.AdvisorBudget
				result.Value = dr
			}
			if deps.SessionStore != nil && deps.SessionStore.Save(&ChildSession{Spec: spec, Request: req, Remediation: codeRemediationConfig(provisionedWorktree)}) {
				if dr, ok := result.Value.(Result); ok {
					dr.persisted = true
					result.Value = dr
				}
			}
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
			if saveChildSession(deps.SessionStore, spec, req, state, runUsage, remediation) {
				if dr, ok := result.Value.(Result); ok {
					dr.persisted = true
					result.Value = dr
				}
			}
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
