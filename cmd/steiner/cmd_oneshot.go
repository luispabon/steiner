package main

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/tool"
)

type phaseRunner struct {
	runner cliRunner
}

func newPhaseRunner(ctx context.Context, cmd *cobra.Command, flags *cliFlags, projectRoot, workDir, modelAlias string, approver tool.ApprovalResponder, advisorCfg config.AdvisorConfig, maxTurns int, runMode string, streamingPreferred bool) (oneshot.PhaseRunner, error) {
	runtime, err := buildRuntimeWithRoots(ctx, cmd, flags, projectRoot, workDir, modelAlias)
	if err != nil {
		return nil, err
	}
	phaseAdvisor := runtime.cfg.Advisor
	if advisorCfg.Model != "" {
		phaseAdvisor.Model = advisorCfg.Model
	}
	if advisorCfg.MaxUsesPerRun > 0 {
		phaseAdvisor.MaxUsesPerRun = advisorCfg.MaxUsesPerRun
	}
	if advisorCfg.MaxTokens != nil {
		value := *advisorCfg.MaxTokens
		phaseAdvisor.MaxTokens = &value
	}
	phaseAdvisor.Enabled = true
	runtime.cfg.Advisor = phaseAdvisor

	runner := cliRunner{
		runtime:            runtime,
		approver:           approver,
		maxTurns:           maxTurns,
		runMode:            runMode,
		streamingPreferred: streamingPreferred,
	}
	if alias := strings.TrimSpace(modelAlias); alias != "" {
		runner.currentAlias = func() string {
			return alias
		}
	}
	return phaseRunner{runner: runner}, nil
}

func (r phaseRunner) RunPhase(ctx context.Context, conversation []agent.Message, skillNames []string, steerCh <-chan string) (oneshot.RunResult, error) {
	defer closeRuntime(&r.runner.runtime)
	return r.runner.RunPhase(ctx, conversation, skillNames, steerCh)
}
