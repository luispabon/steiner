package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

func TestContextOverlayMouseWheelInsideScrollsOverlay(t *testing.T) {
	t.Parallel()
	m := newContextOverlayMouseTestModel(t)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", longContextOverlayMouseReport())})

	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = closed, want open")
	}
	if m.contextOverlay.lineCount <= m.contextOverlay.visibleHeight() {
		t.Fatalf("context overlay lineCount = %d, want more than %d", m.contextOverlay.lineCount, m.contextOverlay.visibleHeight())
	}
	boundsX, boundsY, boundsW, boundsH := m.contextOverlayBounds()
	if boundsW == 0 || boundsH == 0 {
		t.Fatalf("context overlay bounds = (%d, %d, %d, %d), want non-zero rectangle", boundsX, boundsY, boundsW, boundsH)
	}

	viewportOffset := m.viewport.YOffset()
	centerX := boundsX + boundsW/2
	centerY := boundsY + boundsH/2
	m = updateModel(t, m, mouseWheelMsg{direction: "down", x: centerX, y: centerY})

	if m.contextOverlay.scrollOffset == 0 {
		t.Fatal("context overlay scrollOffset = 0, want positive after wheel down inside overlay")
	}
	if got := m.viewport.YOffset(); got != viewportOffset {
		t.Fatalf("viewport yOffset = %d, want unchanged at %d", got, viewportOffset)
	}
}

func TestContextOverlayMouseWheelOutsideScrollsViewport(t *testing.T) {
	t.Parallel()
	m := newContextOverlayMouseTestModel(t)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", longContextOverlayMouseReport())})
	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = closed, want open")
	}

	boundsX, boundsY, boundsW, boundsH := m.contextOverlayBounds()
	if boundsX <= 1 && boundsY <= 1 && boundsX+boundsW > 1 && boundsY+boundsH > 1 {
		t.Fatal("context overlay covers outside test point (1, 1)")
	}
	m.contextOverlay.scrollOffset = 0
	viewportOffset := m.viewport.YOffset()
	m = updateModel(t, m, mouseWheelMsg{direction: "up", x: 1, y: 1})

	if got := m.viewport.YOffset(); got >= viewportOffset {
		t.Fatalf("viewport yOffset = %d, want less than %d after wheel up outside overlay", got, viewportOffset)
	}
	if got := m.contextOverlay.scrollOffset; got != 0 {
		t.Fatalf("context overlay scrollOffset = %d, want unchanged at 0", got)
	}
}

func TestContextOverlayMouseWheelClosedScrollsViewport(t *testing.T) {
	t.Parallel()
	m := newContextOverlayMouseTestModel(t)
	viewportOffset := m.viewport.YOffset()
	m = updateModel(t, m, mouseWheelMsg{direction: "up", x: 1, y: 1})

	if got := m.viewport.YOffset(); got >= viewportOffset {
		t.Fatalf("viewport yOffset = %d, want less than %d after wheel up with overlay closed", got, viewportOffset)
	}
}

func TestContextOverlayDoesNotCaptureWhenBlockingOverlayIsOpen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		open func(*Model)
	}{
		{
			name: "file list",
			open: func(m *Model) {
				m.fileList = m.fileList.Open(".")
			},
		},
		{
			name: "MCP overlay",
			open: func(m *Model) {
				m.mcpOverlay = m.mcpOverlay.Open(m.mcpServers, m.mcpEnabled)
				m.mcpOverlay.OverlayShell = m.mcpOverlay.WithDimensions(m.width, m.height)
			},
		},
		{
			name: "workflow handoff",
			open: func(m *Model) {
				m.workflowHandoff = openWorkflowHandoffModal(m.width, m.height, output.WorkflowHandoffEvent{}, interactive.WorkflowHandoffModelSelection{})
			},
		},
		{
			name: "worktree cleanup",
			open: func(m *Model) {
				m.openWorktreeCleanupModal(m.width, m.height, 1)
			},
		},
		{
			name: "exit",
			open: func(m *Model) {
				m.openExitModal()
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newContextOverlayMouseTestModel(t)
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", contextOverlayMouseReport(8))})
			boundsX, boundsY, boundsW, boundsH := m.contextOverlayBounds()
			centerX := boundsX + boundsW/2
			centerY := boundsY + boundsH/2
			if !m.contextOverlayCapturesMouse(centerX, centerY) {
				t.Fatal("context overlay did not capture mouse at its center before the blocking overlay opened")
			}

			tc.open(m)
			if m.contextOverlayCapturesMouse(centerX, centerY) {
				t.Fatal("context overlay captured mouse while blocking overlay was open")
			}
		})
	}
}

