package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

// cacheBenchSizes are the conversation payload sizes perMessageHashes is
// benchmarked at, matching the request-event benchmarks in internal/output.
var cacheBenchSizes = []int{256 << 10, 1 << 20}

// BenchmarkPerMessageHashes measures the per-model-call fingerprint pass that
// feeds cache diagnostics, so a regression in memory hashing shows up on its
// own rather than folded into a full request build.
func BenchmarkPerMessageHashes(b *testing.B) {
	for _, size := range cacheBenchSizes {
		messages := cacheBenchMessages(size)
		b.Run(fmt.Sprintf("conv%dKiB", size>>10), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for i := 0; i < b.N; i++ {
				if hashes := perMessageHashes(messages); len(hashes) != len(messages) {
					b.Fatalf("len(hashes) = %d, want %d", len(hashes), len(messages))
				}
			}
		})
	}
}

// cacheBenchMessages builds a conversation whose content totals roughly
// totalBytes as a repeating cycle of a user message, an assistant message with
// a tool call, and the matching tool result.
func cacheBenchMessages(totalBytes int) []provider.Message {
	const chunk = 4 << 10
	count := totalBytes / chunk
	messages := make([]provider.Message, 0, count)
	for i := 0; len(messages) < count; i++ {
		messages = append(messages,
			provider.Message{Role: provider.MessageRoleUser, Content: cacheBenchContent(chunk, i)},
			provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: cacheBenchContent(chunk, i+1),
				ToolCalls: []provider.ToolCall{{
					ID:        fmt.Sprintf("call_%d", i),
					Name:      "read",
					Arguments: map[string]any{"path": "internal/agent/cache_diagnostics.go", "limit": 200},
				}},
			},
			provider.Message{Role: provider.MessageRoleTool, ToolCallID: fmt.Sprintf("call_%d", i), Content: cacheBenchContent(chunk, i+2)},
		)
	}
	return messages[:count]
}

func cacheBenchContent(n, seed int) string {
	line := fmt.Sprintf("message %d: the quick brown fox jumps over the lazy dog while the cache digest is built\n", seed)
	repeated := strings.Repeat(line, n/len(line)+1)
	return repeated[:n]
}
