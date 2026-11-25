package agent

import "fmt"

// SummarySegment records which source span an immutable summary segment covers.
type SummarySegment struct {
	Sequence           int `json:"sequence"`             // 1-based; 1 starts a chain
	SourceGenerationID int `json:"source_generation_id"` // generation whose messages this segment summarized
	SourceMessageCount int `json:"source_message_count"` // non-summary messages summarized
}

// segmentHeader prefixes a segment's summary text with its chain position.
func segmentHeader(seq int) string {
	return fmt.Sprintf("Segment %d. Later segments override earlier conflicting state.\n\n", seq)
}

// segmentChain splits a summary prefix into immutable segments and an optional
// mutable tail. A chain is valid when every prefix message is a summary, the
// first k >= 1 carry segments with sequences 1..k and strictly increasing source
// generations below gen.ID, and at most one segment-free tail follows. Anything
// else fails closed so callers rebase instead of trusting the prefix.
func segmentChain(gen ConversationGeneration) (segments []Message, tail *Message, ok bool) {
	prefix := gen.SummaryPrefix
	for _, message := range prefix {
		if message.Role != MessageRoleSummary {
			return nil, nil, false
		}
	}
	k := len(prefix)
	if k > 0 && prefix[k-1].Segment == nil {
		k--
	}
	if k == 0 {
		return nil, nil, false
	}
	previousSource := 0
	for i := 0; i < k; i++ {
		segment := prefix[i].Segment
		if segment == nil || segment.Sequence != i+1 {
			return nil, nil, false
		}
		if segment.SourceGenerationID <= previousSource || segment.SourceGenerationID >= gen.ID {
			return nil, nil, false
		}
		previousSource = segment.SourceGenerationID
	}
	segments = cloneMessages(prefix[:k])
	if k < len(prefix) {
		cloned := cloneMessage(prefix[k])
		tail = &cloned
	}
	return segments, tail, true
}
