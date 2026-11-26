package provider

// markSegmentBoundaryBreakpoint places the segment-boundary breakpoint on
// boundary, unless that block is already marked, sits at or after the first
// volatile block (nil when nothing is volatile), or the budget is spent.
func markSegmentBoundaryBreakpoint(wire *anthropicRequest, boundary, volatile *anthropicBlockRef, numBreakpoints int, cacheControl *anthropicCacheControl) {
	if boundary.msg < 0 || boundary.msg >= len(wire.Messages) || numBreakpoints >= anthropicMaxCacheBreakpoints {
		return
	}
	content := wire.Messages[boundary.msg].Content
	if boundary.block < 0 || boundary.block >= len(content) || content[boundary.block].CacheControl != nil {
		return
	}
	if !blockBefore(*boundary, volatile) {
		return
	}
	content[boundary.block].CacheControl = cacheControl
}
