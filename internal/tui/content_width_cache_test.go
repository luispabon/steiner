package tui

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"

	"github.com/luispabon/steiner/internal/output"
)

var sidebarToggleKey = tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}

// stubWidthCacheClocks pins nanoNow and timeNow to one shared clock so twin
// models render identical elapsed times and timestamps.
func stubWidthCacheClocks(t *testing.T) (advance func(time.Duration)) {
	t.Helper()
	advanceNano := stubBgFormatClock(t)
	original := timeNow
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = original })
	return func(d time.Duration) {
		advanceNano(d)
		now = now.Add(d)
	}
}

// clearRenderCaches drops every content render cache so the next String call
// renders each segment from scratch at the current width.
func clearRenderCaches(m *Model) {
	b := &m.content
	for i := range b.segments {
		b.segments[i].cachedRender = ""
		b.segments[i].cachedRenderWidth = 0
		b.segments[i].altRenders = widthRenders{}
	}
	b.glamour = glamourPool{}
	b.stringCacheWidth, b.stringCacheBlocks = 0, nil
	b.prefixCacheSet, b.prefixCacheRendered = false, ""
}

// widthCacheScenario extends the WI-1 twin scenario with the inputs a
// per-width render cache must react to: sidebar toggles, height changes,
// accent changes, block expand/collapse, approvals and MCP origin updates.
type widthCacheScenario struct {
	bgFormatScenario
	mcpTools []string
}

func (s *widthCacheScenario) op() func(m *Model) {
	s.next++
	id := fmt.Sprintf("w_%d", s.next)
	switch s.r.IntN(16) {
	case 0, 1:
		return sendMsg(sidebarToggleKey)
	case 2:
		width := []int{100, 120, 150, 170}[s.r.IntN(4)]
		return sendMsg(tea.WindowSizeMsg{Width: width, Height: 40})
	case 3:
		h := []int{28, 34, 40}[s.r.IntN(3)]
		return func(m *Model) { updateModelDirect(m, tea.WindowSizeMsg{Width: m.width, Height: h}) }
	case 4:
		preset := []string{"amber", "violet", "cyan"}[s.r.IntN(3)]
		return sendMsg(setAccentMsg{preset: preset})
	case 5:
		k := s.r.IntN(8)
		return func(m *Model) { clickNthBlockFromEnd(m, k) }
	case 6:
		tool := "mcp__srv__" + id
		s.mcpTools = append(s.mcpTools, tool)
		s.openTools = append(s.openTools, id)
		return sendMsg(runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, tool, id, map[string]any{"q": id})})
	case 7:
		origins := map[string]output.MCPToolOrigin{}
		for _, tool := range s.mcpTools {
			origins[tool] = output.MCPToolOrigin{Server: "srv", Tool: "remote_" + tool}
		}
		return sendMsg(runtimeEventMsg{Event: output.NewMCPStatusEvent(true, nil, origins)})
	case 8:
		if len(s.openTools) == 0 {
			return s.op()
		}
		call := s.openTools[len(s.openTools)-1]
		return sendMsg(runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "read", call, "ask", "preview "+call, "", "", "")})
	default:
		return s.bgFormatScenario.op()
	}
}

// clickNthBlockFromEnd expands or collapses the k-th clickable block (tool
// call, thinking block or delegation card) counted from the end.
func clickNthBlockFromEnd(m *Model, k int) {
	for i := len(m.content.segments) - 1; i >= 0; i-- {
		seg := &m.content.segments[i]
		switch seg.kind {
		case segmentToolCall, segmentThinkingBlock, segmentDelegation:
		default:
			continue
		}
		if k > 0 {
			k--
			continue
		}
		m.handleSegmentClick(seg, 0)
		return
	}
}

// assertTwinViews compares transcript, segment heights and viewport; with
// selection set, also the selection (whose anchors record render generations,
// which only match between twins that re-render at the same moments).
func assertTwinViews(t *testing.T, label string, got, want *Model, selection bool) {
	t.Helper()
	if got.bgFormat.source() != want.bgFormat.source() {
		gl, wl := strings.Split(got.bgFormat.source(), "\n"), strings.Split(want.bgFormat.source(), "\n")
		i := 0
		for i < min(len(gl), len(wl)) && gl[i] == wl[i] {
			i++
		}
		t.Fatalf("%s: transcript differs at line %d (%d vs %d lines)\n got %q\nwant %q",
			label, i, len(gl), len(wl), gl[min(i, len(gl)-1)], wl[min(i, len(wl)-1)])
	}
	if !slices.Equal(got.content.segmentHeights, want.content.segmentHeights) {
		t.Fatalf("%s: segment heights %v, want %v", label, got.content.segmentHeights, want.content.segmentHeights)
	}
	if !slices.Equal(got.viewport.Lines(), want.viewport.Lines()) || got.viewport.YOffset() != want.viewport.YOffset() {
		t.Fatalf("%s: viewport views differ", label)
	}
	if selection && got.selection != want.selection || got.activeRegion != want.activeRegion {
		t.Fatalf("%s: selection %+v (region %v), want %+v (region %v)",
			label, got.selection, got.activeRegion, want.selection, want.activeRegion)
	}
}

