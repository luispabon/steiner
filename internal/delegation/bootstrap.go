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
// It derives final limits by combining SubAgentConfig defaults with spec-level
// overrides, then delegates internal assembly to existing helpers. Returns the
// assembled request and the computed DelegationLimits.
func BuildChildRun(ctx context.Context, deps BootstrapDeps, spec DelegationSpec) (agent.RunRequest, DelegationLimits, error) {
	limits := deriveChildLimits(deps.SubAgentCfg, spec.Limits)
	agentLimits := agent.Limits{
		MaxTurns:  limits.MaxTurns,
		MaxTokens: limits.OutputLimitTokens,
	}
	childReg := BuildChildToolRegistry(deps.ParentReg, delegateToolName)
	req := buildChildRunRequest(spec, deps.Provider, childReg, agentLimits, deps.Events)
	return req, limits, nil
}

// deriveChildLimits combines SubAgentConfig defaults with overrides from the
// spec using tighten-only semantics. The returned Limits have all unset override
// fields filled from configuration defaults.
func deriveChildLimits(cfg config.SubAgentConfig, overrides DelegationLimits) DelegationLimits {
	base := DefaultLimits(cfg)
	return ApplyOverrides(base, overrides)
}
