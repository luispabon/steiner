package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// segmentRetentionStart maps retained, the suffix selectRetention chose, to the
// index in fullMessages where those messages begin. Retention can be selected
// from a candidate view with the summary prefix stripped, so the boundary is
// derived from the exact scanned slice instead of the view's own length. It
// fails closed when retained is not the same messages as the equal-length
// suffix, so an ambiguous boundary never decides a chain split.
func segmentRetentionStart(fullMessages, retained []Message) (int, bool) {
	if len(retained) > len(fullMessages) {
		return 0, false
	}
	start := len(fullMessages) - len(retained)
	if !sameMessages(fullMessages[start:], retained) {
		return 0, false
	}
	return start, true
}

// segmentAppendSourceCount returns the number of messages the next segment
// would summarize in the append span messages[scan.End:retentionStart]. It
// counts the way buildSegmentCompactionState does, so a span holding only the
// active skill replay counts zero and the decision rebases as empty-span. skills
// is the state's active skill blocks, computed once by the caller. It reports
// false when the boundary does not line up with the scanned slice.
func segmentAppendSourceCount(skills []prompt.SkillBlock, messages []Message, scan segmentChainScan, retentionStart int) (int, bool) {
	if retentionStart < scan.End || retentionStart > len(messages) {
		return 0, false
	}
	return countSummarizedMessages(messages[scan.End:retentionStart], skills), true
}

// segmentStageInput carries what one compaction stage needs to decide and
// describe its segmented shape.
type segmentStageInput struct {
	Policy config.SegmentedCompactionPolicy
	// Full is the latest generation's FullMessages. The chain is scanned from
	// its leading summary messages, never from a candidate view.
	Full []Message
	// Source is the stage's selected source from the candidate view.
	Source []Message
	// Retained is the selected retained tail, a suffix of Full.
	Retained []Message
	// Skills is the state's active skill blocks, computed once by the caller.
	Skills []prompt.SkillBlock
	// StrippedView reports a candidate view without the summary prefix.
	StrippedView bool
	Mode         prompt.CompactionMode
	// Model estimates chain tokens.
	Model string
	// Budget is the fit that drives compaction, so ContextSize and
	// CompactionThresholdTokens match fit.ShouldCompact.
	Budget prompt.RequestTokenBudget
	// MaxTokens is the summary output budget the stage request sets.
	MaxTokens int
	// Retry reports a summarizer retry or a compaction fallback model, both of
	// which force a rebase.
	Retry bool
	// RequestBlock is the verbatim latest request the new tail carries.
	RequestBlock string
}

// segmentStage is the decided segmented shape of one compaction stage.
type segmentStage struct {
	Scan     segmentChainScan
	Decision segmentDecision
	// Source is the span the new segment covers: the append span, or the whole
	// source a rebase summarizes and stores.
	Source []Message
	// Retained overrides the selected tail when the chain forces the split, so no
	// chain message is left after the new summary.
	Retained []Message
	// ClampRetained reports that Retained replaces the selected tail.
	ClampRetained bool
	// Recall is Source without its latest-request trailers, for the history
	// archive and recall markers.
	Recall []Message
}

// segmentPlanInput carries what prepareSegmentPlan needs beyond ctx, req and
// state.
type segmentPlanInput struct {
	Candidate ConversationCandidate
	// RetainedFit is the fit of the selected retained tail.
	RetainedFit prompt.RequestTokenBudget
	// Skills is the state's active skill blocks, computed once by the caller.
	Skills []prompt.SkillBlock
	Params summarizeCompactionStageParams
	// RequestBlock is the verbatim latest request the stage carries.
	RequestBlock string
	// Retry asks for the shape a summarizer retry adopts.
	Retry bool
}

// segmentPlanPrep is a prepared segmented stage: the shape it decided, the
// params after that shape, the plan built from them and its fit verdict.
type segmentPlanPrep struct {
	Segment *segmentStage
	Params  summarizeCompactionStageParams
	Plan    compactionExecutionPlan
	OK      bool
}

