package provider

import (
	"errors"
	"testing"
)

func TestClaudeSubscriptionProviderCapabilities(t *testing.T) {
	p := NewClaudeSubscriptionProvider(nil)
	if !p.SupportsUsageStats() {
		t.Error("SupportsUsageStats() = false, want true")
	}
	if !p.StatefulTranscript() {
		t.Error("StatefulTranscript() = false, want true")
	}
	if _, err := p.StreamChatCompletion(t.Context(), ChatRequest{}); err == nil {
		t.Fatal("nil pool stream returned nil error")
	}
}

func TestFoldClaudeSubChunks(t *testing.T) {
	usage := &UsageStats{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6}
	got, err := foldClaudeSubChunks([]ChatChunk{
		{Delta: Message{Role: MessageRoleAssistant, Content: "old"}},
		{Delta: Message{Content: "new"}, Thinking: "think"},
		{Delta: Message{Content: "final", ReasoningContent: "raw"}, ContentSnapshot: true, Done: true, FinishReason: "stop", Usage: usage},
	})
	if err != nil {
		t.Fatalf("foldClaudeSubChunks() error = %v", err)
	}
	if got.Message.Content != "final" || got.Message.ReasoningContent != "think" || got.FinishReason != "stop" {
		t.Fatalf("response = %+v, want final content, thinking, and stop", got)
	}
	if got.Message.Content == "oldnewfinal" {
		t.Fatal("snapshot final chunk duplicated streamed text")
	}
	if got.Usage != usage {
		t.Error("fold did not retain final usage pointer")
	}
}

func TestFoldClaudeSubChunksTextAndToolSnapshot(t *testing.T) {
	got, err := foldClaudeSubChunks([]ChatChunk{
		{Delta: Message{Role: MessageRoleAssistant, Content: "before"}},
		{Delta: Message{Content: "final", ToolCalls: []ToolCall{{ID: "call-1", Name: "read"}}}, ContentSnapshot: true, Done: true, FinishReason: "tool_calls"},
	})
	if err != nil {
		t.Fatalf("foldClaudeSubChunks() error = %v", err)
	}
	if got.Message.Content != "final" || len(got.Message.ToolCalls) != 1 || got.FinishReason != "tool_calls" {
		t.Fatalf("response = %+v, want final text and one tool call", got)
	}
}

func TestFoldClaudeSubChunksRequiresTerminal(t *testing.T) {
	if _, err := foldClaudeSubChunks([]ChatChunk{{Delta: Message{Content: "partial"}}}); err == nil {
		t.Fatal("fold without Done returned nil error")
	}
}

func TestFoldClaudeSubChunksPreservesOriginalError(t *testing.T) {
	want := errors.New("overage")
	_, err := foldClaudeSubChunks([]ChatChunk{{Error: want.Error(), OriginalError: want}})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want original error", err)
	}
}
