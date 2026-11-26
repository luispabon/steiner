package agent

import (
	"fmt"
	"strings"
)

// SummarySegment records which source span an immutable summary segment covers.
type SummarySegment struct {
	Sequence           int `json:"sequence"`             // 1-based; 1 starts a chain
	SourceMessageCount int `json:"source_message_count"` // non-summary messages summarized
}

// segmentHeader prefixes a segment's summary text with its chain position.
func segmentHeader(seq int) string {
	return fmt.Sprintf("Segment %d. Later segments override earlier conflicting state.\n\n", seq)
}

type segmentChainStatus int

const (
	chainNone segmentChainStatus = iota
	chainValid
	chainInvalid
)

// segmentChainScan is the immutable segment chain read from the leading summary
// messages of a conversation slice, plus the mutable tail that follows it.
type segmentChainScan struct {
	Segments []Message // cloned leading segment messages, in order
	Tail     *Message  // cloned summary with Segment == nil directly after the segments, or nil
	End      int       // index of the first message after the leading summary run
	Status   segmentChainStatus
}

// scanSegmentChain reads the segment chain from the leading run of summary
// messages in messages. Callers pass ConversationGeneration.FullMessages, so the
// scan is identical whether the summaries sit in SummaryPrefix (persisted state)
// or in Messages (inside a run, after seeding flattened them). A chain is valid
// when sequences run 1..k with a positive SourceMessageCount and non-empty
// content, and at most one segment-free tail follows. Anything else fails closed
// so callers rebase instead of trusting the chain.
func scanSegmentChain(messages []Message) segmentChainScan {
	segments, end, status := scanSegmentChainShape(messages)
	switch status {
	case chainValid:
		scan := segmentChainScan{Segments: cloneMessages(messages[:segments]), End: end, Status: status}
		if segments < end {
			tail := cloneMessage(messages[end-1])
			scan.Tail = &tail
		}
		return scan
	case chainNone:
		if end == 0 {
			return segmentChainScan{Status: status}
		}
		tail := cloneMessage(messages[end-1])
		return segmentChainScan{Tail: &tail, End: end, Status: status}
	default:
		return segmentChainScan{End: end, Status: status}
	}
}

// scanSegmentChainShape validates the leading run of summary messages in
// messages without cloning. It returns the number of validated segments, the
// index of the first message after the run, and the chain status. A valid chain
// may be followed by one segment-free tail at index segments. Request projection
// reads chains on every model turn, so it takes the shape rather than a copy.
func scanSegmentChainShape(messages []Message) (segments, end int, status segmentChainStatus) {
	for end < len(messages) && messages[end].Role == MessageRoleSummary {
		end++
	}
	if end == 0 {
		return 0, 0, chainNone
	}
	segments = end
	if messages[end-1].Segment == nil {
		segments--
	}
	if segments == 0 {
		return 0, end, chainNone
	}
	for i, message := range messages[:segments] {
		segment := message.Segment
		if segment == nil || segment.Sequence != i+1 ||
			segment.SourceMessageCount <= 0 || strings.TrimSpace(message.Content) == "" {
			return 0, end, chainInvalid
		}
	}
	return segments, end, chainValid
}
