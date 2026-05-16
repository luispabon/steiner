package main

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type runnerSetup struct {
	selected    config.ModelConfig
	provider    provider.Provider
	modelBudget prompt.ModelTokenBudget
	assembly    prompt.AssemblyOptions
	runMode     string
}

func (r cliRunner) prepareRun(conversation []agent.Message, skillNames []string) (runnerSetup, error) {
	selected := r.selectedModel()
	prov, err := r.runtimeProvider(selected)
	if err != nil {
		return runnerSetup{}, err
	}
	modelBudget := prompt.ModelTokenBudget{
		ContextSize:         selected.Advanced.Limits.ContextWindow,
		MaxCompletionTokens: selected.Advanced.Limits.MaxOutputTokens,
		SafetyMarginTokens:  selected.Advanced.Limits.SafetyMarginTokens,
		SummaryMaxTokens:    selected.Advanced.Limits.SummaryMaxTokens,
	}

	return runnerSetup{
		selected:    selected,
		provider:    loggingProvider{inner: prov, sink: r.runtime.events},
		modelBudget: modelBudget,
		assembly:    r.promptAssembly(conversation, skillNames, modelBudget, selected),
		runMode:     r.normalizedRunMode(),
	}, nil
}

func (r cliRunner) selectedModel() config.ModelConfig {
	selected := selectedModelConfig(r.runtime.cfg)
	if r.currentModel != nil {
		selected = r.currentModel()
	}
	return selected
}

func (r cliRunner) runtimeProvider(selected config.ModelConfig) (provider.Provider, error) {
	prov := r.runtime.provider
	if r.runtime.providerFactory != nil {
		var err error
		prov, err = r.runtime.providerFactory(selected)
		if err != nil {
			return nil, err
		}
	}
	if prov == nil {
		return nil, fmt.Errorf("provider is required")
	}
	return prov, nil
}

func (r cliRunner) promptAssembly(conversation []agent.Message, skillNames []string, modelBudget prompt.ModelTokenBudget, selected config.ModelConfig) prompt.AssemblyOptions {
	return prompt.AssemblyOptions{
		HomeDir:                   r.runtime.homeDir,
		ProjectRoot:               r.runtime.workDir,
		SkillsRoot:                prompt.DefaultSkillsRoot(r.runtime.homeDir),
		SkillNames:                append([]string(nil), skillNames...),
		ModelBudget:               modelBudget,
		PromptOverrides:           selected.Prompts,
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
	maxTokens := setup.selected.Advanced.Limits.MaxOutputTokens
	return agent.RunRequest{
		Provider:    setup.provider,
		Executor:    tool.NewExecutor(activeRegistry, r.runtime.cfg, r.approver, r.runtime.workDir),
		Tools:       activeRegistry.ToProviderSpecs(),
		Prompt:      setup.assembly,
		ModelBudget: setup.modelBudget,
		Model:       setup.selected.ID,
		ExtraParams: setup.selected.ExtraParams,
		MaxTokens:   &maxTokens,
		Limits: agent.Limits{
			MaxTurns:  r.maxTurns,
			MaxTokens: r.runtime.cfg.Limits.MaxTokens,
		},
		Events:                    events,
		ContextManager:            agent.NewContextManager(string(r.runtime.cfg.ContextManagement.Mode), r.runtime.cfg.ContextManagement),
		ThinkingEnabled:           setup.selected.ThinkingEnabled,
		ThinkingDisableMarker:     setup.selected.ThinkingDisableMarker,
		ThinkingScaffoldInference: setup.selected.ThinkingScaffoldInference,
		ThinkingParams:            setup.selected.ThinkingParams,
		StreamingPreferred:        r.streamingPreferred,
	}
}
