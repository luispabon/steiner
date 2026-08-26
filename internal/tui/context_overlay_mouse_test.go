package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

func TestContextOverlayMouseWheelInsideScrollsOverlay(t *testing.T) {
	t.Parallel()
	m := newContextOverlayMouseTestModel(t)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", longContextOverlayMouseReport())})

	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay = closed, want open")
	}
	if m.contextOverlay.lineCount <= contextOverlayMaxLines {
		t.Fatalf("context overlay lineCount = %d, want more than %d", m.contextOverlay.lineCount, contextOverlayMaxLines)
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

func TestContextOverlayDoesNotCaptureWhenFileListIsOpen(t *testing.T) {
	t.Parallel()
	m := newContextOverlayMouseTestModel(t)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", longContextOverlayMouseReport())})
	boundsX, boundsY, boundsW, boundsH := m.contextOverlayBounds()
	centerX := boundsX + boundsW/2
	centerY := boundsY + boundsH/2

	m.fileList = m.fileList.Open(".")
	if m.contextOverlayCapturesMouse(centerX, centerY) {
		t.Fatal("context overlay captured mouse while file list overlay was open")
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
	lines := []string{"# Long Report", ""}
	for i := 0; i < 80; i++ {
		lines = append(lines, "- item "+strings.Repeat("x", 8)+" "+strings.Repeat("y", 8))
	}
	return strings.Join(lines, "\n")
}
