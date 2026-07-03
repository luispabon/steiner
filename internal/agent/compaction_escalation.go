package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

type compactionEscalation struct {
	Severity        string
	SessionState    string
	RestartGuidance string
}

func needsEmergencyCompaction(fit prompt.RequestTokenBudget) bool {
	if fit.ContextSize <= 0 {
		return false
	}
	return fit.PromptUsage >= compactionUsageThreshold
}

func emergencyCompactionError(fit prompt.RequestTokenBudget) error {
	threshold := fit.CompactionThreshold
	if threshold <= 0 {
		threshold = compactionUsageThreshold
	}
	return fmt.Errorf(
		"emergency compaction could not reduce context enough: prompt=%d context_window=%d usage=%.0f%% compaction_threshold=%.0f%%",
		fit.EstimatedPromptTokens,
		fit.ContextSize,
		fit.PromptUsage*100,
		threshold*100,
	)
}

func compactionCannotSolveError(fit prompt.RequestTokenBudget) error {
	return fmt.Errorf(
		"compaction cannot solve this request: retained conversation already exceeds the hard context limit: prompt=%d context_window=%d usage=%.0f%% hard_limit=%d",
		fit.EstimatedPromptTokens,
		fit.ContextSize,
		fit.PromptUsage*100,
		fit.HardLimitTokens,
	)
}

func recordCompactionSummary(current ContextState, summary string, candidate ConversationCandidate, turn int) ContextState {
	next := current.Clone()
	next.RetainedSummaries = appendRetainedSummary(next.RetainedSummaries, compactionRetainedSummary(summary, candidate, turn))
	return next
}

func compactionRetainedSummary(summary string, candidate ConversationCandidate, turn int) RetainedSummary {
	title, text := parseCompactionRetainedSummary(summary)
	return RetainedSummary{
		Title:  title,
		Text:   text,
		Source: fmt.Sprintf("compaction:%d/%s", candidate.GenerationID, candidate.View),
		Turn:   turn,
	}
}

func parseCompactionRetainedSummary(summary string) (string, string) {
	title := "compacted conversation history"
	text := summary
	var envelope struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(summary), &envelope); err == nil {
		if strings.TrimSpace(envelope.Title) != "" {
			title = envelope.Title
		}
		if strings.TrimSpace(envelope.Content) != "" {
			text = envelope.Content
		}
	}
	return title, text
}

func fallbackCompactionSummary(candidate ConversationCandidate) string {
	if len(candidate.Messages) == 0 {
		return ""
	}

	sections := []string{
		"# Request intent",
		"- " + summarizeTextPreview(firstMessageContentByRole(candidate.Messages, MessageRoleUser), 160),
		"# Solution design",
		"- " + summarizeTextPreview(firstMessageContentByRole(candidate.Messages, MessageRoleAssistant), 160),
		"# Recent actions",
		"- " + summarizeConversationMessages(candidate.Messages, 3),
		"# Unresolved decisions",
		"- none recorded",
		"# Pending work",
		"- continue from the retained conversation",
	}
	return strings.Join(sections, "\n")
}

func appendRetainedSummary(existing []RetainedSummary, summary RetainedSummary) []RetainedSummary {
	if strings.TrimSpace(summary.Text) == "" {
		return cloneRetainedSummaries(existing)
	}
	next := cloneRetainedSummaries(existing)
	if len(next) > 0 {
		last := next[len(next)-1]
		if retainedSummariesEqual(last, summary) {
			next[len(next)-1] = summary
			return next
		}
	}
	return append(next, summary)
}

func retainedSummariesEqual(a, b RetainedSummary) bool {
	return a.Title == b.Title &&
		a.Text == b.Text &&
		a.Source == b.Source &&
		a.Turn == b.Turn
}

func compactionEscalationForFit(compactionCount int, fit prompt.RequestTokenBudget) compactionEscalation {
	fragile := compactionBudgetIsFragile(fit)
	switch {
	case compactionCount >= compactionCriticalThreshold:
		return compactionEscalation{
			Severity:        "critical",
			SessionState:    "likely_lossy",
			RestartGuidance: compactionRestartGuidanceCritical,
		}
	case compactionCount >= compactionWarningThreshold:
		if fragile {
			return compactionEscalation{
				Severity:        "critical",
				SessionState:    "likely_lossy",
				RestartGuidance: compactionRestartGuidanceCritical,
			}
		}
		return compactionEscalation{
			Severity:        "warning",
			SessionState:    "fragile",
			RestartGuidance: compactionRestartGuidanceWarn,
		}
	default:
		if fragile {
			return compactionEscalation{
				Severity:        "warning",
				SessionState:    "fragile",
				RestartGuidance: compactionRestartGuidanceWarn,
			}
		}
		return compactionEscalation{
			Severity:        "info",
			SessionState:    "stable",
			RestartGuidance: compactionRestartGuidanceStable,
		}
	}
}

func compactionBudgetIsFragile(fit prompt.RequestTokenBudget) bool {
	limit := fit.HardLimitTokens
	if limit <= 0 {
		limit = fit.ContextSize
	}
	promptTokens := fit.EstimatedPromptTokens
	if promptTokens <= 0 {
		promptTokens = fit.TotalTokens
	}
	if limit <= 0 || promptTokens <= 0 {
		return false
	}
	overage := promptTokens - limit
	if overage <= 0 {
		return false
	}
	return overage*100 >= limit*compactionFragilityOveragePercent
}

