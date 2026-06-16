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
	compactionUsageThreshold          = 0.70
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

const (
	normalCompactionRetainTurns    = 3
	emergencyCompactionRetainTurns = 1
)

// CompactionOutcome captures the state mutation and diagnostics emitted by a
// compaction strategy.
type CompactionOutcome struct {
	State              RunState
	Applied            bool
	Candidate          ConversationCandidate
	Fit                prompt.RequestTokenBudget
	Mode               prompt.CompactionMode
	SummaryTokenBudget int
	RetainedMessages   []Message
	SummaryText        string
	PromptText         string
}

type summarizeCompactor struct{}

type compactionExecutionPlan struct {
	candidate        ConversationCandidate
	sourceMessages   []Message
	retainedMessages []Message
	request          provider.ChatRequest
	promptText       string
	fit              prompt.RequestTokenBudget
}

func (summarizeCompactor) Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate) (CompactionOutcome, error) {
	return twoStageSummarizeCompaction(ctx, req, state, turn, candidate)
}

func twoStageSummarizeCompaction(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate) (CompactionOutcome, error) {
	return twoStageSummarizeCompactionWithStages(ctx, req, state, turn, candidate, summarizeCompactionStage, fitConversationState)
}

type compactionStageRunner func(context.Context, RunRequest, RunState, int, ConversationCandidate, []Message, []Message, prompt.CompactionMode, int) (CompactionOutcome, error)

type compactionFitRunner func(context.Context, RunRequest, RunState) (prompt.RequestTokenBudget, error)

