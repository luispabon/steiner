package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	source, retained, _, ok, err := selectRetention(ctx, req, state, full, base, prompt.CompactionModeNormal, normalCompactionRetainTurns)
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
// applied stage that still needs emergency compaction, or a not-applied stage
// whose retained conversation does not hard-fit. The latest-request ladder
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
	state, summaryText, dropped := dropRequestBlockState(outcome.State)
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

	requestBlock, requestBlockStatus := latestRequestBlock(params.sourceMessages, req.ModelBudget.ContextSize)

	plan, ok, err := compactionStagePlan(ctx, req, state, candidate, params)
	if err != nil {
		return CompactionOutcome{}, err
	}
	if !ok {
		if onFallback {
			return CompactionOutcome{}, errCompactionFallbackNotFit
		}
		return compactionNotAppliedOutcome(candidate, plan.fit, plan.promptText, params.mode, params.maxTokens), nil
	}

	// The fallback selection is announced only once its fit check passed. Nil
	// announce on main makes this a no-op.
	params.announce.announce()
	summary := compactionSummaryRequest(req, params.summaryReq)
	// Only a main summary retires the main generation's backend state, so only
	// it carries the request-scoped retirement signal. A fallback summary runs
	// on another provider's transcript.
	summaryCtx := ctx
	if !onFallback {
		summaryCtx = provider.WithBackendStateRetirement(ctx)
	}
	response, err := completeCompactionCall(summaryCtx, summary, turn, plan.request, summary.ModelBudget, plan.blocks)
	// A canceled or failed summary can lose its terminal chunk, so the response
	// alone does not report every retirement: read the request signal too.
	cleared := compactionSummaryRetired(summaryCtx, onFallback, response, err)
	if cleared {
		// An in-place summary retired the generation's live process, so the
		// generation's stored backend state no longer names a live transcript.
		state.Lineage = state.Lineage.withBackendState(state.Lineage.latestGenerationID(), nil, true)
	}
	if err != nil {
		return compactionFailedOutcome(CompactionOutcome{BackendStateCleared: cleared}, err)
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
		// The archive note must not re-quote the verbatim request. The summary
		// request itself keeps the block, because it replays this conversation
		// exactly as a main request projects it, so the shared prefix stays cached.
		summaryText, pendingArchive = withHistoryNote(req, summaryText, stripRequestBlocks(params.sourceMessages))
	}
	summaryText = withRequestBlock(summaryText, requestBlock)

	retained := cloneMessages(plan.retainedMessages)
	// buildSummarizedCompactionState strips skill blocks and drops skill-only
	// retained messages before persisting them. Apply the same transformation here
	// so the emitted RetainedMessages reflects the retained source messages that
	// were actually persisted; the synthetic active-skill re-injection that
	// buildSummarizedCompactionState prepends is not a retained source message.
	retained = stripRetainedSkillBlocks(retained)
	nextState := buildSummarizedCompactionState(state, summaryText, retained)
	latestFit, stage, err := fitAppliedStage(ctx, req, state, nextState, params, retainedFit, plan.fit.EstimatedPromptTokens, summaryText)
	if err != nil {
		return compactionFailedOutcome(CompactionOutcome{BackendStateCleared: cleared}, err)
	}
	logCompactionStageSizes(summary.CompactionLogPath, stage)

	return CompactionOutcome{
		State:                    nextState,
		Applied:                  true,
		Candidate:                candidate,
		Fit:                      latestFit,
		Mode:                     params.mode,
		SummaryTokenBudget:       params.maxTokens,
		SummarizedMessages:       countSummarizedMessages(params.sourceMessages, activeSkillBlocks(state)),
		RetainedMessages:         retained,
		SummaryText:              summaryText,
		PromptText:               plan.promptText,
		RequestBlock:             requestBlockStatus,
		RequestBlockBytes:        len(requestBlock),
		Usage:                    response.Usage,
		CacheReportedInputTokens: cacheReportedInputTokens(response.Usage),
		BackendStateCleared:      cleared,
		pendingArchive:           pendingArchive,
		StageSizes:               []output.ContextCompactionStage{stage},
	}, nil
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
