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

const (
	compactionWarningThreshold        = 2
	compactionCriticalThreshold       = 3
	compactionFragilityOveragePercent = 20
)

const (
	compactionRestartGuidanceStable   = "continue, but watch for another compaction"
	compactionRestartGuidanceWarn     = "restart soon in a fresh session; repeated compaction is making retention fragile"
	compactionRestartGuidanceCritical = "restart now in a new session; retained context is likely to be lossy"
)

type compactionEscalation struct {
	Severity        string
	SessionState    string
	RestartGuidance string
}

func (r *Runner) Compact(ctx context.Context, req RunRequest, currentConv []Message) ([]Message, error) {
	state := RunState{
		Conversation: currentConv,
		Lineage:      newConversationLineage(currentConv),
		Context:      fromPromptContext(req.Prompt.ContextState),
	}

	skipped := map[string]bool{}
	compactionCount := 0

	compacted, err := r.compactConversationForBudget(ctx, req, &state, 0, skipped, &compactionCount)
	if err != nil {
		return nil, err
	}
	if !compacted {
		return currentConv, nil
	}

	return state.Conversation, nil
}

func (r *Runner) compactConversationForBudget(ctx context.Context, req RunRequest, state *RunState, turn int, skipped map[string]bool, compactionCount *int) (bool, error) {
	candidate, ok := selectCompactionCandidate(state.Lineage, skipped)
	if !ok {
		return false, nil
	}

	compactionRequest, promptText := buildCompactionRequest(req, *state, candidate)
	fit, err := req.ModelBudget.FitCompactionRequest(ctx, compactionRequest)
	if err != nil {
		return false, err
	}
	if !fit.Fits {
		skipped[compactionCandidateKey(candidate)] = true
		return true, nil
	}

	response, err := completeCompactionCall(ctx, req, turn, compactionRequest, req.ModelBudget)
	if err != nil {
		return false, err
	}

	summaryText := strings.TrimSpace(response.Message.Content)
	if summaryText == "" {
		summaryText = fallbackCompactionSummary(candidate)
	}
	if summaryText == "" {
		skipped[compactionCandidateKey(candidate)] = true
		return true, nil
	}

	retainedMessages := retainedMessagesForCandidate(state.Lineage, candidate)
	summaryPrefix := []Message{{Role: MessageRoleSummary, Content: summaryText}}
	nextLineage := state.Lineage.WithNewGeneration(summaryPrefix, retainedMessages)
	state.Lineage = nextLineage
	state.Conversation = nextLineage.FullMessages()
	state.Context = recordCompactionSummary(state.Context, summaryText, candidate, turn)
	if compactionCount != nil {
		(*compactionCount)++
		emitCompactionDiagnostics(req.Events, turn, *compactionCount, fit, retainedMessages, candidate, summaryText, promptText)
	}

	skipped[compactionCandidateKey(candidate)] = true
	return true, nil
}

func completeCompactionCall(ctx context.Context, req RunRequest, turn int, chatRequest provider.ChatRequest, budget prompt.ModelTokenBudget) (provider.ChatResponse, error) {
	if budget.ContextSize > 0 {
		fit, err := budget.FitCompactionRequest(ctx, chatRequest)
		if err != nil {
			return provider.ChatResponse{}, err
		}
		if !fit.Fits {
			return provider.ChatResponse{}, fmt.Errorf("compaction request exceeds context window: %s", fit.String())
		}
	}
	emitEvent(req.Events, output.NewAPIRequestEvent(chatRequest.Model, chatRequest.Messages, chatRequest.Tools, chatRequest.MaxTokens, nil, budget))

	stream, err := req.Provider.StreamChatCompletion(ctx, chatRequest)
	if err == nil {
		response, streamErr := consumeModelStream(ctx, req.Events, turn, stream)
		if streamErr != nil {
			emitEvent(req.Events, output.NewAPIResponseEvent(nil, nil, "", streamErr))
			return provider.ChatResponse{}, streamErr
		}
		emitEvent(req.Events, output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, nil))
		return response, nil
	}

	response, chatErr := req.Provider.ChatCompletion(ctx, chatRequest)
	emitEvent(req.Events, output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, chatErr))
	if chatErr != nil {
		return provider.ChatResponse{}, chatErr
	}
	return response, nil
}

