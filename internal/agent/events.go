package agent

import (
	"fmt"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
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

func emitCompactionDiagnostics(sink output.EventSink, turn, compactionCount int, beforeFit, afterFit prompt.RequestTokenBudget, mode prompt.CompactionMode, summaryTokenBudget int, retainedMessages []Message, candidate ConversationCandidate, summaryText, promptText string) {
	if sink == nil {
		return
	}

	escalation := compactionEscalationForFit(compactionCount, afterFit)
	notes := []string{
		fmt.Sprintf("source generation=%d view=%s", candidate.GenerationID, candidate.View),
		fmt.Sprintf("mode=%s", mode),
		fmt.Sprintf("before prompt_tokens=%d context_usage_percent=%.0f%%", beforeFit.EstimatedPromptTokens, beforeFit.PromptUsage*100),
		fmt.Sprintf("after prompt_tokens=%d context_usage_percent=%.0f%%", afterFit.EstimatedPromptTokens, afterFit.PromptUsage*100),
		fmt.Sprintf("retained_raw_turns=%d", countTurns(retainedMessages)),
		fmt.Sprintf("summary_token_budget=%d", summaryTokenBudget),
		fmt.Sprintf("threshold_achieved=%t", compactionThresholdAchieved(afterFit)),
	}
	if promptText != "" {
		notes = append(notes, "prompt="+promptText)
	}
	emitEvent(sink, output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:                "compaction",
		Scope:               "conversation",
		Turn:                turn,
		Severity:            escalation.Severity,
		SessionState:        escalation.SessionState,
		CompactionCount:     compactionCount,
		RestartGuidance:     escalation.RestartGuidance,
		CompactedTurns:      len(candidate.Messages),
		CompactedMessages:   len(candidate.Messages),
		RetainedTurns:       countTurns(retainedMessages),
		RetainedMessages:    len(retainedMessages),
		SummaryTitle:        "compacted conversation history",
		SummaryPreview:      summarizeTextPreview(summaryText, 120),
		SummaryBytes:        len(summaryText),
		Mode:                string(mode),
		BeforePromptTokens:  beforeFit.EstimatedPromptTokens,
		BeforeUsagePercent:  beforeFit.PromptUsage * 100,
		AfterPromptTokens:   afterFit.EstimatedPromptTokens,
		AfterUsagePercent:   afterFit.PromptUsage * 100,
		RetainedRawTurns:    countTurns(retainedMessages),
		SummaryTokenBudget:  summaryTokenBudget,
		ThresholdAchieved:   compactionThresholdAchieved(afterFit),
		PromptTokens:        afterFit.EstimatedPromptTokens,
		ContextWindow:       afterFit.ContextSize,
		ContextUsagePercent: afterFit.PromptUsage * 100,
		CompactionThreshold: afterFit.CompactionThreshold * 100,
		EstimatorPadTokens:  afterFit.SafetyMarginTokens,
		Status:              requestTokenBudgetStatus(afterFit),
		Truncated:           afterFit.ContextSize > 0 && afterFit.TotalTokens > afterFit.ContextSize,
		Notes:               notes,
	}))
	emitEvent(sink, output.NewContextSessionHealthEvent("conversation", turn, compactionCount, escalation.Severity, escalation.SessionState, escalation.RestartGuidance, notes...))
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
		if block.Source == prompt.ContextSourceConversationSummary {
			notes = append(notes, "compacted conversation history")
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

	if budgets.PreambleBytes == 0 {
		budgets.PreambleBytes = defaults.PreambleBytes
	}
	if budgets.GlobalAgentsBytes == 0 {
		budgets.GlobalAgentsBytes = defaults.GlobalAgentsBytes
	}
	if budgets.ProjectAgentsBytes == 0 {
		budgets.ProjectAgentsBytes = defaults.ProjectAgentsBytes
	}
	if opts.ProjectContextBudgetBytes > 0 {
		budgets.ProjectContextBytes = opts.ProjectContextBudgetBytes
	} else if budgets.ProjectContextBytes == 0 {
		budgets.ProjectContextBytes = defaults.ProjectContextBytes
	}
	if budgets.SkillBytes == 0 {
		budgets.SkillBytes = defaults.SkillBytes
	}
	if budgets.ToolResultBytes == 0 {
		budgets.ToolResultBytes = defaults.ToolResultBytes
	}
	if budgets.ToolSummaryBytes == 0 {
		budgets.ToolSummaryBytes = defaults.ToolSummaryBytes
	}

	return budgets
}

func budgetForSource(budgets prompt.SourceBudgetModel, source prompt.ContextSource) int {
	switch source {
	case prompt.ContextSourcePreamble:
		return budgets.PreambleBytes
	case prompt.ContextSourceGlobalAgentsMD:
		return budgets.GlobalAgentsBytes
	case prompt.ContextSourceProjectAgentsMD:
		return budgets.ProjectAgentsBytes
	case prompt.ContextSourceProjectContext:
		return budgets.ProjectContextBytes
	case prompt.ContextSourceSkill:
		return budgets.SkillBytes
	case prompt.ContextSourceToolResult:
		return budgets.ToolResultBytes
	case prompt.ContextSourceToolSummary, prompt.ContextSourceDelegationResult:
		return budgets.ToolSummaryBytes
	default:
		return 0
	}
}
