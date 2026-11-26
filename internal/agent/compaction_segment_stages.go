package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

func summarizeCompactionStage(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
	onFallback := params.summaryReq != nil
	setup, err := setupCompactionStage(ctx, req, state, candidate, params)
	if err != nil {
		return CompactionOutcome{}, err
	}
	if !setup.retainedFit.Fits {
		return compactionNotAppliedOutcome(candidate, setup.retainedFit, fmt.Sprintf("%s mode=%s", summarizeCompactionPrompt(candidate), params.mode), params.mode, params.maxTokens), compactionCannotSolveError(setup.retainedFit)
	}
	if len(params.sourceMessages) == 0 {
		return compactionNotAppliedOutcome(candidate, setup.retainedFit, fmt.Sprintf("%s mode=%s no_source=true", summarizeCompactionPrompt(candidate), params.mode), params.mode, params.maxTokens), nil
	}
	if !setup.ok {
		if onFallback {
			return CompactionOutcome{}, errCompactionFallbackNotFit
		}
		return compactionNotAppliedOutcome(candidate, setup.plan.fit, setup.plan.promptText, params.mode, params.maxTokens), nil
	}

	// The fallback selection is announced only once its fit check passed. Nil
	// announce on main makes this a no-op.
	setup.params.announce.announce()
	summary := compactionSummaryRequest(req, setup.params.summaryReq)
	// The retry ladder owns the retirement signal of each call, so this stage only
	// reads the cumulative result.
	calls, err := summarizeStageCalls(ctx, req, summary, turn, state, candidate, setup.skills, params, setup.requestBlock, setup.plan, onFallback)
	if calls.cleared {
		// An in-place summary retired the generation's live process, so the
		// generation's stored backend state no longer names a live transcript.
		state.Lineage = state.Lineage.withBackendState(state.Lineage.latestGenerationID(), nil, true)
	}
	if err != nil {
		return compactionFailedOutcome(CompactionOutcome{BackendStateCleared: calls.cleared}, err)
	}
	return finishCompactionStage(ctx, req, state, candidate, setup, summary.CompactionLogPath, calls)
}

// compactionStageSetup is a stage ready to run: the retained tail fit, the
// effective params after any segmented shape, the decided segment, the active
// skill blocks, the plan and the request block the plan carries.
type compactionStageSetup struct {
	retainedFit        prompt.RequestTokenBudget
	params             summarizeCompactionStageParams
	segment            *segmentStage
	skills             []prompt.SkillBlock
	plan               compactionExecutionPlan
	requestBlock       string
	requestBlockStatus string
	ok                 bool
}

// setupCompactionStage fits the retained tail and builds the stage plan. A tail
// that cannot fit or a stage with no source to summarize reports ok false with
// its fit, so the caller maps it to the not-applied outcome without a plan.
func setupCompactionStage(ctx context.Context, req RunRequest, state RunState, candidate ConversationCandidate, params summarizeCompactionStageParams) (compactionStageSetup, error) {
	setup := compactionStageSetup{params: params}
	retainedFit, err := fitConversationState(ctx, req, state.WithConversation(params.retainedMessages))
	if err != nil {
		return compactionStageSetup{}, err
	}
	setup.retainedFit = retainedFit
	if !retainedFit.Fits || len(params.sourceMessages) == 0 {
		return setup, nil
	}
	// The skill blocks are computed once per stage: the state does not change
	// until the stage is adopted.
	setup.skills = activeSkillBlocks(state)
	setup.requestBlock, setup.requestBlockStatus = latestRequestBlock(params.sourceMessages, req.ModelBudget.ContextSize)
	if !req.SegmentedCompaction.Enabled {
		plan, ok, err := compactionStagePlan(ctx, req, state, candidate, params)
		if err != nil {
			return compactionStageSetup{}, err
		}
		setup.plan, setup.ok = plan, ok
		return setup, nil
	}
	prep, err := prepareSegmentPlan(ctx, req, state, segmentPlanInput{
		Candidate:    candidate,
		RetainedFit:  retainedFit,
		Skills:       setup.skills,
		Params:       params,
		RequestBlock: setup.requestBlock,
	})
	if err != nil {
		return compactionStageSetup{}, err
	}
	setup.params, setup.segment, setup.plan, setup.ok = prep.Params, prep.Segment, prep.Plan, prep.OK
	setup.requestBlock, setup.requestBlockStatus = latestRequestBlock(prep.Params.sourceMessages, req.ModelBudget.ContextSize)
	return setup, nil
}

