package agent

import (
	"fmt"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func emitEvent(sink output.EventSink, event output.Event) {
	if sink != nil {
		sink.Emit(event)
	}
}

func emitStop(sink output.EventSink, state RunState, err error) {
	emitEvent(sink, output.NewStopReasonEvent(state.TurnCount, string(state.StopReason), err))
}

func emitRequestTokenDiagnostic(sink output.EventSink, turn int, fit prompt.RequestTokenBudget, truncated bool) {
	if sink == nil {
		return
	}
	status := requestTokenBudgetStatus(fit)
	notes := []string{
		fmt.Sprintf("prompt_tokens=%d", fit.EstimatedPromptTokens),
		fmt.Sprintf("context_usage_percent=%.0f%%", fit.PromptUsage*100),
		fmt.Sprintf("compaction_threshold=%.0f%%", fit.CompactionThreshold*100),
		fmt.Sprintf("estimator_pad_tokens=%d", fit.SafetyMarginTokens),
		fmt.Sprintf("status=%s", status),
	}
	if truncated {
		notes = append(notes, fmt.Sprintf("request exceeds context window: %s", fit.String()))
	}
	emitEvent(sink, output.NewContextTokenBudgetEvent(
		string(prompt.ContextSourceConversation),
		turn,
		fit.EstimatedPromptTokens,
		fit.RawEstimatedPromptTokens,
		fit.ContextSize,
		fit.PromptUsage*100,
		fit.CompactionThreshold*100,
		fit.SafetyMarginTokens,
		fit.TotalTokens,
		status,
		truncated,
		notes...,
	))
}

func emitCompactionStartedEvent(sink output.EventSink, turn int) {
	if sink == nil {
		return
	}
	emitEvent(sink, output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:     "compaction",
		Scope:    "conversation",
		Turn:     turn,
		Severity: "compacting",
		Notes:    []string{"starting compaction"},
	}))
	emitEvent(sink, output.NewContextSessionHealthEvent("conversation", turn, 0, "compacting", "compacting", "compacting in progress", "starting compaction"))
}

type compactionDiagnosticsParams struct {
	turn               int
	compactionCount    int
	beforeFit          prompt.RequestTokenBudget
	afterFit           prompt.RequestTokenBudget
	mode               prompt.CompactionMode
	summaryTokenBudget int
	retainedMessages   []Message
	candidate          ConversationCandidate
	summaryText        string
	promptText         string
	usage              *provider.UsageStats
}

