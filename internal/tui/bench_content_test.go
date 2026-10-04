package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

// Content-pipeline benchmarks: the cost of appending to / rebuilding a long
// transcript (as opposed to the scroll/frame benchmarks in bench_test.go).
// Every operation goes through Model.Update so the measured cost includes
// contentBuffer.blocks, syncViewport (background/padding format, lines) and any
// chrome that Update refreshes.

const benchMarkdownReply = "Here is the plan, covering **three** areas:\n\n" +
	"1. Parse the config in `loadConfig` and validate it.\n" +
	"2. Wire the result into the *agent* loop.\n" +
	"3. Add tests.\n\n" +
	"```go\n" +
	"func loadConfig(path string) (*Config, error) {\n" +
	"\tdata, err := os.ReadFile(path)\n" +
	"\tif err != nil {\n" +
	"\t\treturn nil, fmt.Errorf(\"read config: %w\", err)\n" +
	"\t}\n" +
	"\tvar cfg Config\n" +
	"\tif err := json.Unmarshal(data, &cfg); err != nil {\n" +
	"\t\treturn nil, fmt.Errorf(\"parse config: %w\", err)\n" +
	"\t}\n" +
	"\treturn &cfg, nil\n" +
	"}\n" +
	"```\n\n" +
	"| field | meaning |\n|---|---|\n| path | where to read |\n| cfg | parsed value |\n\n" +
	"A longer closing paragraph that needs to wrap at narrow widths because it keeps going and going with plenty of words."

// stepSync feeds one event through Update and then performs the viewport sync
// that the 50ms debounce (or the animation tick while streaming/active) would
// run after it. Update alone only appends to the buffer and marks it dirty.
func stepSync(m *Model, msg tea.Msg) {
	updateModelDirect(m, msg)
	m.syncViewport()
}

func newContentBenchModel() *Model {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 100000},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	return updateModelDirect(m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "bench-model", "", 4, 256)})
}

// populateLongTranscript appends n "messages" of mixed content: user prompts,
// markdown replies with highlighted code, read/bash tool calls with output,
// and a completed delegation every 10th message. Roughly 3 segments/message.
func populateLongTranscript(m *Model, n int) *Model {
	toolOut := strings.Repeat("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n", 8)
	for i := 0; i < n; i++ {
		if i%6 == 0 {
			m = updateModelDirect(m, runtimeEventMsg{Event: output.NewUserInputEvent(fmt.Sprintf("please look at module %d and tell me what it does", i), "interactive", nil)})
		}
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewAssistantMessageEvent(1, "assistant", benchMarkdownReply)})
		id := fmt.Sprintf("call_%d", i)
		name := "read"
		if i%3 == 1 {
			name = "bash"
		}
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, name, id, map[string]any{"file_path": fmt.Sprintf("/src/mod%d/main.go", i)})})
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, name, id, toolOut, nil)})
		if i%10 == 5 {
			a := fmt.Sprintf("done_%d", i)
			m = updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationStartedEvent(agentOcc(a), "investigate "+a, "", "")})
			m = updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
				DelegationOccurrence: agentOcc(a), Status: "complete", TurnCount: 3, TokenCount: 900, ToolCallCount: 4,
				Output: strings.Repeat("finding about the module. ", 20),
			})})
		}
	}
	m.syncViewport()
	return m
}

// benchResetEvery runs op b.N times, rebuilding the fixture every `every` ops
// (timer stopped) so the transcript does not grow unboundedly with b.N.
func benchResetEvery(b *testing.B, every int, build func() *Model, op func(m *Model, i int)) {
	b.ReportAllocs()
	m := build()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i > 0 && i%every == 0 {
			b.StopTimer()
			m = build()
			b.StartTimer()
		}
		op(m, i)
	}
}

func benchTranscriptSizes(b *testing.B, fn func(b *testing.B, msgs int)) {
	for _, n := range []int{200, 500} {
		b.Run(fmt.Sprintf("msgs=%d", n), func(b *testing.B) { fn(b, n) })
	}
}

// BenchmarkContentStreamDelta: one assistant text delta (~30 bytes) arriving
// on a long transcript, with a streaming buffer that grows to ~100 deltas.
func BenchmarkContentStreamDelta(b *testing.B) {
	benchTranscriptSizes(b, func(b *testing.B, n int) {
		benchResetEvery(b, 100, func() *Model { return populateLongTranscript(newContentBenchModel(), n) },
			func(m *Model, _ int) {
				stepSync(m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(2, "and some more streamed words, ", output.ChunkSourceAssistant)})
			})
	})
}