// finishCompactionStage stores the adopted summary calls and returns the
// outcome the stage reports.
func finishCompactionStage(ctx context.Context, req RunRequest, state RunState, candidate ConversationCandidate, setup compactionStageSetup, logPath string, calls summaryCalls) (CompactionOutcome, error) {
	params := setup.params
	segment := setup.segment
	requestBlock, requestBlockStatus := setup.requestBlock, setup.requestBlockStatus
	if calls.adoption != nil {
		segment = calls.adoption.segment
		params = calls.adoption.params
		requestBlock, requestBlockStatus = latestRequestBlock(params.sourceMessages, req.ModelBudget.ContextSize)
	}
	if calls.plan != nil {
		setup.plan = *calls.plan
	}
	summaryText := strings.TrimSpace(calls.response.Message.Content)
	marker := stageTruncationMarker(calls.truncated)

	// The history note is written before the retained tail is refit, the order
	// the flag-off stage has always used, so a failed refit cannot skip the Note
	// call and its warning.
	removedSource := stripRequestBlocks(params.sourceMessages)
	if segment != nil {
		removedSource = segmentArchiveSource(segment)
	}
	noted, note, pendingArchive := stageHistoryNote(req, summaryText, removedSource)

	retained, retainedFit, err := compactionStageRetention(ctx, req, state, segment != nil && segment.ClampRetained, setup.retainedFit, setup.plan.retainedMessages)
	if err != nil {
		return compactionFailedOutcome(CompactionOutcome{BackendStateCleared: calls.cleared}, err)
	}
	params.retainedMessages = retained

	nextState, summaryText, err := buildCompactionStageState(state, segment, setup.skills, noted, note, marker, requestBlock, retained)
	if err != nil {
		return compactionFailedOutcome(CompactionOutcome{BackendStateCleared: calls.cleared}, err)
	}
	recall, summarized := stageRecallCount(setup.skills, segment, removedSource, params.sourceMessages)
	latestFit, stage, err := fitAppliedStage(ctx, req, state, nextState, params, retainedFit, setup.plan.fit.EstimatedPromptTokens, summaryText)
	if err != nil {
		return compactionFailedOutcome(CompactionOutcome{BackendStateCleared: calls.cleared}, err)
	}
	logCompactionStageSizes(logPath, stage)

	return CompactionOutcome{
		State:                    nextState,
		Applied:                  true,
		Candidate:                candidate,
		Fit:                      latestFit,
		Mode:                     params.mode,
		SummaryTokenBudget:       params.maxTokens,
		SummarizedMessages:       summarized,
		RetainedMessages:         retained,
		SummaryText:              summaryText,
		SummaryTruncated:         calls.truncated,
		SummaryCondensed:         calls.condensed,
		PromptText:               setup.plan.promptText,
		RequestBlock:             requestBlockStatus,
		RequestBlockBytes:        len(requestBlock),
		Usage:                    calls.usage,
		CacheReportedInputTokens: calls.cacheReported,
		BackendStateCleared:      calls.cleared,
		pendingArchive:           pendingArchive,
		pendingRecall:            recall,
		StageSizes:               []output.ContextCompactionStage{stage},
		segmentDecision:          segmentDecisionOf(segment),
	}, nil
}

func summarizeStageCalls(ctx context.Context, req, summary RunRequest, turn int, state RunState, candidate ConversationCandidate, skills []prompt.SkillBlock, retryParams summarizeCompactionStageParams, requestBlock string, plan compactionExecutionPlan, onFallback bool) (summaryCalls, error) {
	var builder retryPlanBuilder
	if req.SegmentedCompaction.Enabled && !onFallback {
		builder = func() (compactionExecutionPlan, *segmentStage, summarizeCompactionStageParams, error) {
			return buildSegmentRetryPlan(ctx, req, state, candidate, retryParams, requestBlock, skills)
		}
	}
	return summarizeCompactionCallsWithRetryPlan(ctx, summary, turn, plan, onFallback, builder)
}

