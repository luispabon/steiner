package tui

// resetGroupRenderCaches drops the box and settled-row caches of every group in
// the buffer, so the next render draws the group from scratch.
func resetGroupRenderCaches(b *contentBuffer) {
	for i := range b.segments {
		if g := b.segments[i].delegGroupData; g != nil {
			g.box = groupBoxCache{}
			g.settled = settledGroupRows{}
		}
	}
}
