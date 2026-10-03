package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// newSidebarClickModel builds a tall model with one running ungrouped agent, a
// grouped pair (one finished) and enough finished agents to collapse a "+N" row.
func newSidebarClickModel(t *testing.T, position string, finished int) (*Model, map[string]occurrenceKey) {
	t.Helper()
	m := newModel(Config{Model: "m", ModelContexts: map[string]int{"m": 4096}}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 140, Height: 70})
	m.sidebarPosition = position
	keys := map[string]occurrenceKey{}
	idx := 1
	add := func(agentID, group string, status string) {
		callID := "call-" + agentID
		keys[agentID] = addJumpCard(m, idx, callID, group)
		idx++
		e := m.roster.upsert(agentID)
		e.agentType = "review"
		e.occurrence = keys[agentID]
		e.admitted = true
		e.group = group
		e.status = status
		if status == rosterDone {
			e.finishTime = 5
		}
	}
	add("solo", "", rosterRunning)
	add("g1", "grp", rosterRunning)
	add("g2", "grp", rosterDone)
	for i := 0; i < finished; i++ {
		add("old"+string(rune('a'+i)), "", rosterDone)
	}
	addJumpFiller(m, 80)
	m.syncRoster()
	m.syncViewport()
	return m, keys
}

// rosterScreenRow returns the screen row of the given sidebar target index.
func rosterScreenRow(m *Model, idx int) int {
	return len(m.sidebar.staticPrefixLines(sidebarWidth-2*sidebarPadH)) + idx + sidebarPadV
}

func sidebarClickX(m *Model, col int) int {
	if m.sidebarPosition == "right" {
		return m.width - sidebarWidth + col
	}
	return col
}

func TestRosterTargetAtRowMatchesRender(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 10)
	w := sidebarWidth - 2*sidebarPadH
	lines, targets := m.sidebar.subAgentsRows(w)
	if len(lines) != len(targets) || len(lines) == 0 {
		t.Fatalf("lines=%d targets=%d", len(lines), len(targets))
	}
	if got := m.sidebar.subAgentsSection(w); len(got) != len(lines) {
		t.Fatalf("subAgentsSection len %d != rows len %d", len(got), len(lines))
	}
	prefix := len(m.sidebar.staticPrefixLines(w))
	clickable := 0
	for i, want := range targets {
		if got := m.sidebar.rosterTargetAtRow(70, prefix+i); got != want {
			t.Errorf("row %d: target %q, want %q", i, got, want)
		}
		if want != "" {
			clickable++
		}
	}
	if clickable == 0 {
		t.Error("no clickable rows")
	}
	if got := m.sidebar.rosterTargetAtRow(70, prefix-1); got != "" {
		t.Errorf("row before section = %q", got)
	}
	if got := m.sidebar.rosterTargetAtRow(70, prefix+len(lines)); got != "" {
		t.Errorf("row after section = %q", got)
	}
	// innerHeight truncation: the last clickable row is cut off.
	last := 0
	for i, tg := range targets {
		if tg != "" {
			last = i
		}
	}
	if got := m.sidebar.rosterTargetAtRow(prefix+last, prefix+last); got != "" {
		t.Errorf("row at innerHeight = %q, want none", got)
	}
}

func TestSidebarRosterClick(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		t.Run(pos, func(t *testing.T) {
			m, keys := newSidebarClickModel(t, pos, 10)
			w := sidebarWidth - 2*sidebarPadH
			_, targets := m.sidebar.subAgentsRows(w)
			index := map[string]int{}
			var nonTargets []int
			for i, tg := range targets {
				if tg == "" {
					nonTargets = append(nonTargets, i)
				} else {
					index[tg] = i
				}
			}
			if len(nonTargets) < 4 { // blank, label, header, +N finished
				t.Fatalf("expected non-clickable rows, got %d", len(nonTargets))
			}
			for _, id := range []string{"solo", "g1", "g2"} {
				t.Run(id, func(t *testing.T) {
					m.jumpTarget = occurrenceKey{}
					cmd := m.sidebarRosterClick(sidebarClickX(m, sidebarPadH), rosterScreenRow(m, index[id]))
					if cmd == nil || m.jumpTarget != keys[id] {
						t.Errorf("jumpTarget = %+v (cmd nil=%v), want %+v", m.jumpTarget, cmd == nil, keys[id])
					}
				})
			}
			for _, i := range nonTargets {
				m.jumpTarget = occurrenceKey{}
				if cmd := m.sidebarRosterClick(sidebarClickX(m, sidebarPadH), rosterScreenRow(m, i)); cmd != nil || m.jumpTarget != (occurrenceKey{}) {
					t.Errorf("non-clickable row %d jumped", i)
				}
			}
			row := rosterScreenRow(m, index["solo"])
			for _, col := range []int{0, sidebarPadH - 1, sidebarWidth - sidebarPadH, sidebarWidth - 1} {
				if cmd := m.sidebarRosterClick(sidebarClickX(m, col), row); cmd != nil {
					t.Errorf("padding column %d jumped", col)
				}
			}
			if cmd := m.sidebarRosterClick(sidebarClickX(m, sidebarPadH), 0); cmd != nil {
				t.Error("top padding jumped")
			}
		})
	}
}