func twoStageSummarizeCompactionWithStages(
	ctx context.Context,
	req RunRequest,
	state RunState,
	turn int,
	candidate ConversationCandidate,
	stageRunner compactionStageRunner,
	fitRunner compactionFitRunner,
) (CompactionOutcome, error) {
	fullMessages := cloneMessages(candidate.Messages)
	retentionBase := compactionRetentionBaseMessages(state.Lineage, candidate)
	normalSource, normalRetained := compactionSourceAndRetention(fullMessages, retentionBase, normalCompactionRetainTurns)
	emergencySource, emergencyRetained := compactionSourceAndRetention(fullMessages, retentionBase, emergencyCompactionRetainTurns)

	normalOutcome, err := stageRunner(ctx, req, state, turn, candidate, normalSource, normalRetained, prompt.CompactionModeNormal, compactionSummaryMaxTokensForMode(req.ModelBudget, prompt.CompactionModeNormal))
	if err != nil {
		return CompactionOutcome{}, err
	}
	if !normalOutcome.Applied {
		emergencyOutcome, emergencyErr := stageRunner(ctx, req, state, turn, candidate, emergencySource, emergencyRetained, prompt.CompactionModeEmergency, compactionSummaryMaxTokensForMode(req.ModelBudget, prompt.CompactionModeEmergency))
		if emergencyErr != nil {
			return CompactionOutcome{}, emergencyErr
		}
		if !emergencyOutcome.Applied {
			return emergencyOutcome, emergencyCompactionError(emergencyOutcome.Fit)
		}
		emergencyFit, emergencyErr := fitRunner(ctx, req, emergencyOutcome.State)
		if emergencyErr != nil {
			return CompactionOutcome{}, emergencyErr
		}
		emergencyOutcome.Fit = emergencyFit
		if needsEmergencyCompaction(emergencyFit) {
			return emergencyOutcome, emergencyCompactionError(emergencyFit)
		}
		return emergencyOutcome, nil
	}

	normalFit, err := fitRunner(ctx, req, normalOutcome.State)
	if err != nil {
		return CompactionOutcome{}, err
	}
	normalOutcome.Fit = normalFit
	if !needsEmergencyCompaction(normalFit) {
		return normalOutcome, nil
	}

	emergencySource = normalOutcome.State.Conversation
	emergencyCandidate := normalOutcome.Candidate
	emergencyCandidate.Messages = cloneMessages(emergencySource)
	emergencyRetentionBase := normalOutcome.State.Lineage.SummaryPrefixStrippedMessages()
	if len(emergencyRetentionBase) == 0 {
		emergencyRetentionBase = cloneMessages(emergencySource)
	}
	emergencySource, emergencyRetained = compactionSourceAndRetention(emergencySource, emergencyRetentionBase, emergencyCompactionRetainTurns)
	emergencyOutcome, err := stageRunner(ctx, req, normalOutcome.State, turn, emergencyCandidate, emergencySource, emergencyRetained, prompt.CompactionModeEmergency, compactionSummaryMaxTokensForMode(req.ModelBudget, prompt.CompactionModeEmergency))
	if err != nil {
		return CompactionOutcome{}, err
	}
	if !emergencyOutcome.Applied {
		return emergencyOutcome, emergencyCompactionError(emergencyOutcome.Fit)
	}

	emergencyFit, err := fitRunner(ctx, req, emergencyOutcome.State)
	if err != nil {
		return CompactionOutcome{}, err
	}
	emergencyOutcome.Fit = emergencyFit
	if needsEmergencyCompaction(emergencyFit) {
		return emergencyOutcome, emergencyCompactionError(emergencyFit)
	}

	return emergencyOutcome, nil
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

func summarizeCompactionStage(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, sourceMessages, retainedMessages []Message, mode prompt.CompactionMode, maxTokens int) (CompactionOutcome, error) {
	retainedFit, err := fitConversationState(ctx, req, state.WithConversation(retainedMessages))
	if err != nil {
		return CompactionOutcome{}, err
	}
	if !retainedFit.Fits {
		return compactionNotAppliedOutcome(candidate, retainedFit, fmt.Sprintf("%s mode=%s", summarizeCompactionPrompt(candidate), mode), mode, maxTokens), compactionCannotSolveError(retainedFit)
	}
	if len(sourceMessages) == 0 {
		return compactionNotAppliedOutcome(candidate, retainedFit, fmt.Sprintf("%s mode=%s no_source=true", summarizeCompactionPrompt(candidate), mode), mode, maxTokens), nil
	}

	plan, ok, err := buildCompactionExecutionPlanWithMode(ctx, req, state, candidate, sourceMessages, retainedMessages, mode, maxTokens)
	if err != nil {
		return CompactionOutcome{}, err
	}
	if !ok {
		return compactionNotAppliedOutcome(candidate, plan.fit, plan.promptText, mode, maxTokens), nil
	}

	response, err := completeCompactionCall(ctx, req, turn, plan.request, req.ModelBudget)
	if err != nil {
		return CompactionOutcome{}, err
	}

	summaryText := compactionSummaryText(response.Message.Content, plan.candidate)
	if summaryText == "" {
		return compactionNotAppliedOutcome(candidate, plan.fit, plan.promptText, mode, maxTokens), nil
	}

	retained := cloneMessages(plan.retainedMessages)
	nextState := buildSummarizedCompactionState(state, summaryText, candidate, turn, retained)
	latestFit, err := fitConversationState(ctx, req, nextState)
	if err != nil {
		return CompactionOutcome{}, err
	}

	return CompactionOutcome{
		State:              nextState,
		Applied:            true,
		Candidate:          candidate,
		Fit:                latestFit,
		Mode:               mode,
		SummaryTokenBudget: maxTokens,
		RetainedMessages:   retained,
		SummaryText:        summaryText,
		PromptText:         plan.promptText,
	}, nil
}

func fitConversationState(ctx context.Context, req RunRequest, state RunState) (prompt.RequestTokenBudget, error) {
	basePrompt := prepareBasePrompt(req)
	assembly, err := prompt.Assemble(ctx, assemblyOptions(basePrompt, state))
	if err != nil {
		return prompt.RequestTokenBudget{}, err
	}

	chatRequest := provider.ChatRequest{
		Model:       req.ResolvedModel.BackendModelID,
		Messages:    assembly.Messages,
		Tools:       cloneProviderTools(req.Tools),
		Params:      req.ResolvedModel.Params,
		ExtraParams: req.ResolvedModel.ExtraParams,
	}
	chatRequest = buildTurnChatRequest(req, chatRequest)
	return req.ModelBudget.FitRequest(ctx, chatRequest)
}

// Compact reduces the current conversation to fit the model budget.
func (r *Runner) Compact(ctx context.Context, req RunRequest, currentConv []Message) ([]Message, error) {
	state := RunState{
		Conversation: currentConv,
		Lineage:      newConversationLineage(currentConv),
		Context:      fromPromptContext(req.Prompt.ContextState),
	}

	skipped := map[string]bool{}
	compactionCount := 0

	compacted, err := r.compactConversationForBudget(ctx, req, &state, 0, nil, skipped, &compactionCount)
	if err != nil {
		return nil, err
	}
	if !compacted {
		return currentConv, nil
	}

	return state.Conversation, nil
}

func (r *Runner) compactConversationForBudget(ctx context.Context, req RunRequest, state *RunState, turn int, beforeFit *prompt.RequestTokenBudget, skipped map[string]bool, compactionCount *int) (bool, error) {
	candidate, ok := selectCompactionCandidate(state.Lineage, skipped)
	if !ok {
		return false, nil
	}

	currentFit, err := compactionCurrentFit(ctx, req, *state, beforeFit)
	if err != nil {
		return false, err
	}

	outcome, err := summarizeCompactor{}.Compact(ctx, req, *state, turn, candidate)
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
		emitCompactionDiagnostics(req.Events, turn, *compactionCount, currentFit, outcome.Fit, outcome.Mode, outcome.SummaryTokenBudget, outcome.RetainedMessages, outcome.Candidate, outcome.SummaryText, outcome.PromptText)
	}
	skipped[compactionCandidateKey(candidate)] = true
	return true, nil
}

func compactionCurrentFit(ctx context.Context, req RunRequest, state RunState, beforeFit *prompt.RequestTokenBudget) (prompt.RequestTokenBudget, error) {
	if beforeFit != nil {
		return *beforeFit, nil
	}
	return fitConversationState(ctx, req, state)
}

