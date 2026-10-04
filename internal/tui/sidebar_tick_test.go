package tui

import "testing"

func TestRenderSidebarTickInvalidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setup       func(m *Model)
		wantChanged bool
	}{
		{name: "nothing animating", setup: func(*Model) {}},
		{name: "settled mcp", setup: func(m *Model) { m.sidebar.mcpTotal, m.sidebar.mcpConnected = 2, 2 }},
		{name: "finished roster row", setup: func(m *Model) {
			m.roster.upsert("a").status = rosterDone
			m.roster.entries["a"].startTime, m.roster.entries["a"].finishTime = 1, 5
		}},
		{name: "queued roster row", setup: func(m *Model) { m.roster.upsert("a").status = rosterQueued }},
		{name: "mcp connecting", wantChanged: true, setup: func(m *Model) {
			m.sidebar.mcpTotal, m.sidebar.mcpConnecting = 2, true
		}},
		{name: "lsp starting", wantChanged: true, setup: func(m *Model) {
			m.sidebar.lspTotalKnown, m.sidebar.lspStarting, m.sidebar.lspSingleName = 1, true, "gopls"
		}},
		{name: "running roster row", wantChanged: true, setup: func(m *Model) {
			e := m.roster.upsert("a")
			e.status, e.startTime = rosterRunning, nanoNow()
		}},
		{name: "compaction blink", wantChanged: true, setup: func(m *Model) {
			m.sidebar.compaction = compactionState{active: true}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newSidebarTestModel(t)
			tc.setup(m)
			m.syncRoster()
			m.pollLSPStatesFunc = nil
			first := m.renderSidebar(m.width, m.height)
			rendersBefore := m.sidebarRenders

			m = updateModelDirect(m, tickMsg{})
			second := m.renderSidebar(m.width, m.height)

			if tc.wantChanged {
				if first == second {
					t.Error("sidebar output did not change across a tick while something animates")
				}
				if m.sidebarRenders == rendersBefore {
					t.Error("sidebar was not re-rendered across a tick while something animates")
				}
				return
			}
			if m.sidebarRenders != rendersBefore {
				t.Errorf("sidebar re-rendered %d time(s) on a tick with nothing animating", m.sidebarRenders-rendersBefore)
			}
			if first != second {
				t.Error("sidebar output changed across a tick with nothing animating")
			}
		})
	}
}