// applySegmentStage makes the decided stage the one the request and note use: a
// clamped retained tail replaces the selected one, and a rebase summarizes
// exactly the source it stores. The caller derives the request block again after
// the source changes.
func applySegmentStage(params *summarizeCompactionStageParams, segment *segmentStage) {
	params.segment = segment
	if segment.ClampRetained {
		params.retainedMessages = segment.Retained
	}
	if segment.Decision.Rebase {
		params.sourceMessages = segment.Source
	}
}

// segmentStageFor returns the decided segmented shape of a stage. Callers gate
// on req.SegmentedCompaction.Enabled. The chain is scanned from the latest
// generation's FullMessages, never from the candidate view, so a stripped view
// cannot hide an existing chain. It is split out of summarizeCompactionStage to
// keep that stage's branching bounded.
func segmentStageFor(ctx context.Context, req RunRequest, state RunState, candidate ConversationCandidate, retainedFit prompt.RequestTokenBudget, skills []prompt.SkillBlock, params summarizeCompactionStageParams, requestBlock string, retry bool) (*segmentStage, error) {
	decided, err := newSegmentStage(ctx, req, state, segmentStageInput{
		Policy:       req.SegmentedCompaction,
		Full:         conversationForProjection(state),
		Source:       params.sourceMessages,
		Retained:     params.retainedMessages,
		Skills:       skills,
		StrippedView: candidate.View == ConversationViewSummaryPrefixStripped,
		Mode:         params.mode,
		Model:        req.ResolvedModel.BackendModelID,
		Budget:       retainedFit,
		MaxTokens:    params.maxTokens,
		Retry:        retry || params.summaryReq != nil,
		RequestBlock: requestBlock,
	})
	if err != nil {
		return nil, err
	}
	return &decided, nil
}

// stageRecallCount returns the messages the outcome archives for recall and the
// count the summary covers. A segmented stage reports its selected span; with
// the policy off it reports the whole source.
func stageRecallCount(skills []prompt.SkillBlock, segment *segmentStage, removedSource, source []Message) ([]Message, int) {
	if segment == nil {
		return removedSource, countSummarizedMessages(source, skills)
	}
	return segment.Recall, countSummarizedMessages(segment.Source, skills)
}

// segmentArchiveSource returns the source a segmented stage archives once it is
// adopted: the span it summarizes, preceded on an append by the previous tail,
// whose body still carries the older history pointers. The tail is stripped of
// its request block, the way the flag-off source is. A rebase covers the chain
// and tail it replaces in its own span.
func segmentArchiveSource(segment *segmentStage) []Message {
	if segment.Decision.Rebase || segment.Scan.Tail == nil {
		return segment.Recall
	}
	previous := stripRequestBlocks([]Message{*segment.Scan.Tail})
	return append(previous, segment.Recall...)
}

// stageHistoryNote returns body with its history note, the note text alone, and
// the source to archive once the outcome is adopted. It runs before the retained
// tail is refit, so a failed refit cannot skip the Note call and its warning.
func stageHistoryNote(req RunRequest, body string, removedSource []Message) (string, string, [][]Message) {
	if req.HistoryArchive == nil {
		return body, "", nil
	}
	// The archive note must not re-quote the verbatim request. The summary
	// request itself keeps the block, because it replays this conversation
	// exactly as a main request projects it, so the shared prefix stays cached.
	note, pendingArchive := historyNoteText(req, removedSource)
	return joinHistoryNote(StripHistoryNotes(body), note, separatorAlways), note, pendingArchive
}

// segmentDecisionOf returns the decision an adopted stage reports, or nil with
// the segmented policy off.
func segmentDecisionOf(segment *segmentStage) *segmentDecision {
	if segment == nil {
		return nil
	}
	return &segment.Decision
}
