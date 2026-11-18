package tui

// contextOverlayHit maps rendered body rows to the agent index each row
// selects. Narrow mode has no sidebar, so it carries no targets.
type contextOverlayHit struct {
	narrow       bool
	bodyTop      int
	sidebarLeft  int
	sidebarRight int
	targets      []int
}

// contextOverlayHitResult is the outcome of a pointer lookup on the context
// overlay. agent is -1 when the cell selects no agent.
type contextOverlayHitResult struct {
	inside    bool
	inSidebar bool
	agent     int
}

// contextOverlayHitAt maps a terminal cell to the agent row drawn under it. It
// uses the unclipped overlay origin because composeCenteredOverlay positions
// the overlay unclipped while contextOverlayBounds clamps it.
func (m *Model) contextOverlayHitAt(x, y int) (contextOverlayHitResult, bool) {
	if !m.contextOverlay.IsOpen() {
		return contextOverlayHitResult{}, false
	}
	_, w, h := m.contextOverlayRendered()
	entry := m.overlayMemos.context.lookup(m.contextRenderKey())
	if entry == nil || w <= 0 || h <= 0 {
		return contextOverlayHitResult{}, false
	}
	localX := x - (m.width-w)/2
	localY := y - (m.height-h)/2
	if localX < 0 || localX >= w || localY < 0 || localY >= h {
		return contextOverlayHitResult{}, false
	}
	result := contextOverlayHitResult{inside: true, agent: -1}
	hit := entry.hit
	if hit.narrow {
		return result, true
	}
	row := localY - hit.bodyTop
	if row < 0 || row >= len(hit.targets) || localX < hit.sidebarLeft || localX >= hit.sidebarRight {
		return result, true
	}
	result.inSidebar = true
	if target := hit.targets[row]; target >= 0 {
		result.agent = target
	}
	return result, true
}

// handleContextOverlayClick swallows a pointer press inside the open context
// overlay and selects the sidebar agent row under it. It reports whether the
// press belonged to the overlay, in which case the matching release is
// swallowed too and no transcript selection or drag starts.
func (m *Model) handleContextOverlayClick(x, y int) bool {
	if !m.contextOverlayCapturesMouse(x, y) {
		return false
	}
	m.overlayPress = true
	m.activeRegion = regionNone
	m.selection = m.selection.clear()
	m.mousePressX, m.mousePressY = -1, -1
	m.sidebarPressX, m.sidebarPressY = -1, -1
	if hit, ok := m.contextOverlayHitAt(x, y); ok && hit.inSidebar && hit.agent >= 0 {
		m.clickContextAgent(hit.agent)
	}
	return true
}

// handleContextOverlayWheel scrolls the overlay under the pointer. The agent
// sidebar moves its list one row per notch without changing the selection; the
// report pane keeps scrolling the report. It reports whether the wheel belonged
// to the overlay.
func (m *Model) handleContextOverlayWheel(wheel mouseWheelMsg) bool {
	if !m.contextOverlayCapturesMouse(wheel.x, wheel.y) {
		return false
	}
	if hit, ok := m.contextOverlayHitAt(wheel.x, wheel.y); ok && hit.inSidebar {
		delta := 1
		if wheel.direction == "up" {
			delta = -1
		}
		m.contextOverlay = m.contextOverlay.scrollAgents(delta)
		return true
	}
	switch wheel.direction {
	case "up":
		m.contextOverlay = m.contextOverlay.scrollUp(m.viewport.mouseWheelDelta)
	case "down":
		m.contextOverlay = m.contextOverlay.scrollDown(m.viewport.mouseWheelDelta)
	}
	return true
}
