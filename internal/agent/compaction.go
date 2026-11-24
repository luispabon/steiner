package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/output"
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

// CompactionOutcome captures the state mutation and diagnostics emitted by a
// compaction strategy.
type CompactionOutcome struct {
	State              RunState
	Applied            bool
	Candidate          ConversationCandidate
	Fit                prompt.RequestTokenBudget
	Mode               prompt.CompactionMode
	SummaryTokenBudget int
	// SummarizedMessages is the number of source messages the adopted summary
	// stage folded into the summary, excluding prior summary messages.
	SummarizedMessages int
	RetainedMessages   []Message
	SummaryText        string
	PromptText         string
	// RequestBlock is the output.RequestBlock* status of the verbatim latest
	// user request the summary carries, and RequestBlockBytes its size.
	RequestBlock      string
	RequestBlockBytes int
	// Usage is the summarizer response token accounting.
	Usage *provider.UsageStats
	// CacheReportedInputTokens is the prompt-side input total of the summary
	// calls whose provider reported cache data; zero means none did.
	CacheReportedInputTokens int
	// StageCount is the number of compaction stages (1 or 2) executed in this outcome.
	StageCount int
	// StageSizes holds the size entry of each stage that applied. A failed outcome
	// keeps the entries of the stages that applied before the failure.
	StageSizes []output.ContextCompactionStage
	// CompactionModel is the canonical ProviderAlias/BackendModelID of the model
	// that produced the summary: the fallback target when FallbackStatus is used,
	// main when it is skipped. Empty when no fallback was selected or the
	// selected fallback applied nothing.
	CompactionModel string
	// CompactionModelAlias is the configured alias of the CompactionModel model,
	// such as the named alias or raw provider/model-id reference it was resolved
	// from. Empty when that reference was raw, no alias was recorded, or
	// CompactionModel is empty.
	CompactionModelAlias string
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
// caller can drop the state it keeps. The stage sizes are kept for the failure
// diagnostics.
func compactionFailedOutcome(outcome CompactionOutcome, err error) (CompactionOutcome, error) {
	if !outcome.BackendStateCleared {
		return CompactionOutcome{StageSizes: outcome.StageSizes}, err
	}
	return CompactionOutcome{BackendStateCleared: true, StageSizes: outcome.StageSizes}, markCompactionBackendStateRetired(err)
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

type compactionExecutionPlan struct {
	candidate        ConversationCandidate
	sourceMessages   []Message
	retainedMessages []Message
	request          provider.ChatRequest
	blocks           []prompt.ContextBlock
	promptText       string
	fit              prompt.RequestTokenBudget
}

// backendStateRetirementSignaled reads the request-scoped retirement signal of
// a summary request. It is a variable so tests can simulate a provider that
// retired the backend state; production reads
// provider.BackendStateRetirementSignaled.
var backendStateRetirementSignaled = provider.BackendStateRetirementSignaled

// compactionSummaryRetired reports whether a summary request retired the
// generation's backend state. A fallback summary never does. A main summary
// reports it through the response, the request-scoped signal (which survives a
// lost terminal chunk), or an error the provider already marked.
func compactionSummaryRetired(ctx context.Context, onFallback bool, response provider.ChatResponse, err error) bool {
	if onFallback {
		return false
	}
	return response.ClearBackendState || backendStateRetirementSignaled(ctx) || provider.BackendStateRetired(err)
}

// compactionSummaryRequest returns the request a summary call runs on: summaryReq
// when set (a fallback target), otherwise mainReq.
func compactionSummaryRequest(mainReq RunRequest, summaryReq *RunRequest) RunRequest {
	if summaryReq != nil {
		return *summaryReq
	}
	return mainReq
}

// CompactWithLineage reduces the current conversation to fit the model budget
// for a caller that holds the run's lineage. The lineage contributes only its
// latest backend state, which the summary request is issued against. The
// messages compacted are still currentConv. It also reports whether the summary
// cleared that backend state, so the caller can drop it from the lineage it
// keeps. It also reports whether a compaction stage applied. Cleared is
// reported only on success; an error reports false for both flags. A returned
// error that satisfies provider.BackendStateRetired still retires the lineage's
// latest backend state, so a caller must drop that state even though the clear
// is not reported.
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
		return false, false, "", reportCompactionFailure(req.Events, turn, nil, err)
	}

	outcome, err := summarizeCompactor{}.Compact(ctx, req, *state, turn, candidate, steering)
	if err != nil {
		// A summary that retired the latest generation's backend state leaves
		// that state unusable even though the compaction failed. Drop only that
		// state: the failed outcome's conversation and pending archive are not
		// adopted.
		clearRetiredBackendState(state, outcome)
		return false, false, "", reportCompactionFailure(req.Events, turn, outcome.StageSizes, err)
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
			turn:                     turn,
			compactionCount:          *compactionCount,
			beforeFit:                currentFit,
			afterFit:                 outcome.Fit,
			mode:                     outcome.Mode,
			summaryTokenBudget:       outcome.SummaryTokenBudget,
			summarizedMessages:       outcome.SummarizedMessages,
			retainedMessages:         outcome.RetainedMessages,
			requestBlock:             outcome.RequestBlock,
			requestBlockBytes:        outcome.RequestBlockBytes,
			candidate:                outcome.Candidate,
			summaryText:              outcome.SummaryText,
			promptText:               outcome.PromptText,
			usage:                    outcome.Usage,
			cacheReportedInputTokens: outcome.CacheReportedInputTokens,
			compactionModel:          outcome.CompactionModel,
			compactionModelAlias:     outcome.CompactionModelAlias,
			fallbackStatus:           outcome.FallbackStatus,
			fallbackReason:           outcome.FallbackReason,
			stages:                   outcome.StageSizes,
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

// fitAppliedStage fits the state an applied stage produced and sizes the stage for
// diagnostics. tail is the fit of the stage's retained messages on req's main
// budget, and sourceTokens the prompt of the summary request. The summary is
// estimated on the model that wrote it.
func fitAppliedStage(ctx context.Context, req RunRequest, state, nextState RunState, params summarizeCompactionStageParams, tail prompt.RequestTokenBudget, sourceTokens int, summaryText string) (prompt.RequestTokenBudget, output.ContextCompactionStage, error) {
	latestFit, err := fitConversationState(ctx, req, nextState)
	if err != nil {
		return prompt.RequestTokenBudget{}, output.ContextCompactionStage{}, err
	}
	retainedTokens, protectedTokens, err := compactionTailTokens(ctx, req, state, params.retainedMessages, tail)
	if err != nil {
		return prompt.RequestTokenBudget{}, output.ContextCompactionStage{}, err
	}
	model := compactionSummaryRequest(req, params.summaryReq).ResolvedModel.BackendModelID
	summaryTokens, err := provider.EstimateMessageTokens(ctx, model, provider.Message{Role: provider.MessageRoleUser, Content: summaryText})
	if err != nil {
		return prompt.RequestTokenBudget{}, output.ContextCompactionStage{}, fmt.Errorf("estimate compaction summary tokens: %w", err)
	}
	return latestFit, output.ContextCompactionStage{
		Stage:           string(params.mode),
		RetainedUnits:   len(splitCompactionTurns(params.retainedMessages)),
		SourceTokens:    sourceTokens,
		SummaryTokens:   summaryTokens,
		RetainedTokens:  retainedTokens,
		ProtectedTokens: protectedTokens,
	}, nil
}

func compactionCurrentFit(ctx context.Context, req RunRequest, state RunState, beforeFit *prompt.RequestTokenBudget) (prompt.RequestTokenBudget, error) {
	if beforeFit != nil {
		return *beforeFit, nil
	}
	return fitConversationState(ctx, req, state)
}

// countSummarizedMessages counts the source messages a stage folds into its
// summary, ignoring prior summary messages that are replaced rather than
// summarized and the synthetic active-skill replay that is re-injected rather
// than folded.
func countSummarizedMessages(messages []Message, active []prompt.SkillBlock) int {
	replay, hasReplay := replayedSkillMessage(active)
	count := 0
	for _, message := range messages {
		if message.Role == MessageRoleSummary {
			continue
		}
		if hasReplay && message.Role == replay.Role && message.Content == replay.Content {
			continue
		}
		count++
	}
	return count
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
	msg = stampMessageTimestamp(msg)
	state.Conversation = append(state.Conversation, msg)
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{msg})
}
