package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/output"
)

var benchStringSink string
var benchViewSink string

// BenchmarkView measures m.View() steady state performance.
func BenchmarkView(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	populateBenchModel(&m)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchViewSink = m.View()
	}
}

// BenchmarkContentString measures contentBuffer.String(width) on the full-render
// (cache-miss) path. streaming=true with a non-empty streamBuffer causes
// checkBufferDirty to return true every call, so the full segment render runs
// each iteration. See BenchmarkContentStringCacheHit for the settled cache-hit path.
func BenchmarkContentString(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	populateBenchModel(&m)

	// Force the dirty path on every call: streaming with buffered content causes
	// checkBufferDirty() to return true, bypassing the wholesale string cache.
	m.content.streaming = true
	m.content.streamBuffer = "pending stream text"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchStringSink = m.content.String(m.viewport.Width)
	}
}

// BenchmarkContentStringCacheHit measures contentBuffer.String(width) when the
// buffer is settled (no streaming, no active delegations), which exercises the
// wholesale cache-hit path. This is the cache optimization introduced in Phase 4.
func BenchmarkContentStringCacheHit(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	populateBenchModel(&m)

	// Complete the delegation so the buffer is settled (no active delegations).
	m.content.activeDelegations = nil
	m.content.streaming = false

	// Pre-warm the cache by calling String once to populate all caches.
	benchStringSink = m.content.String(m.viewport.Width)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchStringSink = m.content.String(m.viewport.Width)
	}
}

// BenchmarkSyncViewport measures m.syncViewport() performance.
func BenchmarkSyncViewport(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	populateBenchModel(&m)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.syncViewport()
	}
}

// BenchmarkKeystroke measures the cost of feeding a keystroke through Update.
// This exercises relayoutInput → computeInputRows → inputChromeHeight.
func BenchmarkKeystroke(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	populateBenchModel(&m)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m = updateModelDirect(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	}
}

// updateModelDirect calls Model.Update without test infrastructure.
func updateModelDirect(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	if updated, ok := next.(Model); ok {
		return updated
	}
	return m
}

// populateBenchModel populates a Model with realistic content for benchmarking:
// markdown segments, tool calls, a delegation, and completion.
func populateBenchModel(m *Model) {
	// Run started
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "bench-model", "", 4, 256)})

	// Assistant markdown response
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "Looking at the code, I can see several optimization opportunities:\n\n", output.ChunkSourceAssistant)})
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "1. **Redundant allocations**: The `renderContent` loop allocates new strings on each iteration.\n", output.ChunkSourceAssistant)})
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "2. **Repeated regex compilation**: The pattern compilation happens per-frame.\n", output.ChunkSourceAssistant)})
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "3. **Cache invalidation**: Full re-renders even when nothing changed.\n\n", output.ChunkSourceAssistant)})

	// Tool call
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"file_path": "/src/main.go"})})
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "read", "call_1", `package main\n\nfunc main() {\n    // optimized code\n}`, nil)})

	// Another markdown chunk
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "Here's the optimized implementation:\n\n", output.ChunkSourceAssistant)})

	// Tool call
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call_2", map[string]any{"file_path": "/src/main.go", "content": "optimized code"})})
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "write", "call_2", "written successfully", nil)})

	// Delegation event (complete it so buffer is settled for benchmarking)
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewDelegationStartedEvent("agent_1", "design the optimization")})
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewDelegationCompleteEvent("agent_1", "complete", 2, 500, 3, "optimized design")})

	// Thinking block
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewThinkingChunkEventWithSource(1, "The user asked for optimization suggestions. I identified three key areas and proposed solutions.", output.ChunkSourceAssistant)})

	// Context token budget
	*m = updateModelDirect(*m, runtimeEventMsg{Event: output.NewContextTokenBudgetEvent("conversation", 1, 500, 4096, 2, 70, 32, 600, "ok", false)})
}