// prepareSegmentPlan decides the segmented shape of a stage and builds its
// execution plan. A stage and its summarizer retry differ only in the retry
// flag, so both adopt the same shape rules and request.
func prepareSegmentPlan(ctx context.Context, req RunRequest, state RunState, in segmentPlanInput) (segmentPlanPrep, error) {
	segment, err := segmentStageFor(ctx, req, state, in.Candidate, in.RetainedFit, in.Skills, in.Params, in.RequestBlock, in.Retry)
	if err != nil {
		return segmentPlanPrep{}, err
	}
	params := in.Params
	applySegmentStage(&params, segment)
	plan, ok, err := compactionStagePlan(ctx, req, state, in.Candidate, params)
	if err != nil {
		return segmentPlanPrep{}, err
	}
	return segmentPlanPrep{Segment: segment, Params: params, Plan: plan, OK: ok}, nil
}

// buildSegmentRetryPlan rebuilds a stage plan for a summarizer retry, adopting
// the segmented shape that retry decides.
func buildSegmentRetryPlan(ctx context.Context, req RunRequest, state RunState, candidate ConversationCandidate, params summarizeCompactionStageParams, requestBlock string) (compactionExecutionPlan, *segmentStage, summarizeCompactionStageParams, error) {
	retainedFit, err := fitConversationState(ctx, req, state.WithConversation(params.retainedMessages))
	if err != nil {
		return compactionExecutionPlan{}, nil, summarizeCompactionStageParams{}, err
	}
	prep, err := prepareSegmentPlan(ctx, req, state, segmentPlanInput{
		Candidate:    candidate,
		RetainedFit:  retainedFit,
		Skills:       activeSkillBlocks(state),
		Params:       params,
		RequestBlock: requestBlock,
		Retry:        true,
	})
	if err != nil {
		return compactionExecutionPlan{}, nil, summarizeCompactionStageParams{}, err
	}
	if !prep.OK {
		return compactionExecutionPlan{}, nil, summarizeCompactionStageParams{}, errCompactionFallbackNotFit
	}
	return prep.Plan, prep.Segment, prep.Params, nil
}