// TestWidthRenderCacheMatchesFreshRender drives three models through the same
// random event sequence: one with the per-width render cache, one whose
// alternate-width renders are stripped before every op (today's single-width
// behaviour), and one whose render caches are cleared and rebuilt after every
// viewport width change (a fresh render at the new width). All three must
// agree byte for byte.
func TestWidthRenderCacheMatchesFreshRender(t *testing.T) {
	advance := stubWidthCacheClocks(t)
	revisits := 0
	for seed := range uint64(8) {
		s := &widthCacheScenario{bgFormatScenario: bgFormatScenario{r: rand.New(rand.NewPCG(seed, 11))}}
		run := sendMsg(runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "bench-model", "", 4, 256)})
		cached, single, fresh := newBgFormatTwinModel(run), newBgFormatTwinModel(run), newBgFormatTwinModel(run)
		seen := map[int]bool{cached.viewport.Width(): true}
		for step := range 90 {
			advance(time.Duration(s.r.IntN(3)) * time.Second)
			apply := s.op()
			width := cached.viewport.Width()
			stripWidthRenders(single)
			apply(cached)
			apply(single)
			apply(fresh)
			widthChanged := cached.viewport.Width() != width
			if step < 10 && !widthChanged {
				continue
			}
			if widthChanged {
				if seen[cached.viewport.Width()] {
					revisits++
				}
				seen[cached.viewport.Width()] = true
				clearRenderCaches(fresh)
			}
			stripWidthRenders(single)
			cached.syncViewport()
			single.syncViewport()
			fresh.syncViewport()
			assertTwinViews(t, fmt.Sprintf("seed %d step %d: single-width", seed, step), cached, single, true)
			assertTwinViews(t, fmt.Sprintf("seed %d step %d: fresh", seed, step), cached, fresh, false)
		}
	}
	if revisits < 30 {
		t.Fatalf("setup: only %d width changes returned to a previously used width, want >= 30", revisits)
	}
}

// stripWidthRenders drops every segment's inactive-width renders, reducing the
// model to a single-width render cache.
func stripWidthRenders(m *Model) {
	for i := range m.content.segments {
		m.content.segments[i].altRenders = widthRenders{}
	}
}

// widthCacheFixture builds a transcript holding every segment kind the
// mutation cases touch: markdown, tool calls, a completed delegation, a
// finished MCP tool call, a live thinking block and a running tool call.
func widthCacheFixture(t *testing.T) *Model {
	t.Helper()
	m := populateLongTranscript(newContentBenchModel(), 12)
	for _, ev := range []output.Event{
		output.NewToolCallStartedEvent(1, "mcp__srv__lookup", "call_mcp", map[string]any{"q": "x"}),
		output.NewToolCallFinishedEvent(1, "mcp__srv__lookup", "call_mcp", "result", nil),
		output.NewAssistantMessageEvent(1, "assistant", "Now building."),
		output.NewToolCallStartedEvent(1, "bash", "call_live", map[string]any{"command": "make"}),
		output.NewThinkingChunkEventWithSource(1, "weighing the options. ", output.ChunkSourceAssistant),
	} {
		m = updateModelDirect(m, runtimeEventMsg{Event: ev})
	}
	m.syncViewport()
	if !m.sidebar.Visible(m.width) {
		t.Fatal("setup: sidebar not visible at fixture width")
	}
	return m
}

func lastSegmentOfKind(m *Model, kind contentSegmentKind) *contentSegment {
	for i := len(m.content.segments) - 1; i >= 0; i-- {
		if m.content.segments[i].kind == kind {
			return &m.content.segments[i]
		}
	}
	return nil
}

// expandLongDelegation appends a completed, expanded delegation whose
// transcript is longer than the viewport-derived body cap.
func expandLongDelegation(m *Model) {
	occ := callOcc("call_long", "long")
	updateModelDirect(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewDelegationStartedEvent(occ, "explore", "", "explore"), "long")})
	for i := range 30 {
		call := fmt.Sprintf("long_%d", i)
		updateModelDirect(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewToolCallStartedEvent(1, "read", call, map[string]any{"file_path": "/a.go"}), "long")})
		updateModelDirect(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewToolCallFinishedEvent(1, "read", call, "package a\n", nil), "long")})
	}
	updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		DelegationOccurrence: occ, Status: "complete", TurnCount: 1, Output: "done",
	})})
	seg := lastSegmentOfKind(m, segmentDelegation)
	seg.delegData.collapsed = false
	seg.renderDirty = true
	m.content.gen++
	m.syncViewport()
}

