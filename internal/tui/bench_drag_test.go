package tui

import "testing"

// dragBenchModel starts a left-button drag in the viewport of a heavy
// transcript, optionally with the accent picker open, and warms the caches.
func dragBenchModel(b *testing.B, overlay bool) *Model {
	b.Helper()
	m := newOverlayBenchModel(b)
	if overlay {
		m.accentPicker = m.accentPicker.Open(m.accentPreset)
		if !m.anyOverlayOpen() {
			b.Fatal("overlay did not open")
		}
	}
	benchViewSink = m.View().Content
	m.Update(mouseClickMsg{x: 100, y: 20})
	m.Update(mouseMotionMsg{x: 120, y: 30})
	if !m.selection.active {
		b.Fatal("drag did not start")
	}
	for range 3 {
		benchViewSink = m.View().Content
	}
	return m
}

func benchDragFrame(b *testing.B, overlay bool) {
	m := dragBenchModel(b, overlay)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(mouseMotionMsg{x: 120, y: 30 + i%2})
		benchViewSink = m.View().Content
	}
}

// BenchmarkDragSelectionFrame measures one drag frame: a motion event that
// moves the selection end between two rows, then the View it triggers.
func BenchmarkDragSelectionFrame(b *testing.B) {
	b.Run("plain", func(b *testing.B) { benchDragFrame(b, false) })
	b.Run("overlay", func(b *testing.B) { benchDragFrame(b, true) })
}
