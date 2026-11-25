package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// errSegmentSourceEmpty reports a segmented compaction whose summarized source
// holds no non-summary message. A segment with SourceMessageCount 0 would
// invalidate the chain on the next scan, so the builder refuses instead of
// storing one.
var errSegmentSourceEmpty = errors.New("segmented compaction source has no non-summary messages")

// errSegmentAppendChainInvalid reports an append build without a valid scanned
// chain. Appending clones the scanned segments, so a broken chain must rebase.
var errSegmentAppendChainInvalid = errors.New("segmented append requires a valid segment chain")

// segmentStateInput carries the inputs buildSegmentCompactionState needs. The
// history note is computed by the caller, for the source it selected, so the
// builder performs no archive write and a stage keeps one Note call.
type segmentStateInput struct {
	// Scan is the chain read from the conversation this compaction replaces.
	Scan segmentChainScan
	// Rebase writes a fresh sequence 1 segment instead of appending to Scan.
	Rebase bool
	// Source is the span the new segment covers. Its non-summary messages
	// become the segment's SourceMessageCount.
	Source []Message
	// Summary is the summarizer body, before header, history note and request
	// block handling.
	Summary string
	// HistoryNote is the archive note for Source. It goes to the tail only.
	HistoryNote string
	// RequestBlock is the verbatim latest-request block for the tail. Empty
	// omits it.
	RequestBlock string
	// Retained is the retained tail, before skill stripping and replay.
	Retained []Message
}

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
// active skill replay counts zero and the decision rebases as empty-span. It
// reports false when the boundary does not line up with the scanned slice.
func segmentAppendSourceCount(state RunState, messages []Message, scan segmentChainScan, retentionStart int) (int, bool) {
	if retentionStart < scan.End || retentionStart > len(messages) {
		return 0, false
	}
	return countSummarizedMessages(messages[scan.End:retentionStart], activeSkillBlocks(state)), true
}

// buildSegmentCompactionState returns the state of a segmented compaction: a
// chain of immutable segments plus a mutable tail, followed by the retained
// messages. Append clones the scanned segments verbatim; rebase starts a new
// chain at sequence 1. Only the tail carries the history note and the latest
// request block, and an empty tail is omitted. The retained handling matches
// buildSummarizedCompactionState (skill blocks stripped, active skills
// replayed), so the two paths persist the same tail.
//
// It returns errSegmentSourceEmpty rather than storing a segment with
// SourceMessageCount 0, and errSegmentAppendChainInvalid for an append whose
// chain did not validate. Callers treat both as a reason to rebase or fail.
func buildSegmentCompactionState(state RunState, in segmentStateInput) (RunState, error) {
	count := countSummarizedMessages(in.Source, activeSkillBlocks(state))
	if count == 0 {
		return RunState{}, errSegmentSourceEmpty
	}

	sequence := 1
	var segments []Message
	if !in.Rebase {
		if in.Scan.Status != chainValid {
			return RunState{}, errSegmentAppendChainInvalid
		}
		sequence = len(in.Scan.Segments) + 1
		segments = in.Scan.Segments
	}

	prefix := make([]Message, 0, len(segments)+2)
	prefix = append(prefix, cloneMessages(segments)...)
	prefix = append(prefix, Message{
		Role:    MessageRoleSummary,
		Content: segmentHeader(sequence) + segmentBody(in.Summary),
		Segment: &SummarySegment{Sequence: sequence, SourceMessageCount: count},
	})
	if tail := withRequestBlock(in.HistoryNote, in.RequestBlock); tail != "" {
		prefix = append(prefix, Message{Role: MessageRoleSummary, Content: tail})
	}

	retained := stripRetainedSkillBlocks(cloneMessages(in.Retained))
	if replay, ok := replayedSkillMessage(activeSkillBlocks(state)); ok {
		retained = append([]Message{replay}, retained...)
	}

	next := state.Clone()
	next.Lineage = state.Lineage.WithNewGeneration(prefix, retained)
	next.Conversation = next.Lineage.FullMessages()
	return next, nil
}

// segmentBody returns the storing form of a segment summary: the stored body
// without its latest-request trailer and history notes, trimmed. Stale notes
// and blocks belong to the regenerated tail, never to an immutable segment.
func segmentBody(summary string) string {
	body, _, _ := SplitRequestBlock(summary)
	return strings.TrimSpace(StripHistoryNotes(body))
}

// Reasons that select the plain summary fallback instead of a segment. The
// decision log prints both with action plain.
const (
	// segmentReasonEmptySource marks a rebase whose selected source holds only
	// summary messages.
	segmentReasonEmptySource = "empty-source"
	// segmentReasonRetentionUnmapped marks a stage whose retained boundary could
	// not be proved against the scanned conversation.
	segmentReasonRetentionUnmapped = "retention-unmapped"
)