// freshTranscript renders m's transcript with every render cache dropped.
func freshTranscript(m *Model) (string, []int) {
	clearRenderCaches(m)
	m.syncViewport()
	return m.bgFormat.source(), slices.Clone(m.content.segmentHeights)
}

// TestWidthRenderCacheEvictsOnMutation visits both sidebar widths, mutates
// the transcript at one, and toggles back to the other: the cached width must
// serve exactly a fresh render of the mutated transcript.
func TestWidthRenderCacheEvictsOnMutation(t *testing.T) {
	stubWidthCacheClocks(t)
	clickKind := func(kind contentSegmentKind) func(m *Model) {
		return func(m *Model) { m.handleSegmentClick(lastSegmentOfKind(m, kind), 0) }
	}
	cases := []struct {
		name    string
		prepare func(m *Model)
		mutate  func(m *Model)
	}{
		{name: "expand tool call", mutate: clickKind(segmentToolCall)},
		{name: "expand delegation", mutate: clickKind(segmentDelegation)},
		{name: "show thinking", mutate: sendMsg(toggleThinkingMsg{})},
		{
			name:    "stream into thinking block",
			prepare: sendMsg(toggleThinkingMsg{}),
			mutate:  sendMsg(runtimeEventMsg{Event: output.NewThinkingChunkEventWithSource(1, strings.Repeat("more reasoning ", 30), output.ChunkSourceAssistant)}),
		},
		{
			name:    "collapse thinking block",
			prepare: sendMsg(toggleThinkingMsg{}),
			mutate:  clickKind(segmentThinkingBlock),
		},
		{name: "finish tool call", mutate: sendMsg(runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "bash", "call_live", "ok", nil)})},
		{name: "accent change", mutate: sendMsg(setAccentMsg{preset: "violet"})},
		{
			name: "mcp origins arrive",
			mutate: sendMsg(runtimeEventMsg{Event: output.NewMCPStatusEvent(true, nil, map[string]output.MCPToolOrigin{
				"mcp__srv__lookup": {Server: "srv", Tool: "lookup"},
			})}),
		},
		{
			name:    "height change caps delegation body",
			prepare: expandLongDelegation,
			mutate:  func(m *Model) { updateModelDirect(m, tea.WindowSizeMsg{Width: m.width, Height: 22}) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := widthCacheFixture(t)
			if tc.prepare != nil {
				tc.prepare(m)
			}
			updateModelDirect(m, sidebarToggleKey)
			before := m.bgFormat.source()
			updateModelDirect(m, sidebarToggleKey)
			tc.mutate(m)
			m.syncViewport()
			updateModelDirect(m, sidebarToggleKey)
			got, gotHeights := m.bgFormat.source(), slices.Clone(m.content.segmentHeights)
			if got == before {
				t.Fatal("setup: mutation did not change the transcript at the cached width")
			}
			want, wantHeights := freshTranscript(m)
			if got != want {
				t.Fatalf("cached width serves a stale render:\n got %q\nwant %q", got, want)
			}
			if !slices.Equal(gotHeights, wantHeights) {
				t.Fatalf("segment heights %v, want %v", gotHeights, wantHeights)
			}
		})
	}
}

// TestWidthRenderCacheServesRevisitedWidths caches three widths, then
// rewrites every markdown segment's text without dirtying it: any re-render
// (glamour included) would surface the poison, so revisits must serve the
// renders recorded on first visit, byte for byte.
func TestWidthRenderCacheServesRevisitedWidths(t *testing.T) {
	const poison = "POISONED"
	m := populateLongTranscript(newContentBenchModel(), 30)
	resize := func(w int) func(m *Model) {
		return func(m *Model) { updateModelDirect(m, tea.WindowSizeMsg{Width: w, Height: 40}) }
	}
	toggle := sendMsg(sidebarToggleKey)
	first := map[int]string{m.viewport.Width(): m.bgFormat.source()}
	for _, op := range []func(m *Model){toggle, resize(140)} {
		op(m)
		first[m.viewport.Width()] = m.bgFormat.source()
	}
	if len(first) != renderCacheWidths {
		t.Fatalf("setup: visited %d distinct widths, want %d", len(first), renderCacheWidths)
	}

	poisoned := 0
	for i := range m.content.segments {
		if seg := &m.content.segments[i]; seg.kind == segmentAssistantMarkdown {
			seg.text = poison
			poisoned++
		}
	}
	if poisoned == 0 {
		t.Fatal("setup: no markdown segments to poison")
	}
	for step, op := range []func(m *Model){resize(120), toggle, toggle, resize(140), resize(120), toggle, toggle, resize(140)} {
		op(m)
		want, ok := first[m.viewport.Width()]
		if !ok {
			t.Fatalf("step %d: width %d was not visited during setup", step, m.viewport.Width())
		}
		if m.bgFormat.source() != want {
			t.Fatalf("step %d: width %d re-rendered instead of serving its cached render", step, m.viewport.Width())
		}
	}

	resize(100)(m)
	if !strings.Contains(m.bgFormat.source(), poison) {
		t.Fatal("an uncached width must render the current segment text")
	}
}

