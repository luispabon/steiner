package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

const (
	compactionWarningThreshold        = 2
	compactionCriticalThreshold       = 3
	compactionFragilityOveragePercent = 20
	compactionUsageThreshold          = 0.70
	compactionHysteresisPercent       = 10
	compactionCapUsagePercent         = 90
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

// errStatefulCompaction is returned when compaction is attempted for a provider
// that owns its transcript (StatefulTranscript), where rewriting sent history is
// not supported yet.
var errStatefulCompaction = errors.New("compaction is not supported yet with the claude_subscription provider (tracked in #895); start a new session to continue")

// errCompactionFallbackNotFit is returned when a summary request on a fallback
// compaction target does not fit that target's budget. It is an error rather
// than a skipped stage, so a caller can retry without the fallback target.
var errCompactionFallbackNotFit = errors.New("compaction fallback summary request does not fit the fallback target budget")

// errCompactionFallbackEmptySummary is returned when a summary call on a
// fallback compaction target returns no summary text. It is an error rather
// than a not-applied stage, so the operation reruns on main instead of
// treating the empty summary as a no-op.
var errCompactionFallbackEmptySummary = errors.New("fallback summary is empty")

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
	// Usage is the summarizer response token accounting.
	Usage *provider.UsageStats
	// StageCount is the number of compaction stages (1 or 2) executed in this outcome.
	StageCount int
	// CompactionModel is the canonical ProviderAlias/BackendModelID of the model
	// that produced the summary: the fallback target when FallbackStatus is used,
	// main when it is skipped. Empty when no fallback was selected or the
	// selected fallback applied nothing.
	CompactionModel string
	// FallbackStatus is output.CompactionFallbackUsed or
	// output.CompactionFallbackSkipped. Empty when no fallback was selected or
	// the selected fallback applied nothing.
	FallbackStatus string
	// FallbackReason explains FallbackStatus.
	FallbackReason string
	// BackendStateCleared reports that a main summary retired the backend state
	// stored for the generation being compacted. It is reported independently of
	// Applied, so a caller can drop that state even when a later stage fails or
	// the summary applies nothing.
	BackendStateCleared bool
	// pendingArchive holds the sources to append to the history archive once
	// this outcome's state is adopted.
	pendingArchive [][]Message
}

// compactionBackendStateRetiredError marks an error that followed a summary
// which retired the generation's backend state. It keeps the original error
// text and chain, so errors.Is and errors.As still match the underlying
// failure, and implements BackendStateRetired so provider.BackendStateRetired
// reports the retired state.
type compactionBackendStateRetiredError struct{ err error }

func (e *compactionBackendStateRetiredError) Error() string { return e.err.Error() }

func (e *compactionBackendStateRetiredError) Unwrap() error { return e.err }

// BackendStateRetired reports that the failed request retired the backend state
// it was issued against.
func (e *compactionBackendStateRetiredError) BackendStateRetired() bool { return true }

// markCompactionBackendStateRetired wraps err so a caller can drop the retired
// backend state. It returns err unchanged when err is nil or already marked.
func markCompactionBackendStateRetired(err error) error {
	if err == nil || provider.BackendStateRetired(err) {
		return err
	}
	return &compactionBackendStateRetiredError{err: err}
}

// compactionFailedOutcome reports a failed compaction stage. A stage that
// already retired the main backend state contributes only that flag, never a
// partial State or pending archive, and its error is marked retired so the
// caller can drop the state it keeps.
func compactionFailedOutcome(outcome CompactionOutcome, err error) (CompactionOutcome, error) {
	if !outcome.BackendStateCleared {
		return CompactionOutcome{}, err
	}
	return CompactionOutcome{BackendStateCleared: true}, markCompactionBackendStateRetired(err)
}

