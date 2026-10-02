package agent

import (
	"slices"
	"strconv"
	"sync/atomic"

	"github.com/luispabon/steiner/internal/provider"
)

var toolCallSeq atomic.Uint64

// withToolCallIDs returns calls with every empty ID replaced by
// call_<process nonce>_<seq>. OpenAI-compatible servers may omit tool-call
// ids, yet tool results and delegation occurrences pair on them. The ids are
// assigned once, before the assistant message enters history, so they persist
// rather than vary per request. The input slice is copied, not mutated.
func withToolCallIDs(calls []provider.ToolCall) []provider.ToolCall {
	if !slices.ContainsFunc(calls, func(c provider.ToolCall) bool { return c.ID == "" }) {
		return calls
	}
	out := slices.Clone(calls)
	for i := range out {
		if out[i].ID == "" {
			out[i].ID = "call_" + toolBatchNonce + "_" + strconv.FormatUint(toolCallSeq.Add(1), 10)
		}
	}
	return out
}
