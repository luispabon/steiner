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
// sandbox is the parent's SandboxWrapper; if non-nil it is applied to the child
// executor unchanged, enforcing child sandbox ≤ parent sandbox.
func buildChildRunRequest(workDir string, spec DelegationSpec, prov provider.Provider, visibleReg *tool.Registry, execReg *tool.Registry, baseLimits agent.Limits, events output.EventSink, promptOpts prompt.AssemblyOptions, rm provider.ResolvedModel, modelBudget prompt.ModelTokenBudget, maxTokens *int, streamingPreferred bool, caveHuman bool, sandbox tool.SandboxWrapper) agent.RunRequest {
	_ = spec
	childCfg := config.Config{}
	scopedEvents := withAgentScope(spec.AgentID, events)

	exec := tool.NewExecutor(execReg, childCfg, nil, workDir)
	if sandbox != nil {
		exec = exec.WithSandbox(sandbox)
	}

	req := agent.RunRequest{
		Provider:           prov,
		Executor:           exec,
		Tools:              visibleReg.ToProviderSpecs(),
		Limits:             baseLimits,
		Events:             scopedEvents,
		Prompt:             promptOpts,
		ResolvedModel:      rm,
		ModelBudget:        modelBudget,
		MaxTokens:          maxTokens,
		StreamingPreferred: streamingPreferred,
		CaveHuman:          caveHuman,
	}

	return req
}
