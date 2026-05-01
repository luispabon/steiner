package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

const defaultDropRetainTurns = 3

const dropCompactionMarker = "[context compacted - see scratchpad for task state; re-read files if needed]"

const shortCompactionSystemPrompt = "Write a concise handoff summary for the next turn."

// CompactionOutcome captures the state mutation and diagnostics emitted by a
// compaction strategy.
type CompactionOutcome struct {
	State            RunState
	Applied          bool
	Candidate        ConversationCandidate
	Fit              prompt.RequestTokenBudget
	RetainedMessages []Message
	SummaryText      string
	PromptText       string
}

type summarizeCompactor struct{}

type dropCompactor struct {
	retainTurns int
}

type hybridCompactor struct {
	maskingWindowTurns int
}

func (summarizeCompactor) Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate) (CompactionOutcome, error) {
	return summarizeCompactionOutcome(ctx, req, state, turn, candidate, candidate.Messages, retainedMessagesForCandidate(state.Lineage, candidate))
}

func (d dropCompactor) Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate) (CompactionOutcome, error) {
	retainTurns := d.retainTurns
	if retainTurns <= 0 {
		retainTurns = defaultDropRetainTurns
	}

	sourceMessages := cloneMessages(candidate.Messages)
	retainedMessages := retainRecentTurns(sourceMessages, retainTurns)
	if len(retainedMessages) == 0 {
		return CompactionOutcome{Candidate: candidate}, nil
	}
	retainedMessages = append([]Message{{Role: MessageRoleSummary, Content: dropCompactionMarker}}, retainedMessages...)

	nextLineage := state.Lineage.WithCurrentMessages(retainedMessages)
	nextState := state.Clone()
	nextState.Lineage = nextLineage
	nextState.Conversation = nextLineage.FullMessages()

	request := buildConversationRequest(req, nextState.Conversation)
	fit, err := req.ModelBudget.FitRequest(ctx, request)
	if err != nil {
		return CompactionOutcome{}, err
	}

	return CompactionOutcome{
		State:            nextState,
		Applied:          true,
		Candidate:        candidate,
		Fit:              fit,
		RetainedMessages: cloneMessages(retainedMessages),
		SummaryText:      dropCompactionMarker,
		PromptText:       fmt.Sprintf("drop retain_turns=%d", retainTurns),
	}, nil
}

func (h hybridCompactor) Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate) (CompactionOutcome, error) {
	window := h.maskingWindowTurns
	if window <= 0 {
		window = 5
	}

	maskedProviderMessages := prompt.MaskConversation(toProviderMessages(candidate.Messages), window)
	maskedMessages := fromProviderMessages(maskedProviderMessages)
	nextLineage := state.Lineage.WithCurrentMessages(maskedMessages)
	request := buildConversationRequest(req, nextLineage.FullMessages())
	fit, err := req.ModelBudget.FitRequest(ctx, request)
	if err != nil {
		return CompactionOutcome{}, err
	}
	if fit.Fits {
		nextState := state.Clone()
		nextState.Lineage = nextLineage
		nextState.Conversation = nextLineage.FullMessages()
		return CompactionOutcome{
			State:            nextState,
			Applied:          true,
			Candidate:        candidate,
			Fit:              fit,
			RetainedMessages: cloneMessages(maskedMessages),
			SummaryText:      "[conversation masked; no summary needed]",
			PromptText:       fmt.Sprintf("hybrid mask window=%d", window),
		}, nil
	}

	maskedCandidate := candidate
	maskedCandidate.Messages = maskedMessages
	return summarizeCompactionOutcome(ctx, req, state, turn, maskedCandidate, maskedMessages, maskedMessages)
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

	compactor := compactorForRequest(req)
	outcome, err := compactor.Compact(ctx, req, *state, turn, candidate)
	if err != nil {
		return false, err
	}

	if !outcome.Applied {
		skipped[compactionCandidateKey(candidate)] = true
		return true, nil
	}

	*state = outcome.State
	if compactionCount != nil {
		(*compactionCount)++
		emitCompactionDiagnostics(req.Events, turn, *compactionCount, outcome.Fit, outcome.RetainedMessages, outcome.Candidate, outcome.SummaryText, outcome.PromptText)
	}
	skipped[compactionCandidateKey(candidate)] = true
	return true, nil
}

func compactorForRequest(req RunRequest) Compactor {
	if req.ContextManager != nil {
		if compactor, ok := req.ContextManager.(Compactor); ok {
			return compactor
		}
	}
	return summarizeCompactor{}
}

