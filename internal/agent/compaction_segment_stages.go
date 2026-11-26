package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

//nolint:gocyclo // Retry plan adoption adds a bounded result branch to the existing stage.
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

	retryParams := params
	var segment *segmentStage
	if req.SegmentedCompaction.Enabled {
		var err error
		segment, err = segmentStageFor(ctx, req, state, candidate, retainedFit, params, requestBlock, false)
		if err != nil {
			return CompactionOutcome{}, err
		}
		requestBlock, requestBlockStatus = applySegmentStage(req, &params, segment, requestBlock, requestBlockStatus)
	}

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
	// The retry ladder owns the retirement signal of each call, so this stage only
	// reads the cumulative result.
	calls, err := summarizeStageCalls(ctx, req, summary, turn, state, candidate, retryParams, requestBlock, plan, onFallback)
	cleared := calls.cleared
	if cleared {
		// An in-place summary retired the generation's live process, so the
		// generation's stored backend state no longer names a live transcript.
		state.Lineage = state.Lineage.withBackendState(state.Lineage.latestGenerationID(), nil, true)
	}
	if err != nil {
		return compactionFailedOutcome(CompactionOutcome{BackendStateCleared: cleared}, err)
	}
	if calls.segment != nil && calls.params != nil {
		segment = calls.segment
		params = *calls.params
		requestBlock, requestBlockStatus = latestRequestBlock(params.sourceMessages, req.ModelBudget.ContextSize)
	}
	if calls.plan != nil {
		plan = *calls.plan
	}
	summaryText := strings.TrimSpace(calls.response.Message.Content)
	marker := stageTruncationMarker(calls.truncated)

	retained, stageRetainedFit, err := compactionStageRetention(ctx, req, state, segment != nil && segment.ClampRetained, retainedFit, plan.retainedMessages)
	if err != nil {
		return compactionFailedOutcome(CompactionOutcome{BackendStateCleared: cleared}, err)
	}
	retainedFit = stageRetainedFit
	params.retainedMessages = retained

	removedSource := stripRequestBlocks(params.sourceMessages)
	nextState, summaryText, pendingArchive, err := buildCompactionStageState(req, state, segment, removedSource, summaryText, marker, requestBlock, retained)
	if err != nil {
		return compactionFailedOutcome(CompactionOutcome{BackendStateCleared: cleared}, err)
	}
	recall, summarized := stageRecallCount(state, segment, removedSource, params.sourceMessages)
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
		SummarizedMessages:       summarized,
		RetainedMessages:         retained,
		SummaryText:              summaryText,
		SummaryTruncated:         calls.truncated,
		SummaryCondensed:         calls.condensed,
		PromptText:               plan.promptText,
		RequestBlock:             requestBlockStatus,
		RequestBlockBytes:        len(requestBlock),
		Usage:                    calls.usage,
		CacheReportedInputTokens: calls.cacheReported,
		BackendStateCleared:      cleared,
		pendingArchive:           pendingArchive,
		pendingRecall:            recall,
		StageSizes:               []output.ContextCompactionStage{stage},
		segmentDecision:          segmentDecisionOf(segment),
	}, nil
}

func summarizeStageCalls(ctx context.Context, req, summary RunRequest, turn int, state RunState, candidate ConversationCandidate, retryParams summarizeCompactionStageParams, requestBlock string, plan compactionExecutionPlan, onFallback bool) (summaryCalls, error) {
	var builder compactionRetryPlanBuilder
	if req.SegmentedCompaction.Enabled && !onFallback {
		builder = func() (compactionExecutionPlan, *segmentStage, summarizeCompactionStageParams, error) {
			return buildSegmentRetryPlan(ctx, req, state, candidate, retryParams, requestBlock)
		}
	}
	return summarizeCompactionCallsWithRetryPlan(ctx, summary, turn, plan, onFallback, builder)
}

func dropPlainSegmentRequestBlock(state RunState, summaryText string) (RunState, string, bool) {
	next := state.Clone()
	if len(next.Lineage.Generations) == 0 {
		return state, "", false
	}
	latest := &next.Lineage.Generations[len(next.Lineage.Generations)-1]
	for i := len(latest.SummaryPrefix) - 1; i >= 0; i-- {
		message := &latest.SummaryPrefix[i]
		if message.Role != MessageRoleSummary || message.Segment != nil {
			continue
		}
		body, _, ok := SplitRequestBlock(message.Content)
		if !ok {
			continue
		}
		message.Content = body
		next.Conversation = next.Lineage.FullMessages()
		if display, _, hasBlock := SplitRequestBlock(summaryText); hasBlock {
			summaryText = display
		}
		return next, summaryText, true
	}
	return state, "", false
}