// newSegmentStage decides the segmented shape of one stage. It scans the latest
// generation's conversation, maps the retained boundary, estimates chain tokens
// and the conservative post-append prompt, and turns that into a decision. A
// rebase whose source has no non-summary coverage becomes the plain summary
// fallback, so no segment is ever stored with SourceMessageCount 0.
func newSegmentStage(ctx context.Context, req RunRequest, state RunState, in segmentStageInput) (segmentStage, error) {
	scan := scanSegmentChain(in.Full)
	chainTokens, err := segmentChainTokens(ctx, in.Model, scan.Segments)
	if err != nil {
		return segmentStage{}, err
	}

	retentionStart, mapped := segmentRetentionStart(in.Full, in.Retained)
	appendCount := 0
	projected := 0
	if mapped {
		appendCount, _ = segmentAppendSourceCount(in.Skills, in.Full, scan, retentionStart)
		projected, err = segmentProjectedPostTokens(ctx, req, state, scan, in.Retained, in.RequestBlock, in.MaxTokens)
		if err != nil {
			return segmentStage{}, err
		}
	}

	decision := decideSegmentAction(segmentDecisionInput{
		Policy:               in.Policy,
		Chain:                scan,
		Mode:                 in.Mode,
		ContextSize:          in.Budget.ContextSize,
		CompactionThreshold:  in.Budget.CompactionThresholdTokens,
		ChainTokens:          chainTokens,
		ProjectedPostTokens:  projected,
		RetentionStart:       retentionStart,
		AppendSourceMessages: appendCount,
		Retry:                in.Retry,
	})

	stage := segmentStage{Scan: scan, Decision: decision}
	switch {
	case in.StrippedView && scan.Status != chainNone && !decision.Rebase:
		// Only an append is refused: the view omits the chain, so its source
		// cannot be split for an append and no segment may extend a chain it does
		// not show. Rebase over the stripped source, the projection the fallback
		// already uses. Emergency, retry and threshold rebases keep their own
		// reason.
		stage.Decision = segmentRebaseDecision(decision, segmentReasonStrippedView, 1)
		stage.Source = cloneMessages(in.Source)
	case !mapped:
		// The retained boundary cannot be proved, so rebase defensively over the
		// selected source and let the empty-source check below decide whether a
		// segment is stored. Only the split-in-chain decision takes this reason:
		// a valid chain makes an unmapped boundary read as a split at its start,
		// and every other rebase keeps its own reason, so an emergency, retry or
		// chain rebase is not relabelled.
		if decision.Reason == segmentReasonSplitInChain {
			stage.Decision = segmentRebaseDecision(decision, segmentReasonRetentionUnmapped, 1)
		}
		stage.Source = cloneMessages(in.Source)
	case !decision.Rebase:
		stage.Source = cloneMessages(in.Full[scan.End:retentionStart])
	default:
		stage.Source = cloneMessages(in.Source)
		if retentionStart < scan.End {
			// Retention starts inside the chain, so the new summary must cover the
			// whole chain and everything after it stays retained. The clamp keeps
			// the state valid and the stage refits the larger retained tail.
			stage.Source = cloneMessages(in.Full[:scan.End])
			stage.Retained = cloneMessages(in.Full[scan.End:])
			stage.ClampRetained = true
		}
	}
	if stage.Decision.Rebase && countSummarizedMessages(stage.Source, in.Skills) == 0 {
		stage.Decision = segmentPlainDecision(decision, segmentReasonEmptySource)
	}
	stage.Recall = stripRequestBlocks(stage.Source)
	return stage, nil
}

// segmentChainTokens sums the projected tokens of the immutable segments, so a
// budget rule measures what the chain costs a provider request.
func segmentChainTokens(ctx context.Context, model string, segments []Message) (int, error) {
	total := 0
	for _, segment := range segments {
		tokens, err := provider.EstimateMessageTokens(ctx, model, toProviderMessage(segment))
		if err != nil {
			return 0, fmt.Errorf("estimate segment tokens: %w", err)
		}
		total += tokens
	}
	return total, nil
}

// segmentProjectedPostTokens estimates the prompt after an append: the current
// segments, a tail holding the current note and this compaction's request
// block, and the retained messages, plus the summary output budget the stage
// request sets. Using the maximum output budget is conservative and biases
// toward a rebase on small budgets.
func segmentProjectedPostTokens(ctx context.Context, req RunRequest, state RunState, scan segmentChainScan, retained []Message, requestBlock string, maxTokens int) (int, error) {
	conversation := cloneMessages(scan.Segments)
	if tail := segmentTailPlaceholder(scan, requestBlock); tail != "" {
		conversation = append(conversation, Message{Role: MessageRoleSummary, Content: tail})
	}
	conversation = append(conversation, cloneMessages(retained)...)
	fit, err := fitConversationState(ctx, req, state.WithConversation(conversation))
	if err != nil {
		return 0, err
	}
	return fit.EstimatedPromptTokens + maxTokens, nil
}

// segmentTailPlaceholder returns the tail text an append would store for the
// projection: the note already on the current tail and this compaction's
// request block. Reusing the existing note keeps the estimate free of an archive
// call; the stage writes the note for the span it actually adopts.
func segmentTailPlaceholder(scan segmentChainScan, requestBlock string) string {
	note := ""
	if scan.Tail != nil {
		body, _, _ := SplitRequestBlock(scan.Tail.Content)
		note = strings.TrimSpace(body)
	}
	return withRequestBlock(note, requestBlock)
}
