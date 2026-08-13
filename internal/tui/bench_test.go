package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

var benchStringSink string
var benchViewSink string

// BenchmarkView measures m.View() steady state performance at a realistic
// terminal size (220x60). Every cost on the frame path is width/height
// proportional, so a small fixture understates real usage by ~15x — see
// BenchmarkViewSmall for the 80x24 reference point.
func BenchmarkView(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	populateBenchModel(m)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchViewSink = m.View().Content
	}
}

// BenchmarkViewSmall measures m.View() at the historical 80x24 fixture size,
// kept as a reference point for how much a small terminal understates cost.
func BenchmarkViewSmall(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	populateBenchModel(m)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchViewSink = m.View().Content
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
	populateBenchModel(m)

	// Force the dirty path on every call: streaming with buffered content causes
	// checkBufferDirty() to return true, bypassing the wholesale string cache.
	m.content.streaming = true
	m.content.streamBuffer = "pending stream text"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchStringSink = m.content.String(m.viewport.Width())
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
	populateBenchModel(m)

	// Complete the delegation so the buffer is settled (no active delegations).
	m.content.activeDelegations = nil
	m.content.streaming = false

	// Pre-warm the cache by calling String once to populate all caches.
	benchStringSink = m.content.String(m.viewport.Width())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchStringSink = m.content.String(m.viewport.Width())
	}
}

// BenchmarkSyncViewport measures m.syncViewport() performance.
func BenchmarkSyncViewport(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	populateBenchModel(m)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.syncViewport()
	}
}

// BenchmarkKeystroke measures the cost of feeding a keystroke through Update
// at a realistic terminal size (220x60) with a fixed-size composer.
// This exercises relayoutInput → computeInputRows → inputChromeHeight.
// The composer is pre-filled with a long wrapped line and the benchmark
// alternates typing and backspace so its size stays bounded: a naively
// growing composer makes per-op cost grow with b.N and the reported
// average is dominated by an unrealistically huge input.
func BenchmarkKeystroke(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	populateBenchModel(m)
	m.input.SetValue(strings.Repeat("lorem ipsum dolor sit amet ", 40))
	m.input.CursorEnd()
	m.relayoutInput()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			m = updateModelDirect(m, tea.KeyPressMsg{Code: 'x', Text: "x"})
		} else {
			m = updateModelDirect(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
		}
	}
}

// BenchmarkViewHeavy measures m.View() with ~100 segments at a realistic
// terminal size (220x60).
func BenchmarkViewHeavy(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	populateBenchModelHeavy(m)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchViewSink = m.View().Content
	}
}

// BenchmarkSyncViewportHeavy measures m.syncViewport() with ~100 segments.
func BenchmarkSyncViewportHeavy(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	populateBenchModelHeavy(m)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.syncViewport()
	}
}

// BenchmarkContentStringHeavy measures dirty String() path with ~100 segments.
func BenchmarkContentStringHeavy(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	populateBenchModelHeavy(m)

	m.content.streaming = true
	m.content.streamBuffer = "pending stream text"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchStringSink = m.content.String(m.viewport.Width())
	}
}

// BenchmarkContentStringUltraHeavy measures dirty String() path with ~400
// segments (4× the heavy fixture), the streaming-conversation worst case.
func BenchmarkContentStringUltraHeavy(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	populateBenchModelHeavy(m)
	populateBenchModelHeavy(m)
	populateBenchModelHeavy(m)
	populateBenchModelHeavy(m)

	m.content.streaming = true
	m.content.streamBuffer = "pending stream text"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchStringSink = m.content.String(m.viewport.Width())
	}
}

// BenchmarkStationaryFrameHeavy measures a fully cached frame where nothing
// changed: m.View() with ~100 segments at a realistic terminal size (220x60),
// pinned at the bottom by autoscroll. This is the autoscroll-streaming case
// (viewport at bottom, no scroll movement between frames); every View() cost
// other than the viewport slice is a cache hit.
func BenchmarkStationaryFrameHeavy(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	populateBenchModelHeavy(m)

	m.syncViewport()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchViewSink = m.View().Content
	}
}

// BenchmarkScrollDownHeavy measures a real scrolled frame: the viewport
// actually moves every iteration, both directions (up on even i, down on
// odd), with ~100 segments at a realistic terminal size (220x60), on warmed
// caches. The min/max yOffset guard is the regression guard that prevents
// this benchmark from silently measuring a stationary frame again:
// viewport.ScrollDown early-returns at the bottom, and a start-vs-end
// comparison would fail spuriously because the loop oscillates.
func BenchmarkScrollDownHeavy(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	populateBenchModelHeavy(m)

	m.syncViewport()
	m.scrollUp(30) // leave the bottom so both directions can move
	lo, hi := m.viewport.YOffset(), m.viewport.YOffset()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			m.scrollUp(3)
		} else {
			m.scrollDown(3)
		}
		benchViewSink = m.View().Content
		y := m.viewport.YOffset()
		lo, hi = min(lo, y), max(hi, y)
	}
	b.StopTimer()
	if hi == lo {
		b.Fatalf("viewport never moved: yOffset pinned at %d", lo)
	}
}

// BenchmarkScrollDownHeavy16x is the conversation-scale flatness guard:
// identical to BenchmarkScrollDownHeavy but with ~1600 segments (16× the
// heavy fixture). Per-frame B/op and allocs/op must match
// BenchmarkScrollDownHeavy regardless of conversation size.
func BenchmarkScrollDownHeavy16x(b *testing.B) {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	for range 16 {
		populateBenchModelHeavy(m)
	}

	m.syncViewport()
	m.scrollUp(30) // leave the bottom so both directions can move
	lo, hi := m.viewport.YOffset(), m.viewport.YOffset()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			m.scrollUp(3)
		} else {
			m.scrollDown(3)
		}
		benchViewSink = m.View().Content
		y := m.viewport.YOffset()
		lo, hi = min(lo, y), max(hi, y)
	}
	b.StopTimer()
	if hi == lo {
		b.Fatalf("viewport never moved: yOffset pinned at %d", lo)
	}
}

