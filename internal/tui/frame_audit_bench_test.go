package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// Per-handler cost benchmarks backing the frame audit findings. They reuse
// auditModel (220x60, 4x heavy transcript, 3 running sub-agents).

func BenchmarkAuditSyncSidebar(b *testing.B) {
	m := auditModel(&testing.T{}, 3, true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.syncSidebar()
	}
}

func BenchmarkAuditChunkUpdate(b *testing.B) {
	m := auditModel(&testing.T{}, 3, true)
	updateModelDirect(m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "bench-model", "", 4, 256)})
	msg := chunk(1, "")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = m.Update(msg)
	}
}

func BenchmarkAuditTickUpdateStreaming(b *testing.B) {
	m := auditModel(&testing.T{}, 3, true)
	updateModelDirect(m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "bench-model", "", 4, 256)})
	updateModelDirect(m, chunk(1, ""))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = m.Update(tickMsg{})
	}
}

func BenchmarkAuditSyncViewportUnchanged(b *testing.B) {
	m := auditModel(&testing.T{}, 3, true)
	updateModelDirect(m, tickMsg{}) // warm the settled-prefix cache
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.syncViewport()
	}
}

// BenchmarkAuditSyncViewportContentChanged forces a full-transcript background
// format (no lines reused), to price the O(transcript) post-processing.
func BenchmarkAuditSyncViewportContentChanged(b *testing.B) {
	m := auditModel(&testing.T{}, 3, true)
	updateModelDirect(m, tickMsg{}) // warm the settled-prefix cache
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.fmtBgCacheInput = ""
		m.bgFormat = bgFormatCache{}
		m.syncViewport()
	}
}
