package tui

import (
	"strings"
	"testing"

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

// TestRenderGroupBoxReusesCachedLines plants a sentinel in the cached interior of
// an unchanged line. The next render must return it, which proves the per-line
// path reads the line cache instead of redrawing every line.
func TestRenderGroupBoxReusesCachedLines(t *testing.T) {
	t.Parallel()
	border := lipgloss.Color("#ff5f00")
	bg := lipgloss.Color("#101010")
	content := "alpha\nbeta"
	var cache groupBoxCache
	renderGroupBox(&cache, content, border, bg, 40)
	if cache.lines == nil {
		t.Fatal("cold render did not seed the line cache")
	}
	const sentinel = "SENTINEL-CACHED-LINE"
	cache.lines["beta"] = sentinel
	if got := renderGroupBox(&cache, content, border, bg, 40); !strings.Contains(got, sentinel) {
		t.Errorf("unchanged line was redrawn instead of read from the line cache:\n%q", got)
	}
}

// TestRenderGroupBoxCRLFSeedsCache feeds raw CRLF content, which the group
// renderer must normalise to LF before splitting lines. The cold frame must seed
// the line cache, and the next frame must read a planted sentinel from it.
func TestRenderGroupBoxCRLFSeedsCache(t *testing.T) {
	t.Parallel()
	border := lipgloss.Color("#ff5f00")
	bg := lipgloss.Color("#101010")
	const width = 40
	content := "one\r\ntwo\r\nthree\r\nfour"
	var cache groupBoxCache
	want := renderStyledBox(content, border, bg, width)
	if got := renderGroupBox(&cache, content, border, bg, width); got != want {
		t.Fatalf("cold CRLF frame differs from renderStyledBox:\n got %q\nwant %q", got, want)
	}
	if len(cache.lines) != 4 {
		t.Fatalf("CRLF content seeded %d cached lines, want 4; a refused seed leaves the cache cold on every frame", len(cache.lines))
	}
	const sentinel = "SENTINEL-CRLF-LINE"
	cache.lines["two"] = sentinel
	if got := renderGroupBox(&cache, content, border, bg, width); !strings.Contains(got, sentinel) {
		t.Errorf("warm CRLF frame redrew a cached line instead of reusing it:\n%q", got)
	}
}

func TestRenderDelegationGroupCacheMatchesFresh(t *testing.T) {
	freezeDelegationClockAt(t, 1_000_000_000_000)

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
}
