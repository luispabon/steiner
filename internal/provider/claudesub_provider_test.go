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
	if got.Usage != usage {
		t.Error("fold did not retain final usage pointer")
	}
}

func TestFoldClaudeSubChunksPreservesOriginalError(t *testing.T) {
	want := errors.New("overage")
	_, err := foldClaudeSubChunks([]ChatChunk{{Error: want.Error(), OriginalError: want}})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want original error", err)
	}
}