// compactionCarryCleared keeps a backend state clear reported by an earlier
// stage when a later stage reports its own outcome, and marks a later error as
// retired.
func compactionCarryCleared(outcome CompactionOutcome, cleared bool, err error) (CompactionOutcome, error) {
	if !cleared {
		return outcome, err
	}
	outcome.BackendStateCleared = true
	return outcome, markCompactionBackendStateRetired(err)
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

type summarizeCompactionStageParams struct {
	sourceMessages   []Message
	retainedMessages []Message
	mode             prompt.CompactionMode
	maxTokens        int
	steering         string
	// summaryReq is the request the summary call runs on. Nil means main.
	summaryReq *RunRequest
}

func (summarizeCompactor) Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, steerings ...string) (CompactionOutcome, error) {
	steering := ""
	if len(steerings) > 0 {
		steering = steerings[0]
	}
	return twoStageSummarizeCompaction(ctx, req, state, turn, candidate, steering)
}

func twoStageSummarizeCompaction(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, steering string) (CompactionOutcome, error) {
	return compactWithFallback(ctx, req, state, func(summaryReq *RunRequest) (CompactionOutcome, error) {
		return summarizeCompactionStages{
			stageRunner: func(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
				params.steering = steering
				return summarizeCompactionStage(ctx, req, state, turn, candidate, params)
			},
			fitRunner:  fitConversationState,
			summaryReq: summaryReq,
		}.run(ctx, req, state, turn, candidate)
	})
}

type compactionStageRunner func(context.Context, RunRequest, RunState, int, ConversationCandidate, summarizeCompactionStageParams) (CompactionOutcome, error)

type compactionFitRunner func(context.Context, RunRequest, RunState) (prompt.RequestTokenBudget, error)

type summarizeCompactionStages struct {
	stageRunner compactionStageRunner
	fitRunner   compactionFitRunner
	// summaryReq is the request the summary calls run on. Nil means main. run
	// always checks retention and fit against its req argument.
	summaryReq *RunRequest
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
	normalSource, normalRetained, err := s.anchorRealUserRetention(ctx, req, state, fullMessages, normalSource, normalRetained)
	if err != nil {
		return CompactionOutcome{}, err
	}

	summaryBudget := compactionSummaryRequest(req, s.summaryReq).ModelBudget
	normalOutcome, err := s.stageRunner(ctx, req, state, turn, candidate, summarizeCompactionStageParams{
		sourceMessages:   normalSource,
		retainedMessages: normalRetained,
		mode:             prompt.CompactionModeNormal,
		maxTokens:        compactionSummaryMaxTokensForMode(summaryBudget, prompt.CompactionModeNormal),
		summaryReq:       s.summaryReq,
	})
	if err != nil {
		return compactionFailedOutcome(normalOutcome, err)
	}
	if !normalOutcome.Applied {
		emergencyCandidate := makeCompactionCandidate(candidate, emergencySource, emergencyRetained)
		outcome, err := s.runStageAndFit(ctx, req, state, turn, emergencyCandidate, summarizeCompactionStageParams{
			sourceMessages:   emergencySource,
			retainedMessages: emergencyRetained,
			mode:             prompt.CompactionModeEmergency,
			maxTokens:        compactionSummaryMaxTokensForMode(summaryBudget, prompt.CompactionModeEmergency),
			summaryReq:       s.summaryReq,
		})
		outcome, err = compactionCarryCleared(outcome, normalOutcome.BackendStateCleared, err)
		if err == nil {
			outcome.StageCount = 1
		}
		return outcome, err
	}

	normalFit, err := s.fitRunner(ctx, req, normalOutcome.State)
	if err != nil {
		return compactionFailedOutcome(normalOutcome, err)
	}
	normalOutcome.Fit = normalFit
	normalOutcome.StageCount = 1
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
	outcome, err := s.runStageAndFit(ctx, req, normalOutcome.State, turn, emergencyCandidate, summarizeCompactionStageParams{
		sourceMessages:   emergencySource,
		retainedMessages: emergencyRetained,
		mode:             prompt.CompactionModeEmergency,
		maxTokens:        compactionSummaryMaxTokensForMode(summaryBudget, prompt.CompactionModeEmergency),
		summaryReq:       s.summaryReq,
	})
	outcome, err = compactionCarryCleared(outcome, normalOutcome.BackendStateCleared, err)
	if err == nil {
		outcome.StageCount = 2
		outcome.pendingArchive = append(normalOutcome.pendingArchive, outcome.pendingArchive...)
	}
	return outcome, err
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
	params summarizeCompactionStageParams,
) (CompactionOutcome, error) {
	outcome, err := s.stageRunner(ctx, req, state, turn, candidate, params)
	if err != nil {
		return compactionFailedOutcome(outcome, err)
	}
	if !outcome.Applied {
		if outcome.Fit.Fits {
			// Nothing to summarize (or an empty summary) but the request still
			// fits the hard limit: a no-op success. A not-applied outcome carries
			// a zero-value State, so keep the pre-stage state.
			outcome.State = state
			return outcome, nil
		}
		return compactionFailedOutcome(outcome, emergencyCompactionError(outcome.Fit))
	}

	fit, err := s.fitRunner(ctx, req, outcome.State)
	if err != nil {
		return compactionFailedOutcome(outcome, err)
	}
	outcome.Fit = fit
	if needsEmergencyCompaction(fit) {
		return compactionFailedOutcome(outcome, emergencyCompactionError(fit))
	}

	return outcome, nil
}

