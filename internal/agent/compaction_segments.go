package agent

import (
	"errors"
	"strings"
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