func buildCompactionRequest(req RunRequest, state RunState, candidate ConversationCandidate) (provider.ChatRequest, string) {
	source := toProviderMessages(candidate.Messages)
	messages := prompt.BuildConversationCompactionPrompt(source, toPromptContext(state.Context))
	maxTokens := compactionMaxTokens(req.ModelBudget)
	request := provider.ChatRequest{
		Model:       req.Model,
		Messages:    messages,
		Temperature: req.Temperature,
		MaxTokens:   maxTokens,
	}
	return request, summarizeCompactionPrompt(candidate)
}

func summarizeCompactionPrompt(candidate ConversationCandidate) string {
	return fmt.Sprintf("generation=%d view=%s messages=%d", candidate.GenerationID, candidate.View, len(candidate.Messages))
}

func compactionMaxTokens(budget prompt.ModelTokenBudget) *int {
	if budget.SummaryMaxTokens > 0 {
		return &budget.SummaryMaxTokens
	}
	if budget.MaxCompletionTokens > 0 {
		return &budget.MaxCompletionTokens
	}
	return nil
}

func selectCompactionCandidate(lineage ConversationLineage, skipped map[string]bool) (ConversationCandidate, bool) {
	candidates := lineage.Candidates()
	if len(candidates) == 0 {
		return ConversationCandidate{}, false
	}

	bestIndex := -1
	for i, candidate := range candidates {
		if len(candidate.Messages) == 0 {
			continue
		}
		if skipped != nil && skipped[compactionCandidateKey(candidate)] {
			continue
		}
		if bestIndex < 0 || richerCandidate(candidate, candidates[bestIndex]) {
			bestIndex = i
		}
	}
	if bestIndex < 0 {
		return ConversationCandidate{}, false
	}
	return candidates[bestIndex], true
}

func richerCandidate(a, b ConversationCandidate) bool {
	if len(a.Messages) != len(b.Messages) {
		return len(a.Messages) > len(b.Messages)
	}
	if a.GenerationID != b.GenerationID {
		return a.GenerationID > b.GenerationID
	}
	if a.View != b.View {
		return a.View == ConversationViewFull
	}
	return false
}

func compactionCandidateKey(candidate ConversationCandidate) string {
	return fmt.Sprintf("%d:%s", candidate.GenerationID, candidate.View)
}

func retainedMessagesForCandidate(lineage ConversationLineage, candidate ConversationCandidate) []Message {
	generation, ok := generationByID(lineage, candidate.GenerationID)
	if !ok {
		return cloneMessages(candidate.Messages)
	}
	if len(generation.SummaryPrefix) > 0 {
		return generation.SummaryPrefixStrippedMessages()
	}
	if candidate.View == ConversationViewSummaryPrefixStripped {
		return cloneMessages(candidate.Messages)
	}
	return nil
}

func generationByID(lineage ConversationLineage, generationID int) (ConversationGeneration, bool) {
	for _, generation := range lineage.Generations {
		if generation.ID == generationID {
			return generation.Clone(), true
		}
	}
	return ConversationGeneration{}, false
}

func recordCompactionSummary(current ContextState, summary string, candidate ConversationCandidate, turn int) ContextState {
	next := current.Clone()
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
	next.RetainedSummaries = appendRetainedSummary(next.RetainedSummaries, RetainedSummary{
		Title:  title,
		Text:   text,
		Source: fmt.Sprintf("compaction:%d/%s", candidate.GenerationID, candidate.View),
		Turn:   turn,
	})
	return next
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
	if fit.ContextSize <= 0 || fit.TotalTokens <= 0 {
		return false
	}
	overage := fit.TotalTokens - fit.ContextSize
	if overage <= 0 {
		return false
	}
	return overage*100 >= fit.ContextSize*compactionFragilityOveragePercent
}
