package main

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

var fallbackWarningModels sync.Map

type runnerSetup struct {
	resolvedModel provider.ResolvedModel
	provider      provider.Provider
	modelBudget   prompt.ModelTokenBudget
	assembly      prompt.AssemblyOptions
	runMode       string
}

func (r cliRunner) prepareRun(conversation []agent.Message, skillNames []string) (runnerSetup, error) {
	alias := r.selectedAlias()
	rm, err := provider.ResolveWithDiscovery(r.runtime.cfg, alias, r.runtime.httpClient)
	if err != nil {
		return runnerSetup{}, err
	}
	emitFallbackWarnings(r.runtime.status, rm)

	prov, err := r.runtimeProvider(rm)
	if err != nil {
		return runnerSetup{}, err
	}
	modelBudget := prompt.ModelTokenBudget{
		ContextSize:         rm.EffectiveLimits.ContextWindow,
		MaxCompletionTokens: rm.EffectiveLimits.MaxOutputTokens,
		SafetyMarginTokens:  rm.EffectiveLimits.EstimatorPadTokens,
		SummaryMaxTokens:    rm.EffectiveLimits.NormalSummaryMaxTokens,
	}

	return runnerSetup{
		resolvedModel: rm,
		provider:      loggingProvider{inner: prov, sink: r.runtime.events},
		modelBudget:   modelBudget,
		assembly:      r.promptAssembly(conversation, skillNames, modelBudget, rm.Prompts),
		runMode:       r.normalizedRunMode(),
	}, nil
}

func emitFallbackWarnings(stream *output.Stream, rm provider.ResolvedModel) {
	if rm.MetadataSource != "fallback" || len(rm.Warnings) == 0 {
		return
	}
	key := rm.Alias + "\x00" + rm.BackendModelID
	if _, loaded := fallbackWarningModels.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	for _, warn := range rm.Warnings {
		if stream != nil {
			stream.Printf("%s\n", warn)
			continue
		}
		fmt.Fprintf(os.Stderr, "%s\n", warn)
	}
}

func resetFallbackModelWarnings() {
	fallbackWarningModels.Range(func(key, _ any) bool {
		fallbackWarningModels.Delete(key)
		return true
	})
}

func (r cliRunner) selectedAlias() string {
	if r.currentAlias != nil {
		return r.currentAlias()
	}
	if r.currentModel != nil {
		// Reverse lookup: find the alias whose ModelConfig matches the one returned.
		selected := r.currentModel()
		for alias, mc := range r.runtime.cfg.Models {
			if mc.ID == selected.ID && mc.Provider == selected.Provider {
				return alias
			}
		}
	}
	return r.runtime.cfg.DefaultModel
}

func (r cliRunner) runtimeProvider(rm provider.ResolvedModel) (provider.Provider, error) {
	prov := r.runtime.provider
	if r.runtime.providerFactory != nil {
		var err error
		prov, err = r.runtime.providerFactory(rm)
		if err != nil {
			return nil, err
		}
	}
	if prov == nil {
		return nil, fmt.Errorf("provider is required")
	}
	return prov, nil
}

func (r cliRunner) promptAssembly(conversation []agent.Message, skillNames []string, modelBudget prompt.ModelTokenBudget, prompts config.ModelPrompts) prompt.AssemblyOptions {
	return prompt.AssemblyOptions{
		HomeDir:                   r.runtime.homeDir,
		ProjectRoot:               r.runtime.workDir,
		SkillsRoot:                prompt.DefaultSkillsRoot(r.runtime.homeDir),
		SkillNames:                append([]string(nil), skillNames...),
		ModelBudget:               modelBudget,
		PromptOverrides:           prompts,
		ProjectContextBudgetBytes: r.runtime.cfg.ProjectContext.MaxTokens,
		ProjectContextExtraFiles:  append([]string(nil), r.runtime.cfg.ProjectContext.ExtraFiles...),
		ProjectContextIgnoreFiles: append([]string(nil), r.runtime.cfg.ProjectContext.IgnoreFiles...),
		ScratchpadEnabled:         r.runtime.cfg.ContextManagement.ScratchpadMode == config.ScratchpadModeHybrid,
		DelegationEnabled:         r.runtime.cfg.SubAgent.Enabled,
		Conversation:              toProviderConversation(conversation),
	}
}

func (r cliRunner) normalizedRunMode() string {
	runMode := strings.TrimSpace(r.runMode)
	if runMode == "" {
		return "exec"
	}
	return runMode
}

func retainDiagnosticEvents(base output.EventSink) (output.EventSink, *[]output.Event) {
	diagnostics := make([]output.Event, 0, 4)
	events := output.NewMultiSink(
		base,
		output.SinkFunc(func(event output.Event) {
			if isRetainedDiagnosticEvent(event) {
				diagnostics = append(diagnostics, event)
			}
		}),
	)
	return events, &diagnostics
}

func buildRunRequest(r cliRunner, _ []agent.Message, setup runnerSetup, activeRegistry *tool.Registry, events output.EventSink) agent.RunRequest {
	maxTokens := setup.resolvedModel.EffectiveLimits.MaxOutputTokens
	return agent.RunRequest{
		Provider:      setup.provider,
		Executor:      tool.NewExecutor(activeRegistry, r.runtime.cfg, r.approver, r.runtime.workDir),
		Tools:         activeRegistry.ToProviderSpecs(),
		Prompt:        setup.assembly,
		ModelBudget:   setup.modelBudget,
		ResolvedModel: setup.resolvedModel,
		MaxTokens:     &maxTokens,
		Limits: agent.Limits{
			MaxTurns:  r.maxTurns,
			MaxTokens: r.runtime.cfg.Limits.MaxTokens,
		},
		Events:             events,
		ContextManager:     agent.NewContextManager(string(r.runtime.cfg.ContextManagement.Mode), r.runtime.cfg.ContextManagement),
		StreamingPreferred: r.streamingPreferred,
	}
}
