package agent

import (
	"errors"
	"strings"

	"github.com/luispabon/steiner/internal/prompt"
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
	// Skills is the state's active skill blocks, computed once by the caller.
	Skills []prompt.SkillBlock
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
	count := countSummarizedMessages(in.Source, in.Skills)
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
	if replay, ok := replayedSkillMessage(in.Skills); ok {
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
	// withRequestBlock with no request neutralizes header-like text, so a
	// summarizer reply that quotes a trailer-shaped block keeps every byte of its
	// text and never stores a detectable request block. Nothing parses after that
	// sanitizing, because no header prefix survives it.
	return strings.TrimSpace(StripHistoryNotes(withRequestBlock(summary, "")))
}

// buildCompactionStageState stores the state a successful stage produces and
// returns the summary text reported for it. noted is the summary body with its
// history note, computed by stageHistoryNote for the source the stage replaces;
// note is that note alone. With no segment stage, or a plain segmented decision,
// the stored summary keeps the flag-off shape with the note and request block
// inline. A segmented stage stores the summarizer body as the immutable segment
// and moves the note and request block to the mutable tail. marker is the
// truncation marker, which follows the history note and precedes the request
// block.
func buildCompactionStageState(state RunState, segment *segmentStage, skills []prompt.SkillBlock, noted, note, marker, requestBlock string, retained []Message) (RunState, string, error) {
	if segment == nil || segment.Decision.Plain {
		stored := withRequestBlock(noted+marker, requestBlock)
		return buildSummarizedCompactionState(state, stored, retained), stored, nil
	}
	display := withRequestBlock(joinHistoryNote(StripHistoryNotes(noted+marker), note, separatorOmitEmpty), requestBlock)
	next, err := buildSegmentCompactionState(state, segmentStateInput{
		Scan:         segment.Scan,
		Rebase:       segment.Decision.Rebase,
		Source:       segment.Source,
		Skills:       skills,
		Summary:      noted + marker,
		HistoryNote:  note,
		RequestBlock: requestBlock,
		Retained:     retained,
	})
	if err != nil {
		return RunState{}, "", err
	}
	return next, display, nil
}
