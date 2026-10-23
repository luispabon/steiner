package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Dev log overlay benchmarks. They drive only production entry points (the
// Alt+E key, Model.Update, Model.View and Model.recordDevLog), so the same file
// compiles on revisions with and without the overlay render memo.
//
//	go test ./internal/tui -run '^$' -bench 'BenchmarkDevLog' -benchmem
//
// Datasets, each with devLogBenchEntries entries built outside the timed region:
//
//	realistic  one-line messages, and every fourth entry a multi-line message
//	stress     every entry is an 8 KiB message (the per-entry cap)
//
// Workloads:
//
//	closed      overlay closed, View only
//	stationary  overlay open, View with nothing changed
//	scroll      Up/Down alternating, starting devLogBenchPages pages above the tail
//	push        one recordDevLog inside the timed region, then View
//	resize      WindowSizeMsg alternating between two sizes, then View
//	click       one left click inside the overlay, then View

const (
	devLogBenchEntries   = 500
	devLogBenchBlobBytes = 8 * 1024
	devLogBenchPages     = 8
)

var (
	devLogBenchAltE       = tea.KeyPressMsg{Code: 'e', Mod: tea.ModAlt}
	devLogBenchSizes      = [2]tea.WindowSizeMsg{{Width: 220, Height: 60}, {Width: 180, Height: 50}}
	devLogBenchScrollKeys = [2]tea.KeyPressMsg{{Code: tea.KeyUp}, {Code: tea.KeyDown}}
	// The two clicks are more than one cell apart, so the click count stays at
	// 1. Nearby clicks would count as double clicks and run populateScreenLines.
	devLogBenchClicks = [2]mouseClickMsg{{x: 90, y: 20}, {x: 130, y: 40}}
	devLogBenchSink   string
)

// devLogBenchParams returns devLogBenchEntries deterministic entries. Every
// message is valid UTF-8.
func devLogBenchParams(stress bool) []devLogParams {
	sources := []string{"agent.loop", "provider.stream", "tool.bash", "tool.mutate", "mcp.client", "lsp.gopls"}
	events := []string{"tool_result", "stream_error", "turn_end", "approval", "repeat_guard"}
	params := make([]devLogParams, devLogBenchEntries)
	for i := range params {
		p := devLogParams{
			Severity:  devLogWarning,
			Source:    sources[i%len(sources)],
			EventType: events[i%len(events)],
			AgentType: "general",
			CallID:    fmt.Sprintf("call_%04d", i),
		}
		if i%3 == 0 {
			p.Severity = devLogError
		}
		switch {
		case stress:
			p.Message = devLogBenchBlob(i)
		case i%4 == 0:
			p.Message = devLogBenchStack(i)
		default:
			p.Message = fmt.Sprintf("%s failed on attempt %d: connection reset while streaming output", events[i%len(events)], i%7)
		}
		params[i] = p
	}
	return params
}

// devLogBenchStack returns a headline followed by eight tab-indented frame
// lines, with an arrow in the headline.
func devLogBenchStack(i int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "panic while handling tool call %d: index %d out of range [0:%d] → retrying\n", i, i%9+3, i%5+1)
	for f := range 8 {
		fmt.Fprintf(&sb, "\tgithub.com/luispabon/steiner/internal/tool.(*Runner).step_%02d(runner_%03d.go:%d)\n", f, i%200, 100+f*7)
	}
	sb.WriteString("caused by: context deadline exceeded")
	return sb.String()
}

// devLogBenchBlob returns an ASCII message of exactly devLogBenchBlobBytes
// bytes, so recordDevLog stores it without truncation.
func devLogBenchBlob(i int) string {
	var sb strings.Builder
	for line := 0; sb.Len() < devLogBenchBlobBytes; line++ {
		fmt.Fprintf(&sb, "entry %03d line %04d: stress payload for dev log wrap and scroll paths\n", i, line)
	}
	return sb.String()[:devLogBenchBlobBytes]
}

// devLogBenchModel returns a 220x60 model with the heavy transcript and the
// ring filled from params. It opens the overlay with Alt+E when open is true.
// The diagnostics sink stays nil, so no diagnostics I/O happens.
func devLogBenchModel(b *testing.B, params []devLogParams, open bool) *Model {
	b.Helper()
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	populateBenchModelHeavy(m)
	m.syncViewport()
	for _, p := range params {
		m.recordDevLog(p)
	}
	if open {
		m = updateModelDirect(m, devLogBenchAltE)
	}
	for range 3 {
		devLogBenchSink = m.View().Content
	}
	return m
}

func benchDevLogClosed(b *testing.B, params []devLogParams) {
	m := devLogBenchModel(b, params, false)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		devLogBenchSink = m.View().Content
	}
}

func benchDevLogStationary(b *testing.B, params []devLogParams) {
	m := devLogBenchModel(b, params, true)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		devLogBenchSink = m.View().Content
	}
}

// benchDevLogScroll pages up from the tail, then checks that each Up and Down
// before timing changes the frame, so the timed keys move the offset.
func benchDevLogScroll(b *testing.B, params []devLogParams) {
	m := devLogBenchModel(b, params, true)
	for range devLogBenchPages {
		m = updateModelDirect(m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	}
	prev := m.View().Content
	for i := range 4 {
		m = updateModelDirect(m, devLogBenchScrollKeys[i%2])
		next := m.View().Content
		if next == prev {
			b.Fatalf("scroll step %d left the frame unchanged", i)
		}
		prev = next
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		m = updateModelDirect(m, devLogBenchScrollKeys[i%2])
		devLogBenchSink = m.View().Content
	}
}

func benchDevLogPush(b *testing.B, params []devLogParams) {
	m := devLogBenchModel(b, params, true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		m.recordDevLog(params[i%len(params)])
		devLogBenchSink = m.View().Content
	}
}

func benchDevLogResize(b *testing.B, params []devLogParams) {
	m := devLogBenchModel(b, params, true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		m = updateModelDirect(m, devLogBenchSizes[i%2])
		devLogBenchSink = m.View().Content
	}
}

func benchDevLogClick(b *testing.B, params []devLogParams) {
	m := devLogBenchModel(b, params, true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		m = updateModelDirect(m, devLogBenchClicks[i%2])
		devLogBenchSink = m.View().Content
	}
}

func BenchmarkDevLog(b *testing.B) {
	datasets := []struct {
		name   string
		stress bool
	}{{"realistic", false}, {"stress", true}}
	workloads := []struct {
		name string
		run  func(*testing.B, []devLogParams)
	}{
		{"closed", benchDevLogClosed},
		{"stationary", benchDevLogStationary},
		{"scroll", benchDevLogScroll},
		{"push", benchDevLogPush},
		{"resize", benchDevLogResize},
		{"click", benchDevLogClick},
	}
	for _, ds := range datasets {
		b.Run(ds.name, func(b *testing.B) {
			params := devLogBenchParams(ds.stress)
			for _, w := range workloads {
				b.Run(w.name, func(b *testing.B) { w.run(b, params) })
			}
		})
	}
}