// TestWidthRenderCacheLayoutRules checks that the cache keeps the existing
// layout rules across width toggles: height-only resizes resync at once, a
// bottom-pinned transcript stays pinned, and a viewport selection clears.
func TestWidthRenderCacheLayoutRules(t *testing.T) {
	cases := []struct {
		name  string
		check func(t *testing.T, m *Model)
	}{
		{
			name: "height-only resize syncs immediately",
			check: func(t *testing.T, m *Model) {
				updateModelDirect(m, tea.WindowSizeMsg{Width: m.width, Height: 30})
				if got := len(m.viewport.Lines()); got < m.viewport.Height() {
					t.Fatalf("viewport has %d lines, want at least height %d", got, m.viewport.Height())
				}
				got := m.bgFormat.source()
				if want, _ := freshTranscript(m); got != want {
					t.Fatal("transcript after a height-only resize differs from a fresh render")
				}
			},
		},
		{
			name: "bottom-pinned scroll stays pinned",
			check: func(t *testing.T, m *Model) {
				for i := range 4 {
					updateModelDirect(m, sidebarToggleKey)
					if !m.viewport.AtBottom() {
						t.Fatalf("toggle %d: viewport left the bottom", i)
					}
				}
			},
		},
		{
			name: "viewport selection clears on width change",
			check: func(t *testing.T, m *Model) {
				m.activeRegion = regionViewport
				m.selection = selectionState{start: selectionPoint{line: 2, col: 1}, end: selectionPoint{line: 5, col: 4}}
				updateModelDirect(m, sidebarToggleKey)
				if m.selection.hasSelection() {
					t.Fatalf("selection %+v survived a width change", m.selection)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := populateLongTranscript(newContentBenchModel(), 20)
			updateModelDirect(m, sidebarToggleKey)
			updateModelDirect(m, sidebarToggleKey)
			if !m.autoScroll || !m.viewport.AtBottom() {
				t.Fatal("setup: transcript not pinned to the bottom")
			}
			tc.check(t, m)
		})
	}
}

// TestGlamourPoolSlot checks the pool's LRU order: a hit moves the slot to the
// front with its renderer, a miss adds an empty slot for the target width, and
// a miss on a full pool evicts the least recently used width.
func TestGlamourPoolSlot(t *testing.T) {
	target := func(w int) int { return max(1, w-markdownRenderPadding) }
	widths := func(p *glamourPool) []int {
		out := make([]int, len(p.slots))
		for i, s := range p.slots {
			out[i] = s.width
		}
		return out
	}
	full := func() *glamourPool {
		p := &glamourPool{}
		for w := range glamourPoolSize {
			p.slot(10 + w).renderer = &glamour.TermRenderer{}
		}
		return p
	}
	cases := []struct {
		name         string
		pool         func() *glamourPool
		width        int
		wantWidths   func(before []int) []int
		wantRenderer bool
	}{
		{
			name:         "miss on empty pool adds an empty slot",
			pool:         func() *glamourPool { return &glamourPool{} },
			width:        80,
			wantWidths:   func([]int) []int { return []int{target(80)} },
			wantRenderer: false,
		},
		{
			name:  "hit moves the slot to the front and keeps its renderer",
			pool:  full,
			width: 10,
			wantWidths: func(before []int) []int {
				return append([]int{before[len(before)-1]}, before[:len(before)-1]...)
			},
			wantRenderer: true,
		},
		{
			name:  "miss on a full pool evicts the least recently used width",
			pool:  full,
			width: 200,
			wantWidths: func(before []int) []int {
				return append([]int{target(200)}, before[:len(before)-1]...)
			},
			wantRenderer: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.pool()
			before := widths(p)
			s := p.slot(tc.width)
			if s != &p.slots[0] {
				t.Fatal("slot is not the front of the pool")
			}
			if s.width != target(tc.width) {
				t.Fatalf("slot width %d, want %d", s.width, target(tc.width))
			}
			if got := s.renderer != nil; got != tc.wantRenderer {
				t.Fatalf("slot has renderer = %v, want %v", got, tc.wantRenderer)
			}
			if got, want := widths(p), tc.wantWidths(before); !slices.Equal(got, want) {
				t.Fatalf("pool widths %v, want %v", got, want)
			}
		})
	}
}
