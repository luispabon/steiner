package delegation

import (
	"context"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// BootstrapDeps holds the dependencies needed to assemble a child agent run request.
type BootstrapDeps struct {
	Provider    provider.Provider
	ParentReg   *tool.Registry
	SubAgentCfg config.SubAgentConfig
	Events      output.EventSink
}

// BuildChildRun assembles a complete agent.RunRequest for a delegated child agent.
// In this initial stage it delegates to existing helpers; future stages will
// consolidate prompt, limit, tool, and work-dir logic into this flow.
func BuildChildRun(ctx context.Context, deps BootstrapDeps, spec DelegationSpec) (agent.RunRequest, error) {
	agentLimits := agent.Limits{
		MaxTurns:  spec.Limits.MaxTurns,
		MaxTokens: spec.Limits.OutputLimitTokens,
	}
	childReg := BuildChildToolRegistry(deps.ParentReg, delegateToolName)
	req := buildChildRunRequest(spec, deps.Provider, childReg, agentLimits, deps.Events)
	return req, nil
}