func summarizeCompactionOutcome(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, sourceMessages, retainedMessages []Message) (CompactionOutcome, error) {
	compactionCandidate := candidate
	compactionCandidate.Messages = cloneMessages(sourceMessages)

	compactionRequest, promptText := buildCompactionRequest(req, state, compactionCandidate)
	fit, err := req.ModelBudget.FitCompactionRequest(ctx, compactionRequest)
	if err != nil {
		return CompactionOutcome{}, err
	}
	if !fit.Fits {
		maskedMessages := fromProviderMessages(prompt.MaskConversation(toProviderMessages(compactionCandidate.Messages), 1))
		if len(maskedMessages) > 0 {
			maskedCandidate := compactionCandidate
			maskedCandidate.Messages = maskedMessages
			maskedRequest, maskedPromptText := buildCompactionRequest(req, state, maskedCandidate)
			maskedFit, maskedErr := req.ModelBudget.FitCompactionRequest(ctx, maskedRequest)
			if maskedErr != nil {
				return CompactionOutcome{}, maskedErr
			}
			if maskedFit.Fits {
				compactionCandidate = maskedCandidate
				sourceMessages = maskedMessages
				retainedMessages = maskedMessages
				compactionRequest = maskedRequest
				promptText = maskedPromptText
				fit = maskedFit
			} else {
				previewMessages := truncateCompactionMessages(compactionCandidate.Messages, 80)
				if len(previewMessages) == 0 {
					return CompactionOutcome{
						Candidate:   candidate,
						Fit:         fit,
						PromptText:  promptText,
						Applied:     false,
						SummaryText: "",
					}, nil
				}
				previewCandidate := compactionCandidate
				previewCandidate.Messages = previewMessages
				previewRequest, previewPromptText := buildCompactionRequest(req, state, previewCandidate)
				previewFit, previewErr := req.ModelBudget.FitCompactionRequest(ctx, previewRequest)
				if previewErr != nil {
					return CompactionOutcome{}, previewErr
				}
				if !previewFit.Fits {
					shortReq := req
					shortReq.Prompt.PromptOverrides.Compaction = shortCompactionSystemPrompt
					shortRequest, shortPromptText := buildCompactionRequest(shortReq, state, previewCandidate)
					shortFit, shortErr := req.ModelBudget.FitCompactionRequest(ctx, shortRequest)
					if shortErr != nil {
						return CompactionOutcome{}, shortErr
					}
					if !shortFit.Fits {
						return CompactionOutcome{
							Candidate:   candidate,
							Fit:         fit,
							PromptText:  promptText,
							Applied:     false,
							SummaryText: "",
						}, nil
					}
					previewRequest = shortRequest
					previewPromptText = shortPromptText
					previewFit = shortFit
				}
				compactionCandidate = previewCandidate
				sourceMessages = previewMessages
				retainedMessages = previewMessages
				compactionRequest = previewRequest
				promptText = previewPromptText
				fit = previewFit
			}
		} else {
			return CompactionOutcome{
				Candidate:   candidate,
				Fit:         fit,
				PromptText:  promptText,
				Applied:     false,
				SummaryText: "",
			}, nil
		}
	}

	response, err := completeCompactionCall(ctx, req, turn, compactionRequest, req.ModelBudget)
	if err != nil {
		return CompactionOutcome{}, err
	}

	summaryText := strings.TrimSpace(response.Message.Content)
	if summaryText == "" {
		summaryText = fallbackCompactionSummary(compactionCandidate)
	}
	if summaryText == "" {
		return CompactionOutcome{
			Candidate:   candidate,
			Fit:         fit,
			PromptText:  promptText,
			Applied:     false,
			SummaryText: "",
		}, nil
	}

	retained := cloneMessages(retainedMessages)
	if len(retained) == 0 {
		retained = cloneMessages(compactionCandidate.Messages)
	}

	summaryPrefix := []Message{{Role: MessageRoleSummary, Content: summaryText}}
	nextLineage := state.Lineage.WithNewGeneration(summaryPrefix, retained)
	nextState := state.Clone()
	nextState.Lineage = nextLineage
	nextState.Conversation = nextLineage.FullMessages()
	nextState.Context = recordCompactionSummary(nextState.Context, summaryText, candidate, turn)

	return CompactionOutcome{
		State:            nextState,
		Applied:          true,
		Candidate:        candidate,
		Fit:              fit,
		RetainedMessages: retained,
		SummaryText:      summaryText,
		PromptText:       promptText,
	}, nil
}

func buildConversationRequest(req RunRequest, messages []Message) provider.ChatRequest {
	return provider.ChatRequest{
		Model:       req.Model,
		Messages:    toProviderMessages(messages),
		Tools:       cloneProviderTools(req.Tools),
		ExtraParams: req.ExtraParams,
		MaxTokens:   req.MaxTokens,
	}
}

func retainRecentTurns(messages []Message, retainTurns int) []Message {
	if len(messages) == 0 {
		return nil
	}
	if retainTurns <= 0 {
		retainTurns = defaultDropRetainTurns
	}

	turns := splitCompactionTurns(messages)
	if len(turns) <= retainTurns {
		return cloneMessages(messages)
	}

	start := len(turns) - retainTurns
	out := make([]Message, 0, len(messages))
	for _, turn := range turns[start:] {
		out = append(out, cloneMessages(turn)...)
	}
	return out
}

func splitCompactionTurns(messages []Message) [][]Message {
	if len(messages) == 0 {
		return nil
	}

	turns := make([][]Message, 0, len(messages))
	current := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.Role == MessageRoleUser && len(current) > 0 {
			turns = append(turns, current)
			current = make([]Message, 0, len(messages)-len(turns))
		}
		current = append(current, message)
	}
	if len(current) > 0 {
		turns = append(turns, current)
	}
	return turns
}

func truncateCompactionMessages(messages []Message, limit int) []Message {
	if len(messages) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 80
	}
	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		cloned := message
		cloned.Content = summarizeTextPreview(cloned.Content, limit)
		out = append(out, cloned)
	}
	return out
}

func completeCompactionCall(ctx context.Context, req RunRequest, turn int, chatRequest provider.ChatRequest, budget prompt.ModelTokenBudget) (provider.ChatResponse, error) {
	return executeChatRequest(ctx, req.Provider, turn, chatRequest, budget, req.Events, nil, true, true)
}

func buildCompactionRequest(req RunRequest, state RunState, candidate ConversationCandidate) (provider.ChatRequest, string) {
	source := toProviderMessages(candidate.Messages)
	messages := prompt.BuildConversationCompactionPrompt(source, toPromptContext(state.Context), req.Prompt.PromptOverrides.Compaction)
	maxTokens := compactionMaxTokens(req.ModelBudget)
	request := provider.ChatRequest{
		Model:       req.Model,
		Messages:    messages,
		ExtraParams: req.ExtraParams,
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
