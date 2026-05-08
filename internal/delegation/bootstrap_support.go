package delegation

import (
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// buildChildToolRegistry creates a new tool registry from the parent registry,
// excluding the tool named delegateToolName.
func buildChildToolRegistry(parent *tool.Registry, delegateToolName string) *tool.Registry {
	if parent == nil {
		return tool.NewRegistry()
	}

	parentDefs := parent.Definitions()
	childDefs := make([]tool.ToolDef, 0, len(parentDefs))

	for _, def := range parentDefs {
		if def.Name != delegateToolName {
			childDefs = append(childDefs, def)
		}
	}

	return tool.NewRegistry(childDefs...)
}

// buildChildRunRequest assembles the agent.RunRequest for a child delegation.
// Registries and prompt must be provided pre-built; the caller (typically
// BuildChildRun) is responsible for registry and prompt assembly.
func buildChildRunRequest(workDir string, spec DelegationSpec, prov provider.Provider, visibleReg *tool.Registry, execReg *tool.Registry, baseLimits agent.Limits, events output.EventSink, promptOpts prompt.AssemblyOptions, extraParams map[string]any, thinking config.ThinkingConfig) agent.RunRequest {
	childCfg := config.Config{Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto}}

	req := agent.RunRequest{
		Provider:    prov,
		Executor:    tool.NewExecutor(execReg, childCfg, nil, workDir),
		Tools:       visibleReg.ToProviderSpecs(),
		Model:       spec.Model,
		Limits:      baseLimits,
		Events:      events,
		Prompt:      promptOpts,
		ExtraParams: extraParams,
		Thinking:    thinking,
	}

	return req
}