func emitCompactionDiagnostics(sink output.EventSink, params compactionDiagnosticsParams) {
	if sink == nil {
		return
	}
	var cacheReadTokens, inputTokens, cacheCreateTokens int
	if params.usage != nil {
		cacheReadTokens = params.usage.CacheReadInputTokens
		inputTokens = params.usage.NonCachedPromptTokens()
		cacheCreateTokens = params.usage.CacheCreationInputTokens
	}

	escalation := compactionEscalationForFit(params.compactionCount, params.afterFit)
	notes := []string{
		fmt.Sprintf("source generation=%d view=%s", params.candidate.GenerationID, params.candidate.View),
		fmt.Sprintf("mode=%s", params.mode),
		fmt.Sprintf("before prompt_tokens=%d context_usage_percent=%.0f%%", params.beforeFit.EstimatedPromptTokens, params.beforeFit.PromptUsage*100),
		fmt.Sprintf("after prompt_tokens=%d context_usage_percent=%.0f%%", params.afterFit.EstimatedPromptTokens, params.afterFit.PromptUsage*100),
		fmt.Sprintf("retained_raw_turns=%d", countTurns(params.retainedMessages)),
		fmt.Sprintf("summary_token_budget=%d", params.summaryTokenBudget),
		fmt.Sprintf("threshold_achieved=%t", compactionThresholdAchieved(params.afterFit)),
	}
	if params.promptText != "" {
		notes = append(notes, "prompt="+params.promptText)
	}
	emitEvent(sink, output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:                  "compaction",
		Scope:                 "conversation",
		Turn:                  params.turn,
		Severity:              escalation.Severity,
		SessionState:          escalation.SessionState,
		CompactionCount:       params.compactionCount,
		RestartGuidance:       escalation.RestartGuidance,
		CompactedTurns:        len(params.candidate.Messages),
		CompactedMessages:     len(params.candidate.Messages),
		RetainedTurns:         countTurns(params.retainedMessages),
		RetainedMessages:      len(params.retainedMessages),
		SummaryTitle:          "compacted conversation history",
		SummaryPreview:        summarizeTextPreview(params.summaryText, 120),
		SummaryText:           params.summaryText,
		SummaryBytes:          len(params.summaryText),
		CacheReadTokens:       cacheReadTokens,
		InputTokens:           inputTokens,
		CacheCreateTokens:     cacheCreateTokens,
		Mode:                  string(params.mode),
		BeforePromptTokens:    params.beforeFit.EstimatedPromptTokens,
		BeforeRawPromptTokens: params.beforeFit.RawEstimatedPromptTokens,
		BeforeUsagePercent:    params.beforeFit.PromptUsage * 100,
		AfterPromptTokens:     params.afterFit.EstimatedPromptTokens,
		AfterRawPromptTokens:  params.afterFit.RawEstimatedPromptTokens,
		AfterUsagePercent:     params.afterFit.PromptUsage * 100,
		RetainedRawTurns:      countTurns(params.retainedMessages),
		SummaryTokenBudget:    params.summaryTokenBudget,
		ThresholdAchieved:     compactionThresholdAchieved(params.afterFit),
		PromptTokens:          params.afterFit.EstimatedPromptTokens,
		RawPromptTokens:       params.afterFit.RawEstimatedPromptTokens,
		ContextWindow:         params.afterFit.ContextSize,
		ContextUsagePercent:   params.afterFit.PromptUsage * 100,
		CompactionThreshold:   params.afterFit.CompactionThreshold * 100,
		EstimatorPadTokens:    params.afterFit.SafetyMarginTokens,
		Status:                requestTokenBudgetStatus(params.afterFit),
		Truncated:             params.afterFit.ContextSize > 0 && params.afterFit.TotalTokens > params.afterFit.ContextSize,
		Notes:                 notes,
	}))
	emitEvent(sink, output.NewContextSessionHealthEvent("conversation", params.turn, params.compactionCount, escalation.Severity, escalation.SessionState, escalation.RestartGuidance, notes...))
}

func requestTokenBudgetStatus(fit prompt.RequestTokenBudget) string {
	switch {
	case fit.ContextSize <= 0:
		return "unknown_context"
	case fit.EstimatedPromptTokens > fit.HardLimitTokens:
		return "hard_limit"
	case fit.ShouldCompact:
		return "compact"
	default:
		return "ok"
	}
}

func compactionThresholdAchieved(fit prompt.RequestTokenBudget) bool {
	if fit.ContextSize <= 0 {
		return false
	}
	return fit.PromptUsage <= fit.CompactionThreshold
}

func emitAssemblyDiagnostics(sink output.EventSink, opts prompt.AssemblyOptions, turn int, assembly prompt.Assembly) {
	if sink == nil {
		return
	}

	budgets := diagnosticBudgets(opts)
	for _, block := range assembly.Blocks {
		if !block.Truncated {
			continue
		}

		notes := make([]string, 0, 2)
		if block.Path != "" {
			notes = append(notes, "path="+block.Path)
		}

		emitEvent(sink, output.NewContextBudgetEvent(
			string(block.Source),
			turn,
			block.ByteSize,
			budgetForSource(budgets, block.Source),
			true,
			notes...,
		))
	}
}

func diagnosticBudgets(opts prompt.AssemblyOptions) prompt.SourceBudgetModel {
	defaults := prompt.DefaultAssemblyPolicy().Budgets
	budgets := opts.Policy.Budgets

	if opts.ProjectContextBudgetBytes > 0 {
		budgets.ProjectContextBytes = opts.ProjectContextBudgetBytes
	} else if budgets.ProjectContextBytes == 0 {
		budgets.ProjectContextBytes = defaults.ProjectContextBytes
	}
	if budgets.SkillBytes == 0 {
		budgets.SkillBytes = defaults.SkillBytes
	}

	return budgets
}

func budgetForSource(budgets prompt.SourceBudgetModel, source prompt.ContextSource) int {
	switch source {
	case prompt.ContextSourceProjectContext:
		return budgets.ProjectContextBytes
	case prompt.ContextSourceSkill:
		return budgets.SkillBytes
	default:
		return 0
	}
}
