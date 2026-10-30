package tui

import (
	"strings"
	"testing"
)

// rewalkGroups drops the cached render of every group segment, the settled
// prefix, and the joined blocks. The joined blocks would otherwise answer a clean
// buffer before any segment is walked. No segment is marked dirty, so no settled
// rows are flushed.
func rewalkGroups(b *contentBuffer) {
	for i := range b.segments {
		if b.segments[i].delegGroupData != nil {
			b.segments[i].cachedRender = ""
		}
	}
	b.gen++
	b.stringCacheBlocks = nil
}

// assertSettledCacheMatchesBypass re-walks the groups with their settled rows
// cache in place, then again with that cache dropped, and fails when the two
// outputs differ. It proves a cache hit matches a cache-bypassed render.
func assertSettledCacheMatchesBypass(t *testing.T, m *Model, width int) {
	t.Helper()
	rewalkGroups(&m.content)
	cached := m.content.String(width)
	resetGroupRenderCaches(&m.content)
	rewalkGroups(&m.content)
	bypass := m.content.String(width)
	if cached != bypass {
		t.Fatalf("cached group render differs from cache-bypassed render at width %d (got %d bytes, want %d)",
			width, len(cached), len(bypass))
	}
}

// TestSettledRowsKeyCoversUndirtiedInputs changes a render input without marking
// any segment dirty. The settled rows key must still invalidate, so a cache hit
// matches the cache-bypassed render.
func TestSettledRowsKeyCoversUndirtiedInputs(t *testing.T) {
	freezeDelegationClockAt(t)

	cases := []struct {
		name  string
		width int
		apply func(m *Model)
	}{
		{"width", 100, func(_ *Model) {}},
		{"theme", 80, func(m *Model) { m.content.styles = testStyles("#ff8800") }},
		{"body cap", 80, func(m *Model) { m.content.maxDelegationBodyLines = 3 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := mixedGroupModelWithMessages(2, true, mixedGroupTestMessages)
			m.content.String(80)
			tc.apply(m)
			assertSettledCacheMatchesBypass(t, m, tc.width)
		})
	}
}

// TestJumpFlashEntryBypassesSettledCache checks that the entry under a jump flash
// is drawn fresh and never stored in the settled rows cache. The settled rows key
// leaves the flash out, so a stored flash row would outlive the flash.
func TestJumpFlashEntryBypassesSettledCache(t *testing.T) {
	m := mixedGroupModelWithMessages(2, true, mixedGroupTestMessages)
	m.content.String(80)
	dd := mixedEntries(m)[0]
	m.content.jumpFlash = jumpFlashState{dd: dd, on: true}
	m.content.dirtyDelegationCard(dd)
	m.content.String(80)
	if _, ok := mixedGroupSegment(t, m).settled.lines[dd]; ok {
		t.Error("jump-flashed entry was stored in the settled rows cache")
	}
}

// TestSettledGroupCacheSurvivesRewalk covers a fully settled group with no active
// sibling. A re-walk that changes no entry must reuse the settled rows cache, so
// an injected sentinel row comes back in the output.
func TestSettledGroupCacheSurvivesRewalk(t *testing.T) {
	m := populateLongTranscript(newContentBenchModel(), mixedGroupTestMessages)
	for j := range 3 {
		m = appendMixedChild(m, j, true)
	}
	for _, dd := range mixedEntries(m) {
		dd.collapsed = false
		m.content.dirtyDelegationCard(dd)
	}
	m.content.String(80)
	group := mixedGroupSegment(t, m)
	const sentinel = "SENTINEL-SETTLED-ROW"
	group.settled.lines[group.entries[0]] = []string{sentinel}
	rewalkGroups(&m.content)
	if got := m.content.String(80); !strings.Contains(got, sentinel) {
		t.Error("settled rows cache was not reused on a re-walk with no mutation")
	}
}
