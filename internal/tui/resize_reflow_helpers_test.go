package tui

import tea "charm.land/bubbletea/v2"

// flushResizeReflow delivers the pending deferred content reflow, if any, so
// resize benchmarks count the whole cost of a resize.
func flushResizeReflow(m *Model) {
	if m.reflow.pending {
		_, _ = m.Update(resizeReflowFiredMsg{seq: m.reflow.seq})
	}
}

// resizeNow resizes the window and delivers the deferred reflow at once, so
// tests that compare settled frames see the same state as a synchronous reflow.
func resizeNow(w, h int) func(m *Model) {
	return func(m *Model) {
		updateModelDirect(m, tea.WindowSizeMsg{Width: w, Height: h})
		flushResizeReflow(m)
	}
}