// BenchmarkContentStreamDeltaLongBuffer: same, but the streaming buffer is
// already ~8KB (a long answer), isolating the O(buffer) preview re-render.
func BenchmarkContentStreamDeltaLongBuffer(b *testing.B) {
	benchResetEvery(b, 100, func() *Model {
		m := populateLongTranscript(newContentBenchModel(), 200)
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(2, strings.Repeat("streamed words here ", 400), output.ChunkSourceAssistant)})
		return m
	}, func(m *Model, _ int) {
		stepSync(m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(2, "and some more streamed words, ", output.ChunkSourceAssistant)})
	})
}

// BenchmarkContentThinkingDelta: a thinking delta on a long transcript. The
// live thinking segment must not invalidate the settled-prefix cache.
func BenchmarkContentThinkingDelta(b *testing.B) {
	benchTranscriptSizes(b, benchThinkingDelta)
}

func benchThinkingDelta(b *testing.B, n int) {
	benchResetEvery(b, 100, func() *Model { return populateLongTranscript(newContentBenchModel(), n) },
		func(m *Model, _ int) {
			stepSync(m, runtimeEventMsg{Event: output.NewThinkingChunkEventWithSource(2, "considering the options carefully. ", output.ChunkSourceAssistant)})
		})
}

// BenchmarkContentToolCallStart: tool call start (new segment) on a long transcript.
func BenchmarkContentToolCallStart(b *testing.B) {
	benchTranscriptSizes(b, func(b *testing.B, n int) {
		benchResetEvery(b, 50, func() *Model { return populateLongTranscript(newContentBenchModel(), n) },
			func(m *Model, i int) {
				stepSync(m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(2, "read", fmt.Sprintf("s_%d", i), map[string]any{"file_path": "/src/new.go"})})
			})
	})
}

// BenchmarkContentToolCallFinish: finishing a tool call that was just started
// at the tail of a long transcript. Start is excluded from the timed region.
func BenchmarkContentToolCallFinish(b *testing.B) {
	benchTranscriptSizes(b, benchToolCallFinish)
}

func benchToolCallFinish(b *testing.B, n int) {
	toolOut := strings.Repeat("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n", 8)
	b.ReportAllocs()
	m := populateLongTranscript(newContentBenchModel(), n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i > 0 && i%50 == 0 {
			b.StopTimer()
			m = populateLongTranscript(newContentBenchModel(), n)
			b.StartTimer()
		}
		b.StopTimer()
		id := fmt.Sprintf("f_%d", i)
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(2, "read", id, map[string]any{"file_path": "/src/new.go"})})
		b.StartTimer()
		stepSync(m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(2, "read", id, toolOut, nil)})
	}
}

func startInflightChildren(m *Model, k int) []string {
	ids := make([]string, k)
	for j := 0; j < k; j++ {
		ids[j] = fmt.Sprintf("child-%d", j)
		m = updateModelDirect(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewDelegationStartedEvent(callOcc("call_"+ids[j], ids[j]), "explore part "+ids[j], "", "explore"), ids[j])})
	}
	return ids
}

// BenchmarkContentDelegationChildEvent: a sub-agent assistant delta while 3
// sub-agents are in flight (spinners force per-frame re-render of their
// segments) on a long transcript.
func BenchmarkContentDelegationChildEvent(b *testing.B) {
	benchTranscriptSizes(b, func(b *testing.B, n int) {
		var ids []string
		benchResetEvery(b, 100, func() *Model {
			m := populateLongTranscript(newContentBenchModel(), n)
			ids = startInflightChildren(m, 3)
			return m
		}, func(m *Model, i int) {
			id := ids[i%3]
			stepSync(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "child output words. ", output.ChunkSourceAssistant), id)})
		})
	})
}

// BenchmarkContentDelegationChildToolCall: child tool call started+finished
// (two events) with 3 sub-agents in flight.
func BenchmarkContentDelegationChildToolCall(b *testing.B) {
	benchTranscriptSizes(b, func(b *testing.B, n int) {
		var ids []string
		benchResetEvery(b, 40, func() *Model {
			m := populateLongTranscript(newContentBenchModel(), n)
			ids = startInflightChildren(m, 3)
			return m
		}, func(m *Model, i int) {
			id := ids[i%3]
			call := fmt.Sprintf("cc_%d", i)
			stepSync(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewToolCallStartedEvent(1, "read", call, map[string]any{"file_path": "/a.go"}), id)})
			stepSync(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewToolCallFinishedEvent(1, "read", call, "package a\n", nil), id)})
		})
	})
}

// BenchmarkContentIdleFrameInflight: pure spinner tick cost with 3 in-flight
// sub-agents: syncViewport only (what each animation tick pays).
func BenchmarkContentIdleFrameInflight(b *testing.B) {
	benchTranscriptSizes(b, benchIdleFrameInflight)
}

