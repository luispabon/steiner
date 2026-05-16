package interactive

import (
	"context"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func (s *Session) manualCompaction(ctx context.Context) {
	s.mu.RLock()
	conversation := s.conversation
	s.mu.RUnlock()

	if len(conversation) == 0 {
		s.events.Emit(output.NewContextReportEvent("No conversation to compact."))
		return
	}

	rm, err := provider.Resolve(s.deps.Config, s.deps.Config.DefaultModel)
	if err != nil {
		s.emitCompactError(fmt.Errorf("resolve model: %w", err))
		return
	}

	prov := s.deps.Provider
	if prov == nil && s.deps.ProviderFactory != nil {
		prov, err = s.deps.ProviderFactory(rm)
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
		ContextSize:         rm.EffectiveLimits.ContextWindow,
		MaxCompletionTokens: rm.EffectiveLimits.MaxOutputTokens,
		SafetyMarginTokens:  rm.EffectiveLimits.EstimatorPadTokens,
		SummaryMaxTokens:    rm.EffectiveLimits.NormalSummaryMaxTokens,
	}
	assembly := prompt.AssemblyOptions{
		HomeDir:         s.deps.HomeDir,
		ProjectRoot:     s.deps.WorkDir,
		SkillsRoot:      prompt.DefaultSkillsRoot(s.deps.HomeDir),
		ModelBudget:     modelBudget,
		PromptOverrides: rm.Prompts,
	}

	compactReq := agent.RunRequest{
		Provider:      prov,
		Prompt:        assembly,
		ModelBudget:   modelBudget,
		ResolvedModel: rm,
		Events:        s.events,
	}

	newConv, err := s.runManualCompaction(ctx, rm.BackendModelID, func(runCtx context.Context) ([]agent.Message, error) {
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
