package output

import (
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// benchRequestSizes are the conversation payload sizes every request-event
// benchmark reports, so before/after numbers compare like for like.
var benchRequestSizes = []int{256 << 10, 1 << 20}

// BenchmarkNewAPIRequestEvent measures the per-request cost of building an
// APIRequestEvent, including message hashing, the tools byte count and the
// defensive deep copies.
func BenchmarkNewAPIRequestEvent(b *testing.B) {
	tools := benchRequestTools(24)
	blocks := benchRequestBlocks(8)
	maxTokens := 8192
	for _, size := range benchRequestSizes {
		messages := benchRequestMessages(size)
		b.Run(fmt.Sprintf("conv%dKiB", size>>10), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for i := 0; i < b.N; i++ {
				_ = NewAPIRequestEvent("model", messages, tools, &maxTokens, blocks, prompt.ModelTokenBudget{ContextSize: 200000}, 1000, 1200)
			}
		})
	}
}

// BenchmarkMessageHashes isolates the message fingerprint pass so a change to
// hashing can be measured without the deep copies and tool marshaling.
func BenchmarkMessageHashes(b *testing.B) {
	for _, size := range benchRequestSizes {
		messages := benchRequestMessages(size)
		b.Run(fmt.Sprintf("conv%dKiB", size>>10), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, promptBytes := requestMessageHashes(messages)
				if promptBytes == 0 {
					b.Fatal("no prompt bytes measured")
				}
			}
		})
	}
}

// BenchmarkRequestToolsBytes isolates the tools byte count, which does not
// depend on the conversation size and is encoded on every request.
func BenchmarkRequestToolsBytes(b *testing.B) {
	tools := benchRequestTools(24)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = requestToolsBytes(tools)
	}
}

// benchRequestMessages builds a conversation whose message content totals
// roughly totalBytes: a repeating cycle of a user message, an assistant
// message with a tool call, and the matching tool result.
func benchRequestMessages(totalBytes int) []provider.Message {
	const chunk = 4 << 10
	count := totalBytes / chunk
	messages := make([]provider.Message, 0, count)
	for i := 0; len(messages) < count; i++ {
		messages = append(messages,
			provider.Message{Role: provider.MessageRoleUser, Content: benchMessageContent(chunk, i)},
			provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: benchMessageContent(chunk, i+1),
				ToolCalls: []provider.ToolCall{{
					ID:        fmt.Sprintf("call_%d", i),
					Name:      "read",
					Arguments: map[string]any{"path": "internal/output/event_model_call.go", "limit": 200},
				}},
			},
			provider.Message{Role: provider.MessageRoleTool, ToolCallID: fmt.Sprintf("call_%d", i), Content: benchMessageContent(chunk, i+2)},
		)
	}
	return messages[:count]
}

func benchMessageContent(n, seed int) string {
	line := fmt.Sprintf("message %d: the quick brown fox jumps over the lazy dog while the event is built\n", seed)
	repeated := strings.Repeat(line, n/len(line)+1)
	return repeated[:n]
}

func benchRequestTools(n int) []provider.ToolSpec {
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

func benchRequestBlocks(n int) []prompt.ContextBlock {
	blocks := make([]prompt.ContextBlock, 0, n)
	for i := 0; i < n; i++ {
		blocks = append(blocks, prompt.ContextBlock{
			Source:   prompt.ContextSourceProjectContext,
			Path:     fmt.Sprintf("file_%02d.go", i),
			Content:  strings.Repeat("package output\n", 64),
			ByteSize: 15 * 64,
		})
	}
	return blocks
}
