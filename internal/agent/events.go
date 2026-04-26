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
	notes := []string{
		fmt.Sprintf("prompt=%d", fit.EstimatedPromptTokens),
		fmt.Sprintf("reserve=%d", fit.ReservedCompletionTokens),
		fmt.Sprintf("safety=%d", fit.SafetyMarginTokens),
	}
	if truncated {
		notes = append(notes, fmt.Sprintf("request exceeds context window: %s", fit.String()))
	}
	emitEvent(sink, output.NewContextTokenBudgetEvent(
		string(prompt.ContextSourceConversation),
		turn,
		fit.EstimatedPromptTokens,
		fit.ReservedCompletionTokens,
		fit.SafetyMarginTokens,
		fit.TotalTokens,
		fit.ContextSize,
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

func emitCompactionDiagnostics(sink output.EventSink, turn, compactionCount int, fit prompt.RequestTokenBudget, retainedMessages []Message, candidate ConversationCandidate, summaryText, promptText string) {
	if sink == nil {
		return
	}

	escalation := compactionEscalationForFit(compactionCount, fit)
	notes := []string{
		fmt.Sprintf("source generation=%d view=%s", candidate.GenerationID, candidate.View),
		fmt.Sprintf("prompt=%d", fit.EstimatedPromptTokens),
		fmt.Sprintf("reserve=%d", fit.ReservedCompletionTokens),
		fmt.Sprintf("safety=%d", fit.SafetyMarginTokens),
	}
	if promptText != "" {
		notes = append(notes, "prompt="+promptText)
	}
	emitEvent(sink, output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:               "compaction",
		Scope:              "conversation",
		Turn:               turn,
		Severity:           escalation.Severity,
		SessionState:       escalation.SessionState,
		CompactionCount:    compactionCount,
		RestartGuidance:    escalation.RestartGuidance,
		CompactedTurns:     len(candidate.Messages),
		CompactedMessages:  countMessages(candidate.Messages),
		RetainedTurns:      countTurns(retainedMessages),
		RetainedMessages:   countMessages(retainedMessages),
		SummaryTitle:       "compacted conversation history",
		SummaryPreview:     summarizeTextPreview(summaryText, 120),
		SummaryBytes:       len(summaryText),
		PromptTokens:       fit.EstimatedPromptTokens,
		ReservedTokens:     fit.ReservedCompletionTokens,
		SafetyMarginTokens: fit.SafetyMarginTokens,
		ContextTokens:      fit.ContextSize,
		TotalTokens:        fit.TotalTokens,
		Truncated:          fit.TotalTokens > fit.ContextSize && fit.ContextSize > 0,
		Notes:              notes,
	}))
	emitEvent(sink, output.NewContextSessionHealthEvent("conversation", turn, compactionCount, escalation.Severity, escalation.SessionState, escalation.RestartGuidance, notes...))
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
	for _, diagnostic := range assembly.Diagnostics {
		if !diagnostic.Truncated {
			continue
		}

		notes := make([]string, 0, 1)
		if diagnostic.Path != "" {
			notes = append(notes, "path="+diagnostic.Path)
		}

		emitEvent(sink, output.NewContextBudgetEvent(
			string(diagnostic.Source),
			turn,
			diagnostic.ByteSize,
			budgetForSource(budgets, diagnostic.Source),
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
	if budgets.DurableContextBytes == 0 {
		budgets.DurableContextBytes = defaults.DurableContextBytes
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
	case prompt.ContextSourceDurableContext:
		return budgets.DurableContextBytes
	case prompt.ContextSourceToolResult:
		return budgets.ToolResultBytes
	case prompt.ContextSourceToolSummary, prompt.ContextSourceDelegationResult:
		return budgets.ToolSummaryBytes
	default:
		return 0
	}
}