// compactionSummaryRequest returns the request a summary call runs on: summaryReq
// when set (a fallback target), otherwise mainReq.
func compactionSummaryRequest(mainReq RunRequest, summaryReq *RunRequest) RunRequest {
	if summaryReq != nil {
		return *summaryReq
	}
	return mainReq
}

func summarizeCompactionStage(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
	onFallback := params.summaryReq != nil
	retainedFit, err := fitConversationState(ctx, req, state.WithConversation(params.retainedMessages))
	if err != nil {
		return CompactionOutcome{}, err
	}
	if !retainedFit.Fits {
		return compactionNotAppliedOutcome(candidate, retainedFit, fmt.Sprintf("%s mode=%s", summarizeCompactionPrompt(candidate), params.mode), params.mode, params.maxTokens), compactionCannotSolveError(retainedFit)
	}
	if len(params.sourceMessages) == 0 {
		return compactionNotAppliedOutcome(candidate, retainedFit, fmt.Sprintf("%s mode=%s no_source=true", summarizeCompactionPrompt(candidate), params.mode), params.mode, params.maxTokens), nil
	}

	plan, ok, err := buildCompactionExecutionPlanWithMode(ctx, req, state, candidate, params)
	if err != nil {
		return CompactionOutcome{}, err
	}
	if !ok {
		if onFallback {
			return CompactionOutcome{}, errCompactionFallbackNotFit
		}
		return compactionNotAppliedOutcome(candidate, plan.fit, plan.promptText, params.mode, params.maxTokens), nil
	}

	summary := compactionSummaryRequest(req, params.summaryReq)
	response, err := completeCompactionCall(ctx, summary, turn, plan.request, summary.ModelBudget, plan.blocks)
	if err != nil {
		return CompactionOutcome{}, err
	}
	// A fallback summary runs on another provider's transcript, so it must not
	// clear the main generation's backend state.
	cleared := false
	if response.ClearBackendState && !onFallback {
		// An in-place summary retired the generation's live process, so the
		// generation's stored backend state no longer names a live transcript.
		state.Lineage = state.Lineage.withBackendState(state.Lineage.latestGenerationID(), nil, true)
		cleared = true
	}

	if onFallback && strings.TrimSpace(response.Message.Content) == "" {
		return CompactionOutcome{}, errCompactionFallbackEmptySummary
	}
	summaryText := compactionSummaryText(response.Message.Content, plan.candidate)
	if summaryText == "" {
		outcome := compactionNotAppliedOutcome(candidate, plan.fit, plan.promptText, params.mode, params.maxTokens)
		outcome.BackendStateCleared = cleared
		return outcome, nil
	}

	var pendingArchive [][]Message
	if req.HistoryArchive != nil {
		summaryText, pendingArchive = withHistoryNote(req, summaryText, params.sourceMessages)
	}

	retained := cloneMessages(plan.retainedMessages)
	// buildSummarizedCompactionState strips skill blocks and drops skill-only
	// retained messages before persisting them. Apply the same transformation here
	// so the emitted RetainedMessages reflects the retained source messages that
	// were actually persisted; the synthetic active-skill re-injection that
	// buildSummarizedCompactionState prepends is not a retained source message.
	retained = stripRetainedSkillBlocks(retained)
	nextState := buildSummarizedCompactionState(state, summaryText, retained)
	latestFit, err := fitConversationState(ctx, req, nextState)
	if err != nil {
		return compactionFailedOutcome(CompactionOutcome{BackendStateCleared: cleared}, err)
	}

	return CompactionOutcome{
		State:               nextState,
		Applied:             true,
		Candidate:           candidate,
		Fit:                 latestFit,
		Mode:                params.mode,
		SummaryTokenBudget:  params.maxTokens,
		RetainedMessages:    retained,
		SummaryText:         summaryText,
		PromptText:          plan.promptText,
		Usage:               response.Usage,
		BackendStateCleared: cleared,
		pendingArchive:      pendingArchive,
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

// CompactWithLineage reduces the current conversation to fit the model budget
// for a caller that holds the run's lineage. The lineage contributes only its
// latest backend state, which the summary request is issued against. The
// messages compacted are still currentConv. It also reports whether the summary
// cleared that backend state, so the caller can drop it from the lineage it
// keeps. It also reports whether a compaction stage applied. Cleared is
// reported only on success; an error reports false for both flags.
func (r *Runner) CompactWithLineage(ctx context.Context, req RunRequest, lineage ConversationLineage, currentConv []Message, steerings ...string) ([]Message, bool, bool, error) {
	if provider.IsStatefulTranscript(req.Provider) && !provider.SupportsStatefulCompaction(req.Provider) {
		return nil, false, false, fmt.Errorf("compact conversation: %w", errStatefulCompaction)
	}
	steering := ""
	if len(steerings) > 0 {
		steering = steerings[0]
	}
	state := RunState{
		Conversation:    currentConv,
		Lineage:         newConversationLineage(currentConv),
		LastMainRequest: seedLastMainRequest(req.SeedLastMainRequest, req),
	}
	state.Lineage = state.Lineage.withBackendState(state.Lineage.latestGenerationID(), lineage.latestBackendState(), false)
	startID := state.Lineage.latestGenerationID()
	hadState := state.Lineage.latestBackendState() != nil

	skipped := map[string]bool{}
	compactionCount := 0

	compacted, applied, _, err := r.compactConversationForBudgetWithSteering(ctx, req, &state, 0, nil, skipped, &compactionCount, steering)
	if err != nil {
		return nil, false, false, err
	}
	if !compacted {
		return currentConv, false, false, nil
	}

	return state.Conversation, backendStateCleared(state.Lineage, startID, hadState), applied, nil
}

// backendStateCleared reports whether generation id held backend state and the
// compaction has since cleared it.
func backendStateCleared(l ConversationLineage, id int, hadState bool) bool {
	if !hadState {
		return false
	}
	for _, gen := range l.Generations {
		if gen.ID == id {
			return gen.BackendState == nil
		}
	}
	return false
}

func (r *Runner) compactConversationForBudget(ctx context.Context, req RunRequest, state *RunState, turn int, beforeFit *prompt.RequestTokenBudget, skipped map[string]bool, compactionCount *int) (bool, bool, string, error) {
	return r.compactConversationForBudgetWithSteering(ctx, req, state, turn, beforeFit, skipped, compactionCount, "")
}

// compactConversationForBudgetWithSteering processes the next candidate. It
// reports whether a candidate was attempted, whether its compaction applied, and
// the fallback reason of an attempt that applied nothing, empty otherwise.
func (r *Runner) compactConversationForBudgetWithSteering(ctx context.Context, req RunRequest, state *RunState, turn int, beforeFit *prompt.RequestTokenBudget, skipped map[string]bool, compactionCount *int, steering string) (bool, bool, string, error) {
	candidate, ok := selectCompactionCandidate(state.Lineage, skipped)
	if !ok {
		return false, false, "", nil
	}

	currentFit, err := compactionCurrentFit(ctx, req, *state, beforeFit)
	if err != nil {
		return false, false, "", err
	}

	outcome, err := summarizeCompactor{}.Compact(ctx, req, *state, turn, candidate, steering)
	if err != nil {
		// A summary that retired the latest generation's backend state leaves
		// that state unusable even though the compaction failed. Drop only that
		// state: the failed outcome's conversation and pending archive are not
		// adopted.
		clearRetiredBackendState(state, outcome)
		return false, false, "", err
	}

	if !outcome.Applied {
		clearRetiredBackendState(state, outcome)
		skipped[compactionCandidateKey(candidate)] = true
		return true, false, outcome.FallbackReason, nil
	}

	*state = outcome.State
	commitHistoryArchive(ctx, req, outcome.pendingArchive)
	appendPendingSubAgentsLine(req, state)
	if compactionCount != nil {
		*compactionCount += outcome.StageCount
		emitCompactionDiagnostics(req.Events, compactionDiagnosticsParams{
			turn:               turn,
			compactionCount:    *compactionCount,
			beforeFit:          currentFit,
			afterFit:           outcome.Fit,
			mode:               outcome.Mode,
			summaryTokenBudget: outcome.SummaryTokenBudget,
			retainedMessages:   outcome.RetainedMessages,
			candidate:          outcome.Candidate,
			summaryText:        outcome.SummaryText,
			promptText:         outcome.PromptText,
			usage:              outcome.Usage,
			compactionModel:    outcome.CompactionModel,
			fallbackStatus:     outcome.FallbackStatus,
			fallbackReason:     outcome.FallbackReason,
		})
	}
	skipped[compactionCandidateKey(candidate)] = true
	return true, true, "", nil
}

// clearRetiredBackendState drops the latest generation's backend state when the
// outcome reports that a main summary retired it, without adopting any other
// part of the outcome.
func clearRetiredBackendState(state *RunState, outcome CompactionOutcome) {
	if !outcome.BackendStateCleared {
		return
	}
	state.Lineage = state.Lineage.WithoutLatestBackendState()
}

func compactionCurrentFit(ctx context.Context, req RunRequest, state RunState, beforeFit *prompt.RequestTokenBudget) (prompt.RequestTokenBudget, error) {
	if beforeFit != nil {
		return *beforeFit, nil
	}
	return fitConversationState(ctx, req, state)
}

func buildCompactionExecutionPlanWithMode(ctx context.Context, req RunRequest, state RunState, candidate ConversationCandidate, params summarizeCompactionStageParams) (compactionExecutionPlan, bool, error) {
	plan, err := newCompactionExecutionPlanWithMode(ctx, req, state, candidate, params)
	if err != nil {
		return compactionExecutionPlan{}, false, err
	}
	if plan.fit.Fits {
		return plan, true, nil
	}
	return plan, false, nil
}

func newCompactionExecutionPlanWithMode(ctx context.Context, req RunRequest, state RunState, candidate ConversationCandidate, params summarizeCompactionStageParams) (compactionExecutionPlan, error) {
	workingCandidate := candidate
	workingCandidate.Messages = stripImages(cloneMessages(params.sourceMessages))
	summary := compactionSummaryRequest(req, params.summaryReq)
	request, blocks, promptText, err := buildCompactionRequestWithMode(ctx, summary, state, workingCandidate, params.mode, params.maxTokens, params.steering)
	if err != nil {
		return compactionExecutionPlan{}, err
	}
	fit, err := summary.ModelBudget.FitCompactionRequest(ctx, request)
	if err != nil {
		return compactionExecutionPlan{}, err
	}
	return compactionExecutionPlan{
		candidate:        workingCandidate,
		sourceMessages:   stripImages(cloneMessages(params.sourceMessages)),
		retainedMessages: cloneMessages(params.retainedMessages),
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

// appendPendingSubAgentsLine restates the pending sub-agents at the tail after
// compaction; retained messages are never rewritten.
func appendPendingSubAgentsLine(req RunRequest, state *RunState) {
	if req.PendingSubAgents == nil {
		return
	}
	msg, ok := BuildDeliveryMessage(DeliveryParts{Pending: req.PendingSubAgents()})
	if !ok {
		return
	}
	state.Conversation = append(state.Conversation, msg)
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{msg})
}
