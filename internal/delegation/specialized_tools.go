package delegation

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// SpecializedToolDeps holds dependencies shared by all specialized delegate tools.
// It embeds DelegateHandlerDeps and adds a ModelResolver for per-type model resolution.
type SpecializedToolDeps struct {
	DelegateHandlerDeps
	ModelResolver func(alias string) (provider.Provider, provider.ResolvedModel, error)
}

// SpecializedToolDef returns a ToolDef for the given agent type.
// The tool name matches the agent type string and accepts only a "task" parameter.
func SpecializedToolDef(agentType AgentType, deps SpecializedToolDeps) tool.ToolDef {
	return tool.ToolDef{
		Name:        string(agentType),
		Description: specializedDescription(agentType),
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "Required. Self-contained task with objective, file paths/context, expected deliverable, constraints, and success criteria.",
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
	case AgentTypePlan:
		return "Spawn an analysis sub-agent to evaluate a sub-problem and produce a structured recommendation."
	case AgentTypeVerify:
		return "Spawn a verification sub-agent to run checks, tests, or linters and report pass/fail results."
	default:
		return "Spawn a specialized sub-agent."
	}
}

// newSpecializedHandler returns a handler for the given agent type.
// It uses the per-type system prompt and allowed-tool list, leaving other
// delegation parameters at their configured defaults.
func newSpecializedHandler(agentType AgentType, deps SpecializedToolDeps) func(ctx context.Context, input map[string]any) (any, error) {
	return func(ctx context.Context, input map[string]any) (any, error) {
		task, _ := input["task"].(string)
		if task == "" {
			return nil, fmt.Errorf("%s: task is required", agentType)
		}

		agentID := generateAgentID()
		spec := DelegationSpec{
			Task:         task,
			SystemPrompt: AgentSystemPrompt(agentType),
			AgentID:      agentID,
		}

		// Build a modified SubAgentConfig with the per-type tool allowlist.
		typedCfg := deps.SubAgentCfg
		typedCfg.AllowedTools = AgentAllowedTools(agentType)

		// Resolve per-type model if configured.
		resolvedProvider := deps.Provider
		resolvedModel := deps.ResolvedModel
		if deps.ModelResolver != nil {
			if ac, ok := deps.SubAgentCfg.Agents[string(agentType)]; ok && ac.Model != "" {
				p, rm, err := deps.ModelResolver(ac.Model)
				if err != nil {
					return nil, fmt.Errorf("%s: resolve model %q: %w", agentType, ac.Model, err)
				}
				resolvedProvider = p
				resolvedModel = rm
			}
		}

		req, limits, err := BuildChildRun(ctx, BootstrapDeps{
			Provider:             resolvedProvider,
			ParentReg:            deps.ParentReg,
			SubAgentCfg:          typedCfg,
			Events:               deps.Events,
			WorkDir:              deps.WorkDir,
			HomeDir:              deps.HomeDir,
			ProjectContextConfig: deps.ProjectContextConfig,
			ResolvedModel:        resolvedModel,
			MaxTokens:            deps.MaxTokens,
			StreamingPreferred:   deps.StreamingPreferred,
			CaveHuman:            deps.CaveHuman,
			Sandbox:              deps.Sandbox,
			UsageRecorder:        deps.UsageRecorder,
		}, spec)
		if err != nil {
			return nil, fmt.Errorf("%s: build child run: %w", agentType, err)
		}
		spec.Limits = limits

		result, state, err := SpawnDelegate(ctx, spec, req, deps.Runner, deps.Events, deps.TraceLogger)
		if err == nil && deps.SessionStore != nil {
			saveChildSession(deps.SessionStore, spec, req, state)
		}
		if err != nil {
			if result != (tool.ExecutionResult{}) {
				return result, nil
			}
			return nil, fmt.Errorf("%s failed: %w", agentType, err)
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
