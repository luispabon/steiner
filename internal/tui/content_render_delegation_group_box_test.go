package tui

import (
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

func TestRenderGroupBoxMatchesStyledBox(t *testing.T) {
	t.Parallel()
	border := lipgloss.Color("#ff5f00")
	bg := lipgloss.Color("#101010")
	contents := []string{
		"",
		"plain",
		"\x1b[31mred\x1b[0m and a long run of words that wraps inside the box at narrow widths",
		"探索作業 wide 探索作業 探索作業 探索作業",
		"emoji 🙂 mixed 🙂🙂🙂 text",
		"line one\nline two is longer than the box\n\nline four",
		"\x1b[31mopen colour carried\nonto the next line\x1b[m",
		"tab\tcolumn and\ttabs",
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\ text",
	}
	for width := range 121 {
		for _, content := range contents {
			want := renderStyledBox(content, border, bg, width)
			var cache groupBoxCache
			// The second pass draws every line from the cache filled by the first.
			for pass := range 2 {
				if got := renderGroupBox(&cache, content, border, bg, width); got != want {
					t.Errorf("width %d pass %d content %q:\n got %q\nwant %q", width, pass, content, got, want)
				}
			}
		}
	}
}

func TestRenderGroupBoxResetsOnWidthChange(t *testing.T) {
	t.Parallel()
	border := lipgloss.Color("#ff5f00")
	bg := lipgloss.Color("#101010")
	content := "a line that wraps at narrow widths\nshort"
	var cache groupBoxCache
	for _, width := range []int{40, 12, 40, 20} {
		want := renderStyledBox(content, border, bg, width)
		if got := renderGroupBox(&cache, content, border, bg, width); got != want {
			t.Errorf("width %d after width change:\n got %q\nwant %q", width, got, want)
		}
	}
}

func TestRenderDelegationGroupCacheMatchesFresh(t *testing.T) {
	savedNano, savedTime := nanoNow, timeNow
	now := int64(1_000_000_000_000)
	nanoNow = func() int64 { return now }
	timeNow = func() time.Time { return time.Unix(0, now) }
	t.Cleanup(func() { nanoNow, timeNow = savedNano, savedTime })

	b := groupTestBuffer()
	group := groupOf("discovery", "complete", "active", "failed")
	seg := contentSegment{delegGroupData: group}
	// fresh renders the same entries with no cached rows.
	fresh := func(width int) string {
		return b.renderDelegationGroupSegment(contentSegment{delegGroupData: &delegationGroupSegment{entries: group.entries}}, width)
	}
	check := func(step string, width int) {
		t.Helper()
		if got, want := b.renderDelegationGroupSegment(seg, width), fresh(width); got != want {
			t.Errorf("%s (width %d): cached render differs from fresh\n got:\n%s\nwant:\n%s", step, width, got, want)
		}
	}

	check("initial", 60)
	group.entries[1].spinnerFrame++
	check("active spinner tick", 60)
	// A real mutation of a settled entry flushes its cached rows, as processSegment does.
	group.entries[2].elapsed = "9s"
	group.settled = settledGroupRows{}
	check("settled mutation after flush", 60)
	group.entries[0].collapsed = false
	group.settled = settledGroupRows{}
	check("settled entry expanded", 60)
	check("width change", 30)
	b.jumpFlash = jumpFlashState{dd: group.entries[0], on: true}
	check("jump flash on settled entry", 30)
	b.jumpFlash = jumpFlashState{}
	check("jump flash cleared", 30)
	// The key covers the render epoch, so an epoch bump starts a fresh cache even
	// without a flush.
	group.entries[2].elapsed = "10s"
	b.renderEpoch++
	check("epoch bump without flush", 30)
}
