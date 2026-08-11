package agent

import (
	"context"
	"fmt"

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
	blocks           []prompt.ContextBlock
	promptText       string
	fit              prompt.RequestTokenBudget
}

func (summarizeCompactor) Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate) (CompactionOutcome, error) {
	return twoStageSummarizeCompaction(ctx, req, state, turn, candidate)
}

func twoStageSummarizeCompaction(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate) (CompactionOutcome, error) {
	return summarizeCompactionStages{
		stageRunner: summarizeCompactionStage,
		fitRunner:   fitConversationState,
	}.run(ctx, req, state, turn, candidate)
}

type compactionStageRunner func(context.Context, RunRequest, RunState, int, ConversationCandidate, []Message, []Message, prompt.CompactionMode, int) (CompactionOutcome, error)

type compactionFitRunner func(context.Context, RunRequest, RunState) (prompt.RequestTokenBudget, error)

type summarizeCompactionStages struct {
	stageRunner compactionStageRunner
	fitRunner   compactionFitRunner
}

func (s summarizeCompactionStages) run(
	ctx context.Context,
	req RunRequest,
	state RunState,
	turn int,
	candidate ConversationCandidate,
) (CompactionOutcome, error) {
	fullMessages := cloneMessages(candidate.Messages)
	retentionBase := compactionRetentionBaseMessages(state.Lineage, candidate)
	normalSource, normalRetained := compactionSourceAndRetention(fullMessages, retentionBase, normalCompactionRetainTurns)
	emergencySource, emergencyRetained := compactionSourceAndRetention(fullMessages, retentionBase, emergencyCompactionRetainTurns)

	normalOutcome, err := s.stageRunner(ctx, req, state, turn, candidate, normalSource, normalRetained, prompt.CompactionModeNormal, compactionSummaryMaxTokensForMode(req.ModelBudget, prompt.CompactionModeNormal))
	if err != nil {
		return CompactionOutcome{}, err
	}
	if !normalOutcome.Applied {
		emergencyCandidate := makeCompactionCandidate(candidate, emergencySource, emergencyRetained)
		return s.runStageAndFit(ctx, req, state, turn, emergencyCandidate, emergencySource, emergencyRetained, prompt.CompactionModeEmergency, compactionSummaryMaxTokensForMode(req.ModelBudget, prompt.CompactionModeEmergency))
	}

	normalFit, err := s.fitRunner(ctx, req, normalOutcome.State)
	if err != nil {
		return CompactionOutcome{}, err
	}
	normalOutcome.Fit = normalFit
	if !needsEmergencyCompaction(normalFit) {
		return normalOutcome, nil
	}

	emergencySource = normalOutcome.State.Conversation
	emergencyRetentionBase := normalOutcome.State.Lineage.SummaryPrefixStrippedMessages()
	if len(emergencyRetentionBase) == 0 {
		emergencyRetentionBase = cloneMessages(emergencySource)
	}
	emergencySource, emergencyRetained = compactionSourceAndRetention(emergencySource, emergencyRetentionBase, emergencyCompactionRetainTurns)
	emergencyCandidate := makeCompactionCandidate(normalOutcome.Candidate, emergencySource, emergencyRetained)
	return s.runStageAndFit(ctx, req, normalOutcome.State, turn, emergencyCandidate, emergencySource, emergencyRetained, prompt.CompactionModeEmergency, compactionSummaryMaxTokensForMode(req.ModelBudget, prompt.CompactionModeEmergency))
}

// runStageAndFit runs a single compaction stage, verifies it applied, and
// re-checks the resulting fit for whether emergency compaction is still
// required.
func (s summarizeCompactionStages) runStageAndFit(
	ctx context.Context,
	req RunRequest,
	state RunState,
	turn int,
	candidate ConversationCandidate,
	sourceMessages, retainedMessages []Message,
	mode prompt.CompactionMode,
	maxTokens int,
) (CompactionOutcome, error) {
	outcome, err := s.stageRunner(ctx, req, state, turn, candidate, sourceMessages, retainedMessages, mode, maxTokens)
	if err != nil {
		return CompactionOutcome{}, err
	}
	if !outcome.Applied {
		return outcome, emergencyCompactionError(outcome.Fit)
	}

	fit, err := s.fitRunner(ctx, req, outcome.State)
	if err != nil {
		return CompactionOutcome{}, err
	}
	outcome.Fit = fit
	if needsEmergencyCompaction(fit) {
		return outcome, emergencyCompactionError(fit)
	}

	return outcome, nil
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

	response, err := completeCompactionCall(ctx, req, turn, plan.request, req.ModelBudget, plan.blocks)
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

	// Reasoning is intentionally omitted here: this request is only used to
	// estimate token counts against the budget (FitRequest), never sent to a
	// provider, and reasoning effort does not affect token estimation.
	chatRequest := provider.ChatRequest{
		Model:       req.ResolvedModel.BackendModelID,
		Messages:    assembly.Messages,
		Tools:       provider.CloneTools(req.Tools),
		Params:      req.ResolvedModel.Params,
		ExtraParams: req.ResolvedModel.ExtraParams,
	}
	chatRequest = applyPromptSuffix(req.ResolvedModel.PromptSuffix, chatRequest)
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
	request, blocks, promptText, err := buildCompactionRequestWithMode(ctx, req, state, workingCandidate, mode, maxTokens)
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
		blocks:           blocks,
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