func buildCompactionExecutionPlanWithMode(ctx context.Context, req RunRequest, state RunState, candidate ConversationCandidate, sourceMessages, retainedMessages []Message, mode prompt.CompactionMode, maxTokens int) (compactionExecutionPlan, bool, error) {
	plan, err := newCompactionExecutionPlanWithMode(ctx, req, state, candidate, sourceMessages, retainedMessages, mode, maxTokens)
	if err != nil {
		return compactionExecutionPlan{}, false, err
	}
	if plan.fit.Fits {
		return plan, true, nil
	}
	return plan, false, nil
}

func newCompactionExecutionPlanWithMode(ctx context.Context, req RunRequest, state RunState, candidate ConversationCandidate, sourceMessages, retainedMessages []Message, mode prompt.CompactionMode, maxTokens int) (compactionExecutionPlan, error) {
	workingCandidate := candidate
	workingCandidate.Messages = stripImages(cloneMessages(sourceMessages))
	request, promptText, err := buildCompactionRequestWithMode(ctx, req, state, workingCandidate, mode, maxTokens)
	if err != nil {
		return compactionExecutionPlan{}, err
	}
	fit, err := req.ModelBudget.FitCompactionRequest(ctx, request)
	if err != nil {
		return compactionExecutionPlan{}, err
	}
	return compactionExecutionPlan{
		candidate:        workingCandidate,
		sourceMessages:   stripImages(cloneMessages(sourceMessages)),
		retainedMessages: cloneMessages(retainedMessages),
		request:          request,
		promptText:       promptText,
		fit:              fit,
	}, nil
}

func compactionNotAppliedOutcome(candidate ConversationCandidate, fit prompt.RequestTokenBudget, promptText string, mode prompt.CompactionMode, summaryTokenBudget int) CompactionOutcome {
	return CompactionOutcome{
		Candidate:          candidate,
		Fit:                fit,
		Mode:               mode,
		SummaryTokenBudget: summaryTokenBudget,
		PromptText:         promptText,
		Applied:            false,
		SummaryText:        "",
	}
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
	messages := append(cloneProviderMessages(assembly.Messages), provider.Message{
		Role:    provider.MessageRoleUser,
		Content: prompt.RenderConversationCompactionInstruction(basePrompt.PromptOverrides.Compaction, mode, basePrompt.CaveHuman),
	})
	request := provider.ChatRequest{
		Model:       req.ResolvedModel.BackendModelID,
		Messages:    messages,
		ExtraParams: req.ResolvedModel.ExtraParams,
		MaxTokens:   compactionMaxTokensForMode(maxTokens),
	}
	return applyPromptSuffix(req.ResolvedModel.PromptSuffix, request), fmt.Sprintf("%s mode=%s", summarizeCompactionPrompt(candidate), mode), nil
}

func retainRecentTurns(messages []Message, retainTurns int) []Message {
	if len(messages) == 0 {
		return nil
	}
	if retainTurns <= 0 {
		retainTurns = normalCompactionRetainTurns
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

func compactionRetentionBaseMessages(lineage ConversationLineage, candidate ConversationCandidate) []Message {
	base := retainedMessagesForCandidate(lineage, candidate)
	if len(base) > 0 {
		return base
	}
	return cloneMessages(candidate.Messages)
}

func compactionSourceAndRetention(fullMessages, retentionBase []Message, retainTurns int) ([]Message, []Message) {
	retainedMessages := retainRecentTurns(retentionBase, retainTurns)
	if len(retainedMessages) == 0 {
		return cloneMessages(fullMessages), nil
	}
	if len(retainedMessages) >= len(fullMessages) {
		return nil, cloneMessages(retainedMessages)
	}
	sourceMessages := make([]Message, len(fullMessages)-len(retainedMessages))
	copy(sourceMessages, fullMessages[:len(fullMessages)-len(retainedMessages)])
	return sourceMessages, cloneMessages(retainedMessages)
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
	var logger *CompactionLogger
	if req.CompactionLogPath != "" {
		var err error
		logger, err = NewCompactionLogger(req.CompactionLogPath)
		if err == nil {
			_ = logger.LogRequest(chatRequest) // best effort
			defer func() { _ = logger.Close() }()
		}
	}
	response, _, err := executeChatRequest(ctx, req.Provider, turn, chatRequest, budget, req.Events, nil, true, false, output.ChunkSourceAssistant)
	if logger != nil {
		_ = logger.LogResponse(response) // best effort
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

func compactionCannotSolveError(fit prompt.RequestTokenBudget) error {
	return fmt.Errorf(
		"compaction cannot solve this request: retained conversation already exceeds the hard context limit: prompt=%d context_window=%d usage=%.0f%% hard_limit=%d",
		fit.EstimatedPromptTokens,
		fit.ContextSize,
		fit.PromptUsage*100,
		fit.HardLimitTokens,
	)
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