func TestSidebarRosterClickGuards(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	row := rosterScreenRow(m, 2)
	x := sidebarPadH

	m.roster.entries["solo"].occurrence = occurrenceKey{}
	m.syncRoster()
	if cmd := m.sidebarRosterClick(x, row); cmd != nil {
		t.Error("zero occurrence jumped")
	}

	m, keys := newSidebarClickModel(t, "left", 0)
	m.sidebar.expanded = false
	if cmd := m.sidebarRosterClick(x, row); cmd != nil || m.jumpTarget != (occurrenceKey{}) {
		t.Error("hidden sidebar handled click")
	}
	m.sidebar.expanded = true
	m.roster.entries["solo"].occurrence = keys["g1"]
	if cmd := m.sidebarRosterClick(x, row); cmd == nil || m.jumpTarget != keys["g1"] {
		t.Errorf("click did not follow the row's current occurrence: %+v", m.jumpTarget)
	}
}

func TestSidebarMousePressRelease(t *testing.T) {
	cases := []struct {
		name                  string
		pressDX, relDX, relDY int
		wantJump              bool
	}{
		{"same cell", 0, 0, 0, true},
		{"release on other row", 0, 0, 1, false},
		{"release on other column", 0, 1, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, keys := newSidebarClickModel(t, "left", 0)
			_, targets := m.sidebar.subAgentsRows(sidebarWidth - 2*sidebarPadH)
			idx := -1
			for i, tg := range targets {
				if tg == "solo" {
					idx = i
				}
			}
			x, y := sidebarPadH+tc.pressDX, rosterScreenRow(m, idx)
			m = updateModel(t, m, mouseClickMsg{x: x, y: y})
			m = updateModel(t, m, mouseReleaseMsg{x: x + tc.relDX, y: y + tc.relDY})
			if got := m.jumpTarget == keys["solo"]; got != tc.wantJump {
				t.Errorf("jumped = %v, want %v", got, tc.wantJump)
			}
		})
	}
}

func TestSidebarReleaseWithoutPressDoesNothing(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	if m.sidebarPressX != -1 || m.sidebarPressY != -1 {
		t.Fatalf("fresh press cell = (%d,%d)", m.sidebarPressX, m.sidebarPressY)
	}
	m.activeRegion = regionSidebar
	m = updateModel(t, m, mouseReleaseMsg{x: 0, y: 0})
	if m.jumpTarget != (occurrenceKey{}) {
		t.Error("phantom click jumped")
	}
}

// TestSidebarRosterClickUsesRenderedRows clicks each agent where the rendered
// sidebar actually draws it. A dev version too long for the logo row used to
// soft-wrap, shifting every roster row down one screen row so a click landed
// on the agent below.
func TestSidebarRosterClickUsesRenderedRows(t *testing.T) {
	m, keys := newSidebarClickModel(t, "left", 0)
	m.sidebar.version = "0.27.0-8-ga17d550e-dirty"
	rows := strings.Split(ansi.Strip(m.sidebar.View(m.width, m.height)), "\n")
	for _, id := range []string{"solo", "g1", "g2"} {
		row := -1
		for i, line := range rows {
			if strings.Contains(line, " "+id+" ") {
				row = i
				break
			}
		}
		if row < 0 {
			t.Fatalf("agent %q not rendered in sidebar", id)
		}
		m.jumpTarget = occurrenceKey{}
		if cmd := m.sidebarRosterClick(sidebarClickX(m, sidebarPadH), row); cmd == nil || m.jumpTarget != keys[id] {
			t.Errorf("click on %q row %d: jumpTarget = %+v, want %+v", id, row, m.jumpTarget, keys[id])
		}
	}
}