// segmentStageInput carries what one compaction stage needs to decide and
// describe its segmented shape.
type segmentStageInput struct {
	Policy SegmentedCompactionPolicy
	// Full is the conversation this compaction replaces. The chain is scanned
	// from its leading summary messages.
	Full []Message
	// Source is the stage's selected source, a prefix of Full.
	Source []Message
	// Retained is the selected retained tail, a suffix of Full.
	Retained []Message
	Mode     prompt.CompactionMode
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
	// Plain reports the plain summary fallback: no segment metadata is stored.
	Plain bool
	// Source is the span the new segment covers: the append span, or the whole
	// stage source for a rebase and a plain fallback.
	Source []Message
	// Note is the history note for Source, stored in the tail only.
	Note string
	// Tail is the mutable tail text: Note plus the request block.
	Tail string
	// Recall is Source without its latest-request trailers, for the history
	// archive and recall markers.
	Recall []Message
	// pendingArchive holds the sources to archive once the outcome is adopted.
	pendingArchive [][]Message
}

// newSegmentStage decides the segmented shape of one stage. It scans the
// conversation the stage replaces, maps the retained boundary, estimates chain
// tokens and the conservative post-append prompt, and turns that into a
// decision. A rebase whose source has no non-summary coverage becomes the plain
// summary fallback, so no segment is ever stored with SourceMessageCount 0 and
// no append is split at a guessed index.
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
		appendCount, _ = segmentAppendSourceCount(state, in.Full, scan, retentionStart)
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
	if !mapped {
		decision = segmentPlainDecision(segmentReasonRetentionUnmapped, decision)
	}

	stage := segmentStage{Scan: scan, Decision: decision}
	if segmentPlainReason(decision.Reason) {
		stage.Plain = true
		stage.Source = cloneMessages(in.Source)
	} else {
		if decision.Rebase {
			stage.Source = cloneMessages(in.Source)
		} else {
			stage.Source = cloneMessages(in.Full[scan.End:retentionStart])
		}
		if decision.Rebase && countSummarizedMessages(stage.Source, activeSkillBlocks(state)) == 0 {
			stage.Decision = segmentPlainDecision(segmentReasonEmptySource, decision)
			stage.Plain = true
		}
	}

	stage.Recall = stripRequestBlocks(stage.Source)
	stage.Note, stage.pendingArchive = segmentHistoryNote(req, stage.Recall)
	stage.Tail = withRequestBlock(stage.Note, in.RequestBlock)
	return stage, nil
}

// segmentPlainDecision turns decision into the plain summary fallback: no chain
// is written and the flag-off state builder stores one summary message. Rebase
// stays set so no lookup treats the fallback as an append.
func segmentPlainDecision(reason string, decision segmentDecision) segmentDecision {
	decision.Rebase = true
	decision.Reason = reason
	decision.Sequence = 0
	return decision
}

// segmentPlainReason reports whether a decision reason selects the plain
// summary fallback rather than a segment chain.
func segmentPlainReason(reason string) bool {
	return reason == segmentReasonEmptySource || reason == segmentReasonRetentionUnmapped
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

// segmentHistoryNote returns the history note for source and the sources to
// archive once the outcome is adopted. It shares withHistoryNote's rendering and
// warning, so a stage stores one note for one archive write.
func segmentHistoryNote(req RunRequest, source []Message) (string, [][]Message) {
	if req.HistoryArchive == nil {
		return "", nil
	}
	noted, pending := withHistoryNote(req, "", source)
	return strings.TrimPrefix(noted, "\n\n"), pending
}

// segmentNoteText joins a summary body and its history note, omitting either
// part when empty. The plain fallback copies withHistoryNote exactly instead, so
// it keeps today's bytes.
func segmentNoteText(body, note string) string {
	switch {
	case note == "":
		return body
	case body == "":
		return note
	default:
		return body + "\n\n" + note
	}
}

// stripTailRequestBlocks returns a copy of messages with the latest-request
// trailer removed from summary messages that carry no Segment metadata. Segment
// text is immutable, so it is never rewritten. It reports whether any tail
// carried a trailer.
func stripTailRequestBlocks(messages []Message) ([]Message, bool) {
	out := cloneMessages(messages)
	stripped := false
	for i := range out {
		if out[i].Role != MessageRoleSummary || out[i].Segment != nil {
			continue
		}
		if body, _, ok := SplitRequestBlock(out[i].Content); ok {
			out[i].Content = body
			stripped = true
		}
	}
	return out, stripped
}

// dropTailRequestBlockState rewrites the latest generation's mutable tail to
// its body without the latest-request trailer, reporting the rewritten text.
// Segments keep their bytes; only a summary with no Segment metadata changes.
func dropTailRequestBlockState(state RunState) (RunState, string, bool) {
	next := state.Clone()
	if len(next.Lineage.Generations) == 0 {
		return state, "", false
	}
	latest := &next.Lineage.Generations[len(next.Lineage.Generations)-1]
	summaryText := ""
	dropped := false
	for i := range latest.SummaryPrefix {
		message := &latest.SummaryPrefix[i]
		if message.Role != MessageRoleSummary || message.Segment != nil {
			continue
		}
		if body, _, ok := SplitRequestBlock(message.Content); ok {
			message.Content = body
			summaryText = body
			dropped = true
		}
	}
	if !dropped {
		return state, "", false
	}
	next.Conversation = next.Lineage.FullMessages()
	return next, summaryText, true
}