func benchIdleFrameInflight(b *testing.B, n int) {
	b.ReportAllocs()
	m := populateLongTranscript(newContentBenchModel(), n)
	startInflightChildren(m, 3)
	m.syncViewport()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.syncViewport()
	}
}

// toolSegFromEnd returns the k-th tool call segment counting from the end.
func toolSegFromEnd(m *Model, k int) *contentSegment {
	for i := len(m.content.segments) - 1; i >= 0; i-- {
		if m.content.segments[i].kind == segmentToolCall && m.content.segments[i].toolData != nil {
			if k == 0 {
				return &m.content.segments[i]
			}
			k--
		}
	}
	return nil
}

// BenchmarkContentToggleBlock: expand/collapse of a tool block, near the tail
// ("tail") and a third of the way in ("early").
func BenchmarkContentToggleBlock(b *testing.B) {
	for _, pos := range []struct {
		name string
		k    int
	}{{"tail", 0}, {"early", 330}} {
		b.Run(pos.name, func(b *testing.B) { benchToggleBlock(b, 500, pos.k) })
	}
}

// benchToggleBlock toggles the k-th tool block from the end of an n-message
// transcript.
func benchToggleBlock(b *testing.B, n, k int) {
	b.ReportAllocs()
	m := populateLongTranscript(newContentBenchModel(), n)
	seg := toolSegFromEnd(m, k)
	if seg == nil {
		b.Fatal("no tool segment")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.handleToolCallClick(seg)
	}
}

// BenchmarkContentResizeWidth: a single width change on a long transcript.
// The widths cycle through one more than renderCacheWidths, so every change
// lands on a width the per-width render cache no longer holds.
func BenchmarkContentResizeWidth(b *testing.B) {
	widths := []int{100, 105, 110, 115}
	if len(widths) <= renderCacheWidths {
		b.Fatalf("cycle of %d widths fits the %d-width render cache", len(widths), renderCacheWidths)
	}
	benchTranscriptSizes(b, func(b *testing.B, n int) {
		b.ReportAllocs()
		m := populateLongTranscript(newContentBenchModel(), n)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			m = updateModelDirect(m, tea.WindowSizeMsg{Width: widths[i%len(widths)], Height: 40})
		}
	})
}

// BenchmarkContentResizeHeight: height-only change (no reflow expected).
func BenchmarkContentResizeHeight(b *testing.B) {
	b.ReportAllocs()
	m := populateLongTranscript(newContentBenchModel(), 500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h := 40
		if i%2 == 0 {
			h = 41
		}
		m = updateModelDirect(m, tea.WindowSizeMsg{Width: 120, Height: h})
	}
}

// BenchmarkContentResizeStorm: 30 distinct width changes in a row (a window
// drag); the unit of work is the whole storm.
func BenchmarkContentResizeStorm(b *testing.B) {
	benchTranscriptSizes(b, func(b *testing.B, n int) {
		b.ReportAllocs()
		m := populateLongTranscript(newContentBenchModel(), n)
		b.ResetTimer()
		prev := 120
		for i := 0; i < b.N; i++ {
			for j := 0; j < 30; j++ {
				w := 90 + (j*7+i)%41 // widths 90..130
				if w == prev {
					w++
				}
				prev = w
				m = updateModelDirect(m, tea.WindowSizeMsg{Width: w, Height: 40})
			}
		}
	})
}

// BenchmarkSidebarToggleRoundTrip: one ctrl+b sidebar toggle (off, on, off...)
// through Update and View on a 200-message transcript, sidebar initially
// visible. "first" rebuilds the fixture per op so only the cold toggle is
// measured; "steady" keeps toggling the same model after two untimed toggles
// have visited both widths, so it measures the second and later toggles.
func BenchmarkSidebarToggleRoundTrip(b *testing.B) {
	b.Run("first", func(b *testing.B) {
		benchResetEvery(b, 1, func() *Model { return sidebarToggleFixture(b, 200) }, sidebarToggleOp)
	})
	b.Run("steady", func(b *testing.B) { benchSidebarToggleSteady(b, 200) })
}

func sidebarToggleFixture(b *testing.B, msgs int) *Model {
	m := populateLongTranscript(newContentBenchModel(), msgs)
	if !m.sidebar.Visible(m.width) {
		b.Fatal("sidebar not visible at bench width")
	}
	return m
}

func sidebarToggleOp(m *Model, _ int) {
	_, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	_ = m.View()
}

func benchSidebarToggleSteady(b *testing.B, msgs int) {
	benchResetEvery(b, 1<<30, func() *Model {
		m := sidebarToggleFixture(b, msgs)
		sidebarToggleOp(m, 0)
		sidebarToggleOp(m, 0)
		return m
	}, sidebarToggleOp)
}
