package agent

import (
	"context"
	"errors"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

type summarizeCompactor struct{}

type summarizeCompactionStageParams struct {
	sourceMessages   []Message
	retainedMessages []Message
	mode             prompt.CompactionMode
	maxTokens        int
	steering         string
	// segment is the decided segmented shape of the stage. Nil with the
	// segmented policy off.
	segment *segmentStage
	// summaryReq is the request the summary call runs on. Nil means main.
	summaryReq *RunRequest
	// announce emits the selected fallback event once the fit check passes. Nil on main.
	announce *compactionFallbackAnnouncement
}

func (summarizeCompactor) Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, steerings ...string) (CompactionOutcome, error) {
	steering := ""
	if len(steerings) > 0 {
		steering = steerings[0]
	}
	return twoStageSummarizeCompaction(ctx, req, state, turn, candidate, steering)
}

func twoStageSummarizeCompaction(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, steering string) (CompactionOutcome, error) {
	// Stamp elision before any fallback copy exists: whichever target writes the
	// summary, the retained tail is judged by the consuming provider and the
	// main request's estimator.
	req = withCompactionElision(req)
	return compactWithFallback(ctx, req, state, turn, func(summaryReq *RunRequest, announce *compactionFallbackAnnouncement) (CompactionOutcome, error) {
		return summarizeCompactionStages{
			stageRunner: func(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
				params.steering = steering
				params.announce = announce
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
	summaryBudget := compactionSummaryRequest(req, s.summaryReq).ModelBudget
	normalOutcome, err := s.normalStage(ctx, req, state, turn, candidate, fullMessages, retentionBase, summaryBudget)
	if err != nil {
		return compactionFailedOutcome(normalOutcome, err)
	}
	if !normalOutcome.Applied {
		return s.emergencyAfterNormal(ctx, req, state, turn, candidate, normalOutcome, fullMessages, retentionBase, summaryBudget)
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

	postNormalMessages := normalOutcome.State.Conversation
	emergencyRetentionBase := normalOutcome.State.Lineage.SummaryPrefixStrippedMessages()
	if len(emergencyRetentionBase) == 0 {
		emergencyRetentionBase = cloneMessages(postNormalMessages)
	}
	emergencySource, emergencyRetained, err := selectEmergencyRetention(ctx, req, normalOutcome.State, postNormalMessages, emergencyRetentionBase)
	if err != nil {
		// No emergency stage runs when its tail is too large, so the normal result is the only retry.
		if dropped, ok := s.adoptWithoutRequestBlock(ctx, req, normalOutcome, err); ok {
			return dropped, nil
		}
		return compactionFailedOutcome(normalOutcome, err)
	}
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
		outcome.Usage, outcome.CacheReportedInputTokens = sumCompactionUsage(normalOutcome, outcome)
		outcome.SummarizedMessages += normalOutcome.SummarizedMessages
		outcome.pendingArchive = append(normalOutcome.pendingArchive, outcome.pendingArchive...)
		outcome.pendingRecall = append(normalOutcome.pendingRecall, outcome.pendingRecall...)
		outcome.StageSizes = append(normalOutcome.StageSizes, outcome.StageSizes...)
		return outcome, nil
	}
	// A kept block can push a small window back over the compaction threshold.
	// Dropping it loses the verbatim request, not the summary, so retry that
	// before failing the whole compaction, but only after a budget failure:
	// any other error stays an error.
	if dropped, ok := s.adoptWithoutRequestBlock(ctx, req, outcome, err); ok {
		dropped.StageCount = 2
		dropped.Usage, dropped.CacheReportedInputTokens = sumCompactionUsage(normalOutcome, dropped)
		dropped.SummarizedMessages += normalOutcome.SummarizedMessages
		dropped.pendingArchive = append(normalOutcome.pendingArchive, dropped.pendingArchive...)
		dropped.pendingRecall = append(normalOutcome.pendingRecall, dropped.pendingRecall...)
		dropped.StageSizes = append(normalOutcome.StageSizes, dropped.StageSizes...)
		return dropped, nil
	}
	if dropped, ok := s.adoptWithoutRequestBlock(ctx, req, normalOutcome, err); ok {
		// Either attempt may have retired the main generation's backend state.
		dropped.BackendStateCleared = dropped.BackendStateCleared || outcome.BackendStateCleared
		if outcome.Applied {
			// The emergency call ran and its summary was discarded, but its tokens
			// were spent and its stage escalated the compaction count.
			dropped.StageCount = 2
			dropped.Usage, dropped.CacheReportedInputTokens = sumCompactionUsage(dropped, outcome)
			dropped.StageSizes = append(dropped.StageSizes, outcome.StageSizes...)
		}
		return dropped, nil
	}
	outcome.StageSizes = append(normalOutcome.StageSizes, outcome.StageSizes...)
	outcome, err = compactionFailedOutcome(outcome, err)
	return outcome, err
}

// emergencyAfterNormal runs the emergency stage when the normal stage applied
// nothing. It is split out of run to keep run's cyclomatic complexity in bounds.
func (s summarizeCompactionStages) emergencyAfterNormal(
	ctx context.Context,
	req RunRequest,
	state RunState,
	turn int,
	candidate ConversationCandidate,
	normalOutcome CompactionOutcome,
	full, base []Message,
	summaryBudget prompt.ModelTokenBudget,
) (CompactionOutcome, error) {
	if normalOutcome.BackendStateCleared {
		// The normal summary retired the latest generation's backend state
		// before applying nothing, so the emergency stage must not issue its
		// summary against that retired state.
		state.Lineage = state.Lineage.WithoutLatestBackendState()
	}
	emergencySource, emergencyRetained, err := selectEmergencyRetention(ctx, req, state, full, base)
	if err != nil {
		return compactionFailedOutcome(normalOutcome, err)
	}
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
		return outcome, nil
	}
	if dropped, ok := s.adoptWithoutRequestBlock(ctx, req, outcome, err); ok {
		dropped.StageCount = 1
		return dropped, nil
	}
	outcome, err = compactionFailedOutcome(outcome, err)
	return outcome, err
}

// normalStage runs the normal stage on the newest normal tail that fits. With no
// such tail it returns a zero outcome and makes no model call, so the caller falls
// through to the emergency stage, as it does after a not-applied normal stage.
func (s summarizeCompactionStages) normalStage(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, full, base []Message, summaryBudget prompt.ModelTokenBudget) (CompactionOutcome, error) {
	source, retained, ok, err := selectRetention(ctx, req, state, full, base, prompt.CompactionModeNormal, normalCompactionRetainTurns)
	if err != nil || !ok {
		return CompactionOutcome{}, err
	}
	return s.stageRunner(ctx, req, state, turn, candidate, summarizeCompactionStageParams{
		sourceMessages:   source,
		retainedMessages: retained,
		mode:             prompt.CompactionModeNormal,
		maxTokens:        compactionSummaryMaxTokensForMode(summaryBudget, prompt.CompactionModeNormal),
		summaryReq:       s.summaryReq,
	})
}

// runStageAndFit runs a single compaction stage and verifies it applied. An
// applied stage that still needs emergency compaction is returned with its
// state and the error, so a caller can retry it without the request block.
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
		return compactionFailedOutcome(outcome, &compactionBudgetMissError{err: emergencyCompactionError(outcome.Fit)})
	}

	fit, err := s.fitRunner(ctx, req, outcome.State)
	if err != nil {
		return compactionFailedOutcome(outcome, err)
	}
	outcome.Fit = fit
	if needsEmergencyCompaction(fit) {
		return outcome, &compactionBudgetMissError{err: emergencyCompactionError(fit)}
	}

	return outcome, nil
}

// compactionBudgetMissError marks a stage failure caused by budget alone: an
// applied stage that still needs emergency compaction, a not-applied stage whose
// retained conversation does not hard-fit, or an emergency tail too large even
// without its block. The latest-request ladder
// retries only these failures without the block; every other failure, such as a
// provider, fallback, or fit error, propagates.
type compactionBudgetMissError struct{ err error }

func (e *compactionBudgetMissError) Error() string { return e.err.Error() }

func (e *compactionBudgetMissError) Unwrap() error { return e.err }

// isCompactionBudgetMiss reports whether err is a budget-only stage failure.
func isCompactionBudgetMiss(err error) bool {
	var miss *compactionBudgetMissError
	return errors.As(err, &miss)
}

// sumUsageStats returns the token accounting of two summary calls. A nil record
// adds nothing, and two nil records stay nil, so a stage that issued no call is
// not reported as usage.
func sumUsageStats(a, b *provider.UsageStats) *provider.UsageStats {
	if a == nil && b == nil {
		return nil
	}
	out := provider.UsageStats{}
	if a != nil {
		out = *a
	}
	if b != nil {
		out.PromptTokens += b.PromptTokens
		out.CompletionTokens += b.CompletionTokens
		out.TotalTokens += b.TotalTokens
		out.CacheCreationInputTokens += b.CacheCreationInputTokens
		out.CacheReadInputTokens += b.CacheReadInputTokens
		out.ReasoningTokens += b.ReasoningTokens
		out.CacheReported = out.CacheReported || b.CacheReported
		out.CostUSD = sumCostUSD(out.CostUSD, b.CostUSD)
	}
	return &out
}

// sumCompactionUsage returns the token accounting and reported-cache input
// total of two stage outcomes. A nil usage record adds nothing, matching
// sumUsageStats.
func sumCompactionUsage(a, b CompactionOutcome) (*provider.UsageStats, int) {
	return sumUsageStats(a.Usage, b.Usage), a.CacheReportedInputTokens + b.CacheReportedInputTokens
}

// sumCostUSD adds two provider-reported costs, treating a nil cost as zero and
// keeping nil only when neither side reported a cost.
func sumCostUSD(a, b *float64) *float64 {
	if a == nil && b == nil {
		return nil
	}
	var sum float64
	if a != nil {
		sum += *a
	}
	if b != nil {
		sum += *b
	}
	return &sum
}

// adoptWithoutRequestBlock retries an applied stage without its stored
// latest-request block: it rewrites the stored summary text, issues no model
// call, and refits with the stage's fit runner. It reports false when cause is
// not a budget failure, when the stage carries no block, or when the stage
// still needs emergency compaction once the block is gone.
func (s summarizeCompactionStages) adoptWithoutRequestBlock(ctx context.Context, req RunRequest, outcome CompactionOutcome, cause error) (CompactionOutcome, bool) {
	if !isCompactionBudgetMiss(cause) {
		return CompactionOutcome{}, false
	}
	if !outcome.Applied || outcome.RequestBlock == "" || outcome.RequestBlock == output.RequestBlockNone || outcome.RequestBlock == output.RequestBlockDropped {
		return CompactionOutcome{}, false
	}
	// A segmented segment keeps immutable bytes; a plain fallback must keep its
	// ordinary summary even when dropping the block empties its mutable tail.
	var state RunState
	var summaryText string
	var dropped bool
	switch {
	case outcome.segmentDecision == nil:
		state, summaryText, dropped = dropRequestBlockState(outcome.State)
	case segmentPlainReason(outcome.segmentDecision.Reason):
		state, summaryText, dropped = dropPlainSegmentRequestBlock(outcome.State, outcome.SummaryText)
	default:
		state, summaryText, dropped = dropTailRequestBlockState(outcome.State)
		if body, _, ok := SplitRequestBlock(outcome.SummaryText); ok {
			summaryText = body
		}
	}
	if !dropped {
		return CompactionOutcome{}, false
	}
	fit, err := s.fitRunner(ctx, req, state)
	if err != nil || needsEmergencyCompaction(fit) {
		return CompactionOutcome{}, false
	}
	outcome.State = state
	outcome.Fit = fit
	outcome.SummaryText = summaryText
	outcome.RequestBlock = output.RequestBlockDropped
	outcome.RequestBlockBytes = 0
	return outcome, true
}

// compactionStageRetention strips skill blocks and elides old tool output in the
// retained messages a stage stores, keeping the comments' intent: the emitted
// RetainedMessages are the retained source messages that were actually persisted,
// and elision markers are what the new generation freezes. recomputeFit refits the
// tail when it is not the one the stage already fit.
func compactionStageRetention(ctx context.Context, req RunRequest, state RunState, recomputeFit bool, fit prompt.RequestTokenBudget, retained []Message) ([]Message, prompt.RequestTokenBudget, error) {
	retained, elided := buildCompactedRetention(ctx, req, state, cloneMessages(retained))
	if !elided && !recomputeFit {
		return retained, fit, nil
	}
	// The stage's token attribution measures the tail the generation keeps.
	refit, err := fitConversationState(ctx, req, state.WithConversation(retained))
	if err != nil {
		return nil, prompt.RequestTokenBudget{}, err
	}
	return retained, refit, nil
}

// stageTruncationMarker returns the marker a truncated summary carries. It
// follows the history note and precedes the request block.
func stageTruncationMarker(truncated bool) string {
	if !truncated {
		return ""
	}
	return "\n\n" + summaryTruncatedMarker
}

// compactionStagePlan builds the summary plan for a stage. The plan normally
// replays the source exactly as main projects it, carried latest-request block
// included, so the summarizer shares main's cached prefix. When that plan does
// not fit and the source carries a block, it rebuilds the plan once without the
// blocks: dropping the block is the only way a tight window can still
// summarize, because the block is what pushed the request over.
func compactionStagePlan(ctx context.Context, req RunRequest, state RunState, candidate ConversationCandidate, params summarizeCompactionStageParams) (compactionExecutionPlan, bool, error) {
	plan, ok, err := buildCompactionExecutionPlanWithMode(ctx, req, state, candidate, params)
	if err != nil || ok {
		return plan, ok, err
	}
	strippedMessages, hasBlock := stripRequestBlockSource(params.sourceMessages)
	if !hasBlock {
		return plan, false, nil
	}
	strippedParams := params
	strippedParams.sourceMessages = strippedMessages
	strippedPlan, ok, err := buildCompactionExecutionPlanWithMode(ctx, req, state, candidate, strippedParams)
	if err != nil || !ok {
		return plan, false, err
	}
	return strippedPlan, true, nil
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
