package agent

import (
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// toRequestProviderMessages is the replay-safe conversion for a provider
// request. With segmented compaction enabled it additionally marks the last
// immutable summary segment, so the wire can place a cache breakpoint on the
// prefix that no longer changes. Estimation, diagnostics and snapshot callers
// stay on ToReplaySafeProviderMessages and never see the marker. With the flag
// off the projection is byte-identical to that unmarked conversion, even for a
// conversation that still carries segment metadata.
func toRequestProviderMessages(messages []Message, segmentedCompaction bool) []provider.Message {
	replay := ReplaySafeConversation(messages)
	out := ToProviderMessages(replay)
	if !segmentedCompaction {
		return out
	}
	scan := scanSegmentChain(replay)
	// Only a validated chain earns the boundary: a legacy or malformed chain
	// leaves the request unmarked.
	if scan.Status != chainValid {
		return out
	}
	out[len(scan.Segments)-1].CacheBoundary = true
	return out
}

// requestAssemblyOptions builds prompt assembly options for a provider request,
// so the projection marks the immutable segment boundary when segmented
// compaction is enabled for that request.
func requestAssemblyOptions(base prompt.AssemblyOptions, state RunState, segmentedCompaction bool) prompt.AssemblyOptions {
	return assemblyOptionsWith(base, state, func(messages []Message) []provider.Message {
		return toRequestProviderMessages(messages, segmentedCompaction)
	})
}
