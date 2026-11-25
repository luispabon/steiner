package provider

// markSegmentBoundaryBreakpoint places the segment-boundary breakpoint on the
// last content block of wire message boundaryMsg, the wire index of the last
// message carrying Message.CacheBoundary. It is called after the static and
// rolling placements, so an already marked block is left alone (dedupe) and an
// existing breakpoint is never moved or removed. A block at or after the first
// volatile block is skipped: its bytes change on the next request, so a cache
// entry written there would never be read back. The per-request breakpoint
// budget is respected.
func markSegmentBoundaryBreakpoint(wire *anthropicRequest, boundaryMsg, numBreakpoints int, cacheControl *anthropicCacheControl) int {
	if boundaryMsg < 0 || boundaryMsg >= len(wire.Messages) || numBreakpoints >= anthropicMaxCacheBreakpoints {
		return numBreakpoints
	}
	block := len(wire.Messages[boundaryMsg].Content) - 1
	if block < 0 || wire.Messages[boundaryMsg].Content[block].CacheControl != nil {
		return numBreakpoints
	}
	if limitMsg, limitBlock, ok := firstVolatileBlock(wire.Messages); ok {
		if boundaryMsg > limitMsg || (boundaryMsg == limitMsg && block >= limitBlock) {
			return numBreakpoints
		}
	}
	wire.Messages[boundaryMsg].Content[block].CacheControl = cacheControl
	return numBreakpoints + 1
}
