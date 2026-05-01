package interactive

import (
	"context"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

func selectedModelConfig(cfg config.Config) (config.ModelConfig, error) {
	return cfg.Model, nil
}

func (s *Session) manualCompaction(ctx context.Context) {
	s.mu.RLock()
	conversation := s.conversation
	s.mu.RUnlock()

	if len(conversation) == 0 {
		s.events.Emit(output.NewContextReportEvent("No conversation to compact."))
		return
	}

	cfg := s.deps.Config
	selected, err := selectedModelConfig(cfg)
	if err != nil {
		s.emitCompactError(err)
		return
	}

	prov := s.deps.Provider
	if prov == nil && s.deps.ProviderFactory != nil {
		prov, err = s.deps.ProviderFactory(selected)
		if err != nil {
			s.emitCompactError(err)
			return
		}
	}
	if prov == nil {
		s.emitCompactError(fmt.Errorf("no provider available for compaction"))
		return
	}

	modelBudget := prompt.ModelTokenBudget{
		ContextSize:         selected.ContextSize,
		MaxCompletionTokens: selected.MaxCompletionTokens,
		SafetyMarginTokens:  selected.Compaction.SafetyMarginTokens,
		SummaryMaxTokens:    selected.Compaction.SummaryMaxTokens,
	}
	assembly := prompt.AssemblyOptions{
		HomeDir:         s.deps.HomeDir,
		ProjectRoot:     s.deps.WorkDir,
		SkillsRoot:      prompt.DefaultSkillsRoot(s.deps.HomeDir),
		ModelBudget:     modelBudget,
		PromptOverrides: selected.Prompts,
	}

	compactReq := agent.RunRequest{
		Provider:    prov,
		Prompt:      assembly,
		ModelBudget: modelBudget,
		Model:       selected.Model,
		Events:      s.events,
	}

	newConv, err := s.runManualCompaction(ctx, selected.Model, func(runCtx context.Context) ([]agent.Message, error) {
		agentRunner := agent.NewRunner()
		return agentRunner.Compact(runCtx, compactReq, conversation)
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.emitCompactError(err)
		}
		return
	}

	s.events.Emit(output.NewContextReportEvent("Compaction triggered manually."))
	s.SetConversation(newConv)
}

func (s *Session) runManualCompaction(ctx context.Context, model string, run func(context.Context) ([]agent.Message, error)) (result []agent.Message, err error) {
	runCtx, cancel := context.WithCancel(ctx)
	s.runController.Set(cancel)
	defer func() {
		cancel()
		s.runController.Clear()

		reason := "complete"
		if err != nil {
			if errors.Is(err, context.Canceled) {
				reason = "cancelled"
			} else {
				reason = "error"
			}
		}
		s.events.Emit(output.NewRunFinishedEvent(0, reason, "", "", err))
	}()

	s.events.Emit(output.NewRunStartedEvent("interactive", model, "", 0, 0))
	s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:     "compaction",
		Scope:    "conversation",
		Severity: "compacting",
		Notes:    []string{"starting compaction"},
	}))

	return run(runCtx)
}

func (s *Session) emitCompactError(err error) {
	s.events.Emit(output.Event{
		Type:    output.EventTypeStopReason,
		Payload: output.StopReasonEvent{Reason: fmt.Sprintf("Compaction error: %v", err)},
	})
}
