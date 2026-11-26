package provider

// markSegmentBoundaryBreakpoint places the segment-boundary breakpoint on the
// content block at (boundaryMsg, boundaryBlock), the tracked position of the
// last input message carrying Message.CacheBoundary. The block index matters
// because a coalesced tool-result batch puts several input messages in one wire
// message; the marker belongs on the marked tool result, which need not be the
// last block. It is called after the static and rolling placements, so an
// already marked block is left alone (dedupe) and an existing breakpoint is
// never moved or removed. A block at or after the first volatile block is
// skipped: its bytes change on the next request, so a cache entry written there
// would never be read back. The per-request breakpoint budget is respected.
func markSegmentBoundaryBreakpoint(wire *anthropicRequest, boundaryMsg, boundaryBlock, numBreakpoints int, cacheControl *anthropicCacheControl) {
	// Defensive guard: at most three breakpoints exist here, but keep the cap in case placements change.
	if boundaryMsg < 0 || boundaryMsg >= len(wire.Messages) || numBreakpoints >= anthropicMaxCacheBreakpoints {
		return
	}
	content := wire.Messages[boundaryMsg].Content
	if boundaryBlock < 0 || boundaryBlock >= len(content) || content[boundaryBlock].CacheControl != nil {
		return
	}
	if limitMsg, limitBlock, ok := firstVolatileBlock(wire.Messages); ok {
		if boundaryMsg > limitMsg || (boundaryMsg == limitMsg && boundaryBlock >= limitBlock) {
			return
		}
	}
	content[boundaryBlock].CacheControl = cacheControl
}