// updateModelDirect calls Model.Update without test infrastructure.
func updateModelDirect(m *Model, msg tea.Msg) *Model {
	next, _ := m.Update(msg)
	if updated, ok := next.(*Model); ok {
		return updated
	}
	return m
}

// populateBenchModel populates a Model with realistic content for benchmarking:
// markdown segments, tool calls, a delegation, and completion.
func populateBenchModel(m *Model) {
	// Run started
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "bench-model", "", 4, 256)})

	// Assistant markdown response
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "Looking at the code, I can see several optimization opportunities:\n\n", output.ChunkSourceAssistant)})
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "1. **Redundant allocations**: The `renderContent` loop allocates new strings on each iteration.\n", output.ChunkSourceAssistant)})
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "2. **Repeated regex compilation**: The pattern compilation happens per-frame.\n", output.ChunkSourceAssistant)})
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "3. **Cache invalidation**: Full re-renders even when nothing changed.\n\n", output.ChunkSourceAssistant)})

	// Tool call
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"file_path": "/src/main.go"})})
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "read", "call_1", `package main\n\nfunc main() {\n    // optimized code\n}`, nil)})

	// Another markdown chunk
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "Here's the optimized implementation:\n\n", output.ChunkSourceAssistant)})

	// Tool call
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call_2", map[string]any{"file_path": "/src/main.go", "content": "optimized code"})})
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "write", "call_2", "written successfully", nil)})

	// Delegation event (complete it so buffer is settled for benchmarking)
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationStartedEvent("agent_1", "design the optimization")})
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "agent_1",
		Status:        "complete",
		TurnCount:     2,
		TokenCount:    500,
		ToolCallCount: 3,
		Output:        "optimized design",
	})})

	// Thinking block
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewThinkingChunkEventWithSource(1, "The user asked for optimization suggestions. I identified three key areas and proposed solutions.", output.ChunkSourceAssistant)})

	// Context token budget
	updateModelDirect(m, runtimeEventMsg{Event: output.NewContextTokenBudgetEvent("conversation", 1, 500, 4096, 2, 70, 32, 600, "ok", false)})
}

// populateBenchModelHeavy populates a Model with ~100 segments of mixed types:
// 30 assistant markdown chunks, 20 tool calls, 10 user messages, 5 thinking blocks,
// 5 delegations, and 5 tool call groups (2-3 calls each).
func populateBenchModelHeavy(m *Model) {
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "bench-model", "", 4, 256)})

	// 30 assistant markdown chunks with varying lengths
	chunkSizes := []int{100, 200, 150, 300, 250, 180, 220, 280, 320, 160,
		190, 240, 270, 210, 140, 290, 260, 170, 230, 310,
		120, 195, 265, 285, 155, 275, 235, 305, 125, 245}
	for i, size := range chunkSizes {
		content := strings.Repeat("x", size) + " "
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, content, output.ChunkSourceAssistant)})
		if i%5 == 0 {
			m = updateModelDirect(m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "\n\n", output.ChunkSourceAssistant)})
		}
	}

	// 20 tool calls (mix of read, bash, mutate)
	toolNames := []string{"read", "bash", "mutate", "read", "bash",
		"mutate", "read", "bash", "mutate", "read",
		"bash", "mutate", "read", "bash", "mutate",
		"read", "bash", "mutate", "read", "bash"}
	for i, toolName := range toolNames {
		callID := fmt.Sprintf("call_heavy_%d", i)
		outputSize := 500 + (i%4)*500
		toolOutput := strings.Repeat("y", outputSize)
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, toolName, callID, map[string]any{"arg": "value"})})
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, toolName, callID, toolOutput, nil)})
	}

	// 10 user messages
	for i := 0; i < 10; i++ {
		msg := "User message " + strings.Repeat("z", 100+i*10)
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewUserInputEvent(msg, "interactive")})
	}

	// 5 thinking blocks
	for i := 0; i < 5; i++ {
		thought := "Thinking about problem " + strings.Repeat("t", 200+i*50)
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewThinkingChunkEventWithSource(1, thought, output.ChunkSourceAssistant)})
	}

	// 5 delegations (started + completed pairs)
	for i := 0; i < 5; i++ {
		agentID := fmt.Sprintf("agent_heavy_%d", i)
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationStartedEvent(agentID, "task preview "+fmt.Sprintf("%d", i))})
		delegOutput := strings.Repeat("d", 300+i*100)
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
			AgentID:       agentID,
			Status:        "complete",
			TurnCount:     3 + i,
			TokenCount:    500 + i*100,
			ToolCallCount: 2 + i,
			Output:        delegOutput,
		})})
	}

	// 5 tool call groups (2-3 calls each)
	callIdx := len(toolNames)
	for grp := 0; grp < 5; grp++ {
		callsInGroup := 2 + grp%2
		for j := 0; j < callsInGroup; j++ {
			callID := fmt.Sprintf("call_heavy_%d", callIdx)
			callIdx++
			outputSize := 400 + (grp%3)*600
			toolOutput := strings.Repeat("g", outputSize)
			m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "read", callID, map[string]any{"file": "test.go"})})
			m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "read", callID, toolOutput, nil)})
		}
	}

	updateModelDirect(m, runtimeEventMsg{Event: output.NewContextTokenBudgetEvent("conversation", 1, 500, 4096, 2, 70, 32, 600, "ok", false)})
}