func TestContextOverlayMouseIgnoredWhenConfirmModalOpen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		open func(*Model)
	}{
		{
			name: "worktree cleanup",
			open: func(m *Model) {
				m.openWorktreeCleanupModal(m.width, m.height, 1)
			},
		},
		{
			name: "exit",
			open: func(m *Model) {
				m.openExitModal()
			},
		},
		{
			name: "orchestration",
			open: func(m *Model) {
				m.requestOrchestrationLevel(config.OrchestrationLevelLow)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := &conversationTestController{conversation: []agent.Message{{Role: "user"}}}
			m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 160, Height: 50})
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", contextOverlayMouseReport(8))})
			boundsX, boundsY, boundsW, boundsH := m.contextOverlayBounds()
			centerX := boundsX + boundsW/2
			centerY := boundsY + boundsH/2
			if !m.contextOverlayCapturesMouse(centerX, centerY) {
				t.Fatal("context overlay did not capture mouse at its center before the confirm modal opened")
			}

			tc.open(m)
			if !m.anyConfirmModalOpen() {
				t.Fatalf("expected a confirm modal to be open after opener")
			}
			if m.contextOverlayCapturesMouse(centerX, centerY) {
				t.Fatal("context overlay captured mouse while confirm modal was open")
			}
		})
	}
}

func TestClassifyMouseWheelIncludesCoordinates(t *testing.T) {
	t.Parallel()
	cmd := classifyMouse(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown, X: 17, Y: 23}))
	if cmd == nil {
		t.Fatal("wheel command = nil, want command")
	}
	msg, ok := cmd().(mouseWheelMsg)
	if !ok {
		t.Fatalf("wheel command message = %T, want mouseWheelMsg", cmd())
	}
	if msg.x != 17 || msg.y != 23 {
		t.Fatalf("wheel coordinates = (%d, %d), want (17, 23)", msg.x, msg.y)
	}
}

// TestContextOverlayHitMapEdges pins the sidebar cell boundaries: the last
// sidebar column still selects its agent, the pane divider beside it belongs to
// the overlay but no agent, and the rule, the "Sub-agents" label and the clipped
// overflow indicator select nothing.
func TestContextOverlayHitMapEdges(t *testing.T) {
	t.Parallel()
	m, _ := contextReportModel(t, contextOverlayManyAgents(15))
	if !m.contextOverlay.twoPane() {
		t.Fatal("want the two-pane layout")
	}
	_, w, h := m.contextOverlayRendered()
	entry := m.overlayMemos.context.lookup(m.contextRenderKey())
	if entry == nil {
		t.Fatal("no cached context overlay render, want the rendered overlay memoized")
	}
	hit := entry.hit
	if hit.narrow || len(hit.targets) == 0 {
		t.Fatalf("hit map = %+v, want the two-pane sidebar rows", hit)
	}
	originX, originY := (m.width-w)/2, (m.height-h)/2
	cell := func(localX, bodyRow int) (int, int) {
		return originX + localX, originY + hit.bodyTop + bodyRow
	}

	x, y := cell(hit.sidebarRight-1, 4)
	if got, ok := m.contextOverlayHitAt(x, y); !ok || !got.inSidebar || got.agent != 1 {
		t.Fatalf("last sidebar cell (%d,%d) hit = %+v, %v; want sidebar agent 1", x, y, got, ok)
	}
	x, y = cell(hit.sidebarRight, 4)
	if got, ok := m.contextOverlayHitAt(x, y); !ok || !got.inside || got.inSidebar || got.agent != -1 {
		t.Fatalf("pane divider cell (%d,%d) hit = %+v, %v; want inside without a sidebar agent", x, y, got, ok)
	}

	for _, row := range []int{2, 3} {
		x, y = cell(hit.sidebarLeft, row)
		if got, ok := m.contextOverlayHitAt(x, y); !ok || !got.inSidebar || got.agent != -1 {
			t.Fatalf("sidebar row %d at (%d,%d) hit = %+v, %v; want no agent", row, x, y, got, ok)
		}
	}

	lines := strings.Split(stripANSI(m.renderContextOverlay()), "\n")
	indicatorRow := -1
	for row := range hit.targets {
		if strings.Contains(lines[hit.bodyTop+row], "↓") {
			indicatorRow = row
			break
		}
	}
	if indicatorRow < 0 {
		t.Fatal("no down overflow indicator drawn, want the 15 agents to overflow the sidebar window")
	}
	x, y = cell(hit.sidebarLeft, indicatorRow)
	if got, ok := m.contextOverlayHitAt(x, y); !ok || !got.inSidebar || got.agent != -1 {
		t.Fatalf("overflow indicator row %d at (%d,%d) hit = %+v, %v; want no agent", indicatorRow, x, y, got, ok)
	}
}

