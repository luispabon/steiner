package tui

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// oldTranscriptJoin is the pre-WI-8e assembly in contentBuffer.String, kept
// verbatim as the oracle for transcriptBlocks.
func oldTranscriptJoin(prefix string, prefixLastKind contentSegmentKind, parts []string, kinds []contentSegmentKind, preview string) string {
	segmentJoin := joinWithUserMargin(parts, kinds)
	result := segmentJoin
	if prefix != "" && len(parts) > 0 {
		result = prefix + joinSeparator(prefixLastKind, kinds[0]) + segmentJoin
	} else if prefix != "" {
		result = prefix
	}
	if preview != "" {
		result = oldAppendStreamPreview(result, preview)
	}
	return result
}

func oldAppendStreamPreview(result, preview string) string {
	trimmed := strings.TrimRight(preview, "\n")
	if result == "" {
		return trimmed
	}
	return result + "\n" + trimmed
}

func TestTranscriptBlocksMatchOldJoin(t *testing.T) {
	texts := []string{"", "a", "a\nb", "\nlead", "trail\n", "\n", "x\n\n"}
	kindPool := []contentSegmentKind{-1, segmentUser, segmentUserMarkdown, segmentDelegation, segmentDelegationGroup, segmentToolCall, segmentAssistantMarkdown}
	for seed := range uint64(2000) {
		r := rand.New(rand.NewPCG(seed, 6))
		prefix := texts[r.IntN(len(texts))]
		prefixLastKind := kindPool[r.IntN(len(kindPool))]
		n := r.IntN(5)
		parts := make([]string, n)
		kinds := make([]contentSegmentKind, n)
		for i := range parts {
			parts[i] = texts[1+r.IntN(len(texts)-1)]
			kinds[i] = kindPool[r.IntN(len(kindPool))]
		}
		preview := texts[r.IntN(len(texts))]
		blocks := transcriptBlocks(prefix, prefixLastKind, parts, kinds, preview)
		if got, want := strings.Join(blocks, "\n"), oldTranscriptJoin(prefix, prefixLastKind, parts, kinds, preview); got != want {
			t.Fatalf("seed %d: transcriptBlocks(%q, %d, %q, %v, %q) joins to %q, want %q", seed, prefix, prefixLastKind, parts, kinds, preview, got, want)
		}
		if blocksJoinEmpty(blocks) != (strings.Join(blocks, "\n") == "") {
			t.Fatalf("seed %d: blocksJoinEmpty(%q) disagrees with the join", seed, blocks)
		}
	}
}
