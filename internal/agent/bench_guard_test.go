//go:build perfguard

// Package-level note: this file is behind the `perfguard` build tag so the
// benchmarks it runs stay out of `go test ./...`. It costs ~7s for this
// package alone (~14s combined with the TUI guards via `make test-perf`).

package agent

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
)

// TestBenchmarkAllocationCeilings pins allocation ceilings for the agent
// conversation-pipeline operations so a regression that re-introduces
// allocations fails the test suite. Ceilings are set from baselines measured
// on this machine (go1.26.6 linux/amd64): bytes = ceil(measured*1.15),
// allocs = ceil(measured*1.2).
func TestBenchmarkAllocationCeilings(t *testing.T) {
	// The perfguard tag keeps this out of the normal suite; this skip is the
	// separate foot-gun guard for `go test -tags perfguard -race`, where five
	// testing.Benchmark runs under race instrumentation take minutes.
	if raceEnabled {
		t.Skip("five testing.Benchmark runs under race take minutes")
	}

	cases := []struct {
		name      string
		fn        func(*testing.B)
		maxBytes  int64
		maxAllocs int64
		baseline  string
	}{
		{
			name:      "CloneMessages",
			fn:        BenchmarkCloneMessages,
			maxBytes:  329032,
			maxAllocs: 1952,
			baseline:  "measured 286114 B/op, 1626 allocs/op (reference ~286k/1626)",
		},
		{
			name:      "LineageFullMessages",
			fn:        BenchmarkLineageFullMessages,
			maxBytes:  329034,
			maxAllocs: 1952,
			baseline:  "measured 286116 B/op, 1626 allocs/op (reference ~286k/1626)",
		},
		{
			name:      "ReplaySafeConversation",
			fn:        BenchmarkReplaySafeConversation,
			maxBytes:  379630,
			maxAllocs: 2102,
			baseline:  "measured 330113 B/op, 1751 allocs/op (reference ~330k/1751)",
		},
		{
			name:      "AssemblyOptions",
			fn:        BenchmarkAssemblyOptions,
			maxBytes:  1018859,
			maxAllocs: 6004,
			baseline:  "measured 885964 B/op, 5003 allocs/op (reference ~886k/5003)",
		},
		{
			name:      "PrepareTurnStatePrologue",
			fn:        BenchmarkPrepareTurnStatePrologue,
			maxBytes:  2008616,
			maxAllocs: 11880,
			baseline:  "measured 1746622 B/op, 9900 allocs/op (reference ~1747k/9900)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := testing.Benchmark(tc.fn)
			// Always report, so a passing run shows the measured values
			// rather than looking like it asserted nothing.
			t.Logf("%s: %d B/op (ceiling %d), %d allocs/op (ceiling %d)",
				tc.name, res.AllocedBytesPerOp(), tc.maxBytes, res.AllocsPerOp(), tc.maxAllocs)
			if res.AllocsPerOp() == 0 || res.AllocedBytesPerOp() == 0 {
				t.Fatal("benchmark measured no allocations; metrics are not being captured")
			}
			if res.AllocsPerOp() > tc.maxAllocs {
				t.Errorf("allocs/op = %d, ceiling %d; %s", res.AllocsPerOp(), tc.maxAllocs, tc.baseline)
			}
			if res.AllocedBytesPerOp() > tc.maxBytes {
				t.Errorf("B/op = %d, ceiling %d; %s", res.AllocedBytesPerOp(), tc.maxBytes, tc.baseline)
			}
		})
	}
}

// perfConversation builds a realistic 500-message transcript as a repeated
// 4-message cycle: user message, assistant message with two tool calls (grep,
// read) carrying nested arguments, and the two matching tool results. The tool
// result IDs match the assistant's ToolCall IDs so ReplaySafeConversation keeps
// the full transcript.
func perfConversation(messages int) []Message {
	cycle := []Message{
		{
			Role:    MessageRoleUser,
			Content: "Implement the feature and run the tests.",
		},
		{
			Role:    MessageRoleAssistant,
			Content: "I'll search for the relevant code and read the files.",
			ToolCalls: []ToolCall{
				{
					ID:   "call_grep",
					Name: "grep",
					Arguments: map[string]any{
						"pattern": "foo",
						"nested":  map[string]any{"key": "value"},
						"list":    []any{"a", "b", 1},
					},
				},
				{
					ID:   "call_read",
					Name: "read",
					Arguments: map[string]any{
						"path":   "file.go",
						"nested": map[string]any{"limit": 10},
						"list":   []any{"x"},
					},
				},
			},
		},
		{
			Role:       MessageRoleTool,
			ToolCallID: "call_grep",
			Content:    "3 matches in file.go",
		},
		{
			Role:       MessageRoleTool,
			ToolCallID: "call_read",
			Content:    "package main\n\nfunc main() {}",
		},
	}
	out := make([]Message, 0, messages)
	for len(out) < messages {
		out = append(out, cycle...)
	}
	return out
}

func BenchmarkCloneMessages(b *testing.B) {
	b.ReportAllocs()
	messages := perfConversation(500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cloneMessages(messages)
	}
}

func BenchmarkLineageFullMessages(b *testing.B) {
	b.ReportAllocs()
	lineage := newConversationLineage(perfConversation(500))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = lineage.FullMessages()
	}
}

func BenchmarkReplaySafeConversation(b *testing.B) {
	b.ReportAllocs()
	messages := perfConversation(500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ReplaySafeConversation(messages)
	}
}

func BenchmarkAssemblyOptions(b *testing.B) {
	b.ReportAllocs()
	state := RunState{
		Conversation: perfConversation(500),
		Lineage:      newConversationLineage(perfConversation(500)),
		Context:      ContextState{},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = assemblyOptions(prompt.AssemblyOptions{}, state)
	}
}

func BenchmarkPrepareTurnStatePrologue(b *testing.B) {
	b.ReportAllocs()
	state := RunState{
		Conversation: perfConversation(500),
		Lineage:      newConversationLineage(perfConversation(500)),
		Context:      ContextState{},
	}
	cm := NewContextStateManager()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prepared, err := cm.PrepareTurnState(context.Background(), state)
		if err != nil {
			b.Fatal(err)
		}
		_ = assemblyOptions(prompt.AssemblyOptions{}, prepared)
	}
}