// TestContextOverlayClippedClick pins pointer handling when the overlay is
// taller than the terminal: a click on the visibly drawn part is swallowed and
// starts no transcript selection, while a click outside the clipped bounds is
// handled by the transcript as usual.
func TestContextOverlayClippedClick(t *testing.T) {
	t.Parallel()
	useTrueColor(t)
	m := newModel(Config{Model: "gpt-test"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 8})
	m.roster.entries = map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
	}
	m.contextOverlay = openContextReportOverlay(
		contextAgentsFromRoster(m.roster.snapshot()), m.width, m.height, m.styles, m.content.glamourStyleSheet)
	_, _, overlayHeight := m.contextOverlayRendered()
	if overlayHeight <= m.height {
		t.Fatalf("overlay height = %d at terminal height %d, want a vertically clipped overlay", overlayHeight, m.height)
	}
	boundsX, boundsY, boundsW, boundsH := m.contextOverlayBounds()
	if boundsH != m.height {
		t.Fatalf("clipped overlay height = %d, want the whole terminal height %d", boundsH, m.height)
	}

	insideX, insideY := boundsX+2, boundsY+boundsH/2
	if !m.contextOverlayCapturesMouse(insideX, insideY) {
		t.Fatalf("cell (%d,%d) not captured, want it inside the visibly drawn overlay", insideX, insideY)
	}
	m = updateModel(t, m, mouseClickMsg{x: insideX, y: insideY})
	if !m.overlayPress {
		t.Fatalf("overlayPress = false after a click at (%d,%d), want the overlay to own the press", insideX, insideY)
	}
	if m.mousePressX >= 0 || m.selection.hasSelection() {
		t.Fatalf("press state = mousePressX %d, selection %+v; want no transcript selection inside the overlay", m.mousePressX, m.selection)
	}

	outsideX, outsideY := boundsX+boundsW, insideY
	if m.contextOverlayCapturesMouse(outsideX, outsideY) {
		t.Fatalf("cell (%d,%d) captured, want it outside the clipped overlay bounds", outsideX, outsideY)
	}
	m = updateModel(t, m, mouseClickMsg{x: outsideX, y: outsideY})
	if m.overlayPress {
		t.Fatalf("overlayPress = true after a click at (%d,%d), want the transcript to handle it", outsideX, outsideY)
	}
	if got := m.mousePressX; got != outsideX {
		t.Fatalf("mousePressX = %d, want %d from the transcript press outside the overlay", got, outsideX)
	}
}

func newContextOverlayMouseTestModel(t *testing.T) *Model {
	t.Helper()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 160, Height: 50})
	for i := 0; i < 100; i++ {
		m.content.AppendLine("conversation line")
	}
	m.syncViewport()
	m.viewport.GotoBottom()
	return m
}

func longContextOverlayMouseReport() string {
	return contextOverlayMouseReport(80)
}

// contextOverlayMouseReport builds a report with n list items. Rendering it
// through glamour dominates the cost of these tests, so tests that only need
// the overlay's bounds use a short one.
func contextOverlayMouseReport(n int) string {
	lines := []string{"# Long Report", ""}
	for i := 0; i < n; i++ {
		lines = append(lines, "- item "+strings.Repeat("x", 8)+" "+strings.Repeat("y", 8))
	}
	return strings.Join(lines, "\n")
}
