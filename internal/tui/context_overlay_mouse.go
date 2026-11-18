package tui

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