// applySegmentStage makes the decided stage the one the request and note use: a
// clamped retained tail replaces the selected one, and a rebase summarizes
// exactly the source it stores. It returns the request block of the final source.
func applySegmentStage(req RunRequest, params *summarizeCompactionStageParams, segment *segmentStage, requestBlock, requestBlockStatus string) (string, string) {
	params.segment = segment
	if segment.ClampRetained {
		params.retainedMessages = segment.Retained
	}
	if !segment.Decision.Rebase {
		return requestBlock, requestBlockStatus
	}
	params.sourceMessages = segment.Source
	return latestRequestBlock(params.sourceMessages, req.ModelBudget.ContextSize)
}

// segmentStageFor returns the decided segmented shape of a stage, or nil when
// the segmented policy is off. The chain is scanned from the latest generation's
// FullMessages, never from the candidate view, so a stripped view cannot hide an
// existing chain. It is split out of summarizeCompactionStage to keep that
// stage's branching bounded.
func segmentStageFor(ctx context.Context, req RunRequest, state RunState, candidate ConversationCandidate, retainedFit prompt.RequestTokenBudget, params summarizeCompactionStageParams, requestBlock string, retry bool) (*segmentStage, error) {
	if !req.SegmentedCompaction.Enabled {
		return nil, nil
	}
	full := state.Lineage.FullMessages()
	if len(full) == 0 {
		full = state.Conversation
	}
	decided, err := newSegmentStage(ctx, req, state, segmentStageInput{
		Policy:       req.SegmentedCompaction,
		Full:         full,
		Source:       params.sourceMessages,
		Retained:     params.retainedMessages,
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
func stageRecallCount(state RunState, segment *segmentStage, removedSource, source []Message) ([]Message, int) {
	if segment == nil {
		return removedSource, countSummarizedMessages(source, activeSkillBlocks(state))
	}
	return segment.Recall, countSummarizedMessages(segment.Source, activeSkillBlocks(state))
}

// buildCompactionStageState stores the state a successful stage produces and
// returns the summary text reported for it. It runs only after the summarizer
// succeeded, so a failed call writes no history note. With no segment stage it is
// today's plain summary with the history note and request block inline. A
// segmented stage stores the summarizer body as the immutable segment and moves
// the note and request block to the mutable tail; a plain segmented decision keeps
// the flag-off shape with no segment metadata. marker is the truncation marker,
// which follows the history note and precedes the request block.
func buildCompactionStageState(req RunRequest, state RunState, segment *segmentStage, removedSource []Message, body, marker, requestBlock string, retained []Message) (RunState, string, [][]Message, error) {
	if segment != nil {
		// A segmented stage notes and archives only the span it replaces, never
		// the segments and tail it keeps.
		removedSource = segment.Recall
	}
	note := ""
	text := body
	var pendingArchive [][]Message
	if req.HistoryArchive != nil {
		// The archive note must not re-quote the verbatim request. The summary
		// request itself keeps the block, because it replays this conversation
		// exactly as a main request projects it, so the shared prefix stays cached.
		text, pendingArchive = withHistoryNote(req, body, removedSource)
		note = strings.TrimPrefix(strings.TrimPrefix(text, StripHistoryNotes(body)), "\n\n")
	}
	if segment == nil || segment.Plain {
		stored := withRequestBlock(text+marker, requestBlock)
		return buildSummarizedCompactionState(state, stored, retained), stored, pendingArchive, nil
	}
	display := withRequestBlock(segmentNoteText(StripHistoryNotes(body+marker), note), requestBlock)
	next, err := buildSegmentCompactionState(state, segmentStateInput{
		Scan:         segment.Scan,
		Rebase:       segment.Decision.Rebase,
		Source:       segment.Source,
		Summary:      body + marker,
		HistoryNote:  note,
		RequestBlock: requestBlock,
		Retained:     retained,
	})
	if err != nil {
		return RunState{}, "", nil, err
	}
	return next, display, pendingArchive, nil
}

// segmentDecisionOf returns the decision an adopted stage reports, or nil with
// the segmented policy off.
func segmentDecisionOf(segment *segmentStage) *segmentDecision {
	if segment == nil {
		return nil
	}
	return &segment.Decision
}
