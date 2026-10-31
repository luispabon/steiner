package interactive

import (
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// benchSnapshotSizes are the conversation payload sizes reported by the
// snapshot benchmarks, matching internal/output's request-event benchmarks.
var benchSnapshotSizes = []int{256 << 10, 1 << 20}

// BenchmarkSnapshotSinkEmit measures the per-request cost of the sink that
// feeds the context report: building the event plus the deep copy
// SnapshotStore.Store takes of it.
func BenchmarkSnapshotSinkEmit(b *testing.B) {
	tools := benchSnapshotTools(24)
	for _, size := range benchSnapshotSizes {
		messages := benchSnapshotMessages(size)
		b.Run(fmt.Sprintf("conv%dKiB", size>>10), func(b *testing.B) {
			sink := &snapshotSink{store: &SnapshotStore{}}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sink.Emit(output.NewAPIRequestEvent("model", messages, tools, nil, nil, prompt.ModelTokenBudget{}, 0, 0))
			}
		})
	}
}

// BenchmarkSnapshotStoreStore measures the per-request cost of storing a
// request-context snapshot, which deep-copies the conversation and tools under
// the store mutex.
func BenchmarkSnapshotStoreStore(b *testing.B) {
	tools := benchSnapshotTools(24)
	for _, size := range benchSnapshotSizes {
		messages := benchSnapshotMessages(size)
		b.Run(fmt.Sprintf("conv%dKiB", size>>10), func(b *testing.B) {
			store := &SnapshotStore{}
			snapshot := RequestContextSnapshot{Model: "model", Messages: messages, Tools: tools}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				store.Store(snapshot)
			}
		})
	}
}

// BenchmarkSnapshotStoreRead measures the read path, which clones the stored
// snapshot before returning it.
func BenchmarkSnapshotStoreRead(b *testing.B) {
	tools := benchSnapshotTools(24)
	for _, size := range benchSnapshotSizes {
		messages := benchSnapshotMessages(size)
		b.Run(fmt.Sprintf("conv%dKiB", size>>10), func(b *testing.B) {
			store := &SnapshotStore{}
			store.Store(RequestContextSnapshot{Model: "model", Messages: messages, Tools: tools})
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = store.Snapshot()
			}
		})
	}
}

// benchSnapshotMessages builds a conversation whose message content totals
// roughly totalBytes: a user message, an assistant message with a tool call,
// and the matching tool result, repeated.
func benchSnapshotMessages(totalBytes int) []provider.Message {
	const chunk = 4 << 10
	count := totalBytes / chunk
	messages := make([]provider.Message, 0, count)
	for i := 0; len(messages) < count; i++ {
		messages = append(messages,
			provider.Message{Role: provider.MessageRoleUser, Content: benchSnapshotContent(chunk, i)},
			provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: benchSnapshotContent(chunk, i+1),
				ToolCalls: []provider.ToolCall{{
					ID:        fmt.Sprintf("call_%d", i),
					Name:      "read",
					Arguments: map[string]any{"path": "internal/interactive/state.go", "limit": 200},
				}},
			},
			provider.Message{Role: provider.MessageRoleTool, ToolCallID: fmt.Sprintf("call_%d", i), Content: benchSnapshotContent(chunk, i+2)},
		)
	}
	return messages[:count]
}

func benchSnapshotContent(n, seed int) string {
	line := fmt.Sprintf("message %d: the quick brown fox jumps over the lazy dog while the snapshot is stored\n", seed)
	repeated := strings.Repeat(line, n/len(line)+1)
	return repeated[:n]
}

func benchSnapshotTools(n int) []provider.ToolSpec {
	tools := make([]provider.ToolSpec, 0, n)
	for i := 0; i < n; i++ {
		tools = append(tools, provider.ToolSpec{
			Type: "function",
			Function: provider.ToolFunctionSpec{
				Name:        fmt.Sprintf("tool_%02d", i),
				Description: fmt.Sprintf("Tool %02d reads or writes project files.", i),
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":  map[string]any{"type": "string", "description": "Path to the file"},
						"limit": map[string]any{"type": "integer", "description": "Maximum lines"},
					},
					"required": []any{"path"},
				},
			},
		})
	}
	return tools
}