func compactionSummaryText(content string, candidate ConversationCandidate) string {
	summaryText := strings.TrimSpace(content)
	if summaryText != "" {
		return summaryText
	}
	return fallbackCompactionSummary(candidate)
}

func buildSummarizedCompactionState(state RunState, summaryText string, candidate ConversationCandidate, turn int, retained []Message) RunState {
	summaryPrefix := []Message{{Role: MessageRoleSummary, Content: summaryText}}
	nextLineage := state.Lineage.WithNewGeneration(summaryPrefix, retained)
	nextState := state.Clone()
	nextState.Lineage = nextLineage
	nextState.Conversation = nextLineage.FullMessages()
	nextState.Context = recordCompactionSummary(nextState.Context, summaryText, candidate, turn)
	return nextState
}

func buildCompactionRequestWithMode(ctx context.Context, req RunRequest, state RunState, candidate ConversationCandidate, mode prompt.CompactionMode, maxTokens int) (provider.ChatRequest, string, error) {
	basePrompt := prepareBasePrompt(req)
	sourceState := state.WithConversation(candidate.Messages)
	assembly, err := prompt.Assemble(ctx, assemblyOptions(basePrompt, sourceState))
	if err != nil {
		return provider.ChatRequest{}, "", err
	}
	messages := append(provider.CloneMessages(assembly.Messages), provider.Message{
		Role:    provider.MessageRoleUser,
		Content: prompt.RenderConversationCompactionInstruction(basePrompt.PromptOverrides.Compaction, mode, basePrompt.CaveHuman),
	})
	// Mirror the normal-turn request shape (same Tools and Params) so the
	// compaction call replays the identical cached prefix (system + tools +
	// conversation) and hits the prompt cache. Omitting Tools also let any
	// `tools` carried in ExtraParams leak through unfiltered, since the wire
	// layer only sets (and thus overrides) the tools key when Tools is non-empty.
	request := provider.ChatRequest{
		Model:       req.ResolvedModel.BackendModelID,
		Messages:    messages,
		Tools:       provider.CloneTools(req.Tools),
		Params:      req.ResolvedModel.Params,
		ExtraParams: req.ResolvedModel.ExtraParams,
		MaxTokens:   compactionMaxTokensForMode(maxTokens),
	}
	request = applyPromptSuffix(req.ResolvedModel.PromptSuffix, request)
	request.IncludeEmptyReasoning = req.ResolvedModel.ReasoningEchoBack
	if !req.ResolvedModel.ReasoningEchoBack {
		stripReasoningContent(request.Messages)
	}
	return request, fmt.Sprintf("%s mode=%s", summarizeCompactionPrompt(candidate), mode), nil
}

func completeCompactionCall(ctx context.Context, req RunRequest, turn int, chatRequest provider.ChatRequest, budget prompt.ModelTokenBudget) (provider.ChatResponse, error) {
	var logger *CompactionLogger
	if req.CompactionLogPath != "" {
		var err error
		logger, err = NewCompactionLogger(req.CompactionLogPath)
		if err == nil {
			_ = logger.LogRequest(chatRequest) // best effort
			defer func() { _ = logger.Close() }()
		}
	}
	response, _, err := executeChatRequest(ctx, req.Provider, turn, chatRequest, budget, req.Events, nil, true, false, output.ChunkSourceAssistant, nil)
	if logger != nil {
		_ = logger.LogResponse(response) // best effort
	}
	if err == nil {
		recordModelUsage(req, response.Usage)
	}
	return response, err
}

func summarizeCompactionPrompt(candidate ConversationCandidate) string {
	return fmt.Sprintf("generation=%d view=%s messages=%d", candidate.GenerationID, candidate.View, len(candidate.Messages))
}

func compactionMaxTokensForMode(maxTokens int) *int {
	if maxTokens <= 0 {
		return nil
	}
	return &maxTokens
}

func compactionSummaryMaxTokensForMode(budget prompt.ModelTokenBudget, mode prompt.CompactionMode) int {
	switch mode {
	case prompt.CompactionModeEmergency:
		if budget.EmergencySummaryMaxTokens > 0 {
			return budget.EmergencySummaryMaxTokens
		}
	default:
		if budget.NormalSummaryMaxTokens > 0 {
			return budget.NormalSummaryMaxTokens
		}
	}
	if budget.SummaryMaxTokens > 0 {
		return budget.SummaryMaxTokens
	}
	if budget.MaxCompletionTokens > 0 {
		return budget.MaxCompletionTokens
	}
	return 0
}

// stripImages removes image attachments from messages and replaces them with a
// marker text. This prevents images from being sent to the compaction model
// while preserving a record that images were present.
func stripImages(messages []Message) []Message {
	result := make([]Message, len(messages))
	for i, msg := range messages {
		if len(msg.Images) > 0 {
			msg.Images = nil
			if msg.Content != "" {
				msg.Content += "\n[image was attached]"
			} else {
				msg.Content = "[image was attached]"
			}
		}
		result[i] = msg
	}
	return result
}
