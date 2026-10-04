package tui

import (
	"strings"

	"charm.land/glamour/v2"
)

// renderCacheWidths bounds how many viewport widths a segment keeps a render
// for: the active width plus renderCacheWidths-1 recently used alternates. A
// sidebar toggle alternates between two widths; the third absorbs a terminal
// resize or the sidebar auto-hide threshold without evicting either.
const renderCacheWidths = 3

// glamourPoolSize bounds the pooled glamour renderers. Each viewport width
// renders markdown at two target widths (assistant prose and the narrower
// user-prompt card), so the pool holds two per cached viewport width.
const glamourPoolSize = 2 * renderCacheWidths

// renderStamp records render inputs that live outside a segment's own data and
// change without dirtying it: renderEpoch, and the delegation body cap, which
// follows the viewport height and is read only by delegation cards.
type renderStamp struct {
	epoch    int
	delegCap int
}

// widthRender is a segment's render at one viewport width.
type widthRender struct {
	width    int
	stamp    renderStamp
	rendered string
}

// widthRenders holds a segment's renders at recently used inactive widths,
// most recent first; an empty rendered string marks a free slot. It is an
// array, not a slice, so segment copies never share alternates.
type widthRenders [renderCacheWidths - 1]widthRender

func (b *contentBuffer) currentRenderStamp() renderStamp {
	return renderStamp{epoch: b.renderEpoch, delegCap: b.maxDelegationBodyLines}
}

// stampCurrent reports whether a render stamped s for a segment of kind still
// reflects the buffer's out-of-segment render inputs.
func (b *contentBuffer) stampCurrent(kind contentSegmentKind, s renderStamp) bool {
	return s.epoch == b.renderEpoch && (!isDelegationSegment(kind) || s.delegCap == b.maxDelegationBodyLines)
}

// renderAtWidth returns seg's render at width when its active render is for
// another width or stale. A clean segment reuses a current render cached for
// width and keeps its active render as an alternate; a dirty segment drops
// every alternate and renders afresh. fresh reports whether it rendered.
func (b *contentBuffer) renderAtWidth(seg *contentSegment, width int) (r widthRender, fresh bool) {
	if seg.renderDirty {
		seg.altRenders = widthRenders{}
	} else {
		hit, ok := b.takeAltRender(seg, width)
		if seg.cachedRender != "" && b.stampCurrent(seg.kind, seg.cachedStamp) && !segmentHasActiveToolCall(seg) {
			copy(seg.altRenders[1:], seg.altRenders[:len(seg.altRenders)-1])
			seg.altRenders[0] = widthRender{width: seg.cachedRenderWidth, stamp: seg.cachedStamp, rendered: seg.cachedRender}
		}
		if ok {
			return hit, false
		}
	}
	rendered := strings.TrimRight(b.renderSegment(*seg, width), "\n")
	return widthRender{width: width, stamp: b.currentRenderStamp(), rendered: rendered}, true
}

// segmentHasActiveToolCall reports whether seg shows a running tool call, whose
// render reads the clock (elapsed time) and tick counter (approval pulse), so
// it must not be reused at another width later. Active delegations and running
// compactions read the clock too but are re-rendered every frame, which drops
// their alternates.
func segmentHasActiveToolCall(seg *contentSegment) bool {
	switch seg.kind {
	case segmentToolCall:
		return seg.toolData != nil && seg.toolData.active
	case segmentToolCallGroup:
		if seg.toolGroupData == nil {
			return false
		}
		for _, tc := range seg.toolGroupData.entries {
			if tc != nil && tc.active {
				return true
			}
		}
	}
	return false
}

// takeAltRender removes and returns seg's current alternate render for width.
func (b *contentBuffer) takeAltRender(seg *contentSegment, width int) (widthRender, bool) {
	alts := &seg.altRenders
	for i, alt := range alts {
		if alt.rendered == "" || alt.width != width {
			continue
		}
		copy(alts[i:], alts[i+1:])
		alts[len(alts)-1] = widthRender{}
		return alt, b.stampCurrent(seg.kind, alt.stamp)
	}
	return widthRender{}, false
}

// glamourPool keeps width-bound glamour renderers for the most recently used
// target widths, most recent first, so moving between cached viewport widths
// and alternating assistant/user markdown does not rebuild renderers.
type glamourPool struct {
	slots []glamourSlot
}

type glamourSlot struct {
	renderer *glamour.TermRenderer
	width    int
}

// slot returns the slot for the glamour target width of a markdown block
// rendered at width, moved to the front. A new slot has a nil renderer for
// renderMarkdownBlock to fill; the least recently used slot is evicted when
// the pool is full.
func (p *glamourPool) slot(width int) *glamourSlot {
	target := max(1, width-markdownRenderPadding)
	i := 0
	for i < len(p.slots) && p.slots[i].width != target {
		i++
	}
	if i == len(p.slots) {
		if len(p.slots) < glamourPoolSize {
			p.slots = append(p.slots, glamourSlot{})
		}
		i = len(p.slots) - 1
		p.slots[i] = glamourSlot{width: target}
	}
	s := p.slots[i]
	copy(p.slots[1:i+1], p.slots[:i])
	p.slots[0] = s
	return &p.slots[0]
}
