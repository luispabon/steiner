package provider

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestClaudeSubscriptionProviderCapabilities(t *testing.T) {
	p := NewClaudeSubscriptionProvider(nil, nil)
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

// TestFoldClaudeSubChunksBackendStateTransitions proves ChatCompletion's fold
// carries the backend-state transition of the final chunk, as the streaming
// path does, so a non-stream caller can persist or clear the generation state.
func TestFoldClaudeSubChunksBackendStateTransitions(t *testing.T) {
	state := json.RawMessage(`{"transport_session":"parent"}`)
	tests := []struct {
		name      string
		chunks    []ChatChunk
		wantState json.RawMessage
		wantClear bool
	}{
		{
			name:      "state on final chunk",
			chunks:    []ChatChunk{{Delta: Message{Content: "hi"}}, {Done: true, BackendState: state}},
			wantState: state,
		},
		{
			name:      "clear on final chunk",
			chunks:    []ChatChunk{{Delta: Message{Content: "summary"}}, {Done: true, ClearBackendState: true}},
			wantClear: true,
		},
		{
			name:   "no transition",
			chunks: []ChatChunk{{Delta: Message{Content: "hi"}, Done: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := foldClaudeSubChunks(tt.chunks)
			if err != nil {
				t.Fatalf("foldClaudeSubChunks() error = %v", err)
			}
			if string(got.BackendState) != string(tt.wantState) {
				t.Errorf("BackendState = %s, want %s", got.BackendState, tt.wantState)
			}
			if got.ClearBackendState != tt.wantClear {
				t.Errorf("ClearBackendState = %v, want %v", got.ClearBackendState, tt.wantClear)
			}
		})
	}
}

// TestFoldClaudeSubChunksSnapshotAndReasoning pins the fold's text sources: a
// snapshot replaces the accumulated content, Thinking accumulates, and
// Delta.ReasoningContent fills reasoning only while no thinking has arrived.
func TestFoldClaudeSubChunksSnapshotAndReasoning(t *testing.T) {
	tests := []struct {
		name          string
		chunks        []ChatChunk
		wantContent   string
		wantReasoning string
	}{
		{
			name:        "later snapshot replaces earlier content",
			chunks:      []ChatChunk{{Delta: Message{Content: "a"}}, {Delta: Message{Content: "ab"}, ContentSnapshot: true}, {Delta: Message{Content: "abc"}, ContentSnapshot: true}, {Done: true}},
			wantContent: "abc",
		},
		{
			name:          "reasoning content fills empty reasoning",
			chunks:        []ChatChunk{{Delta: Message{ReasoningContent: "raw"}}, {Done: true}},
			wantReasoning: "raw",
		},
		{
			name:          "thinking wins over reasoning content",
			chunks:        []ChatChunk{{Thinking: "think"}, {Delta: Message{ReasoningContent: "raw"}}, {Done: true}},
			wantReasoning: "think",
		},
		{
			name:          "reasoning content first then thinking",
			chunks:        []ChatChunk{{Delta: Message{ReasoningContent: "raw"}}, {Thinking: "think"}, {Done: true}},
			wantReasoning: "rawthink",
		},
		{
			name:          "snapshot does not reset reasoning",
			chunks:        []ChatChunk{{Delta: Message{Content: "old"}, Thinking: "think"}, {Delta: Message{Content: "final"}, ContentSnapshot: true}, {Done: true}},
			wantContent:   "final",
			wantReasoning: "think",
		},
		{
			name:   "no text",
			chunks: []ChatChunk{{Done: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := foldClaudeSubChunks(tt.chunks)
			if err != nil {
				t.Fatalf("foldClaudeSubChunks() error = %v", err)
			}
			if got.Message.Content != tt.wantContent {
				t.Errorf("Content = %q, want %q", got.Message.Content, tt.wantContent)
			}
			if got.Message.ReasoningContent != tt.wantReasoning {
				t.Errorf("ReasoningContent = %q, want %q", got.Message.ReasoningContent, tt.wantReasoning)
			}
		})
	}
}

// TestFoldClaudeSubChunksErrorPaths pins the fold's failures: every error path
// returns the zero response and keeps the error identity it was given.
func TestFoldClaudeSubChunksErrorPaths(t *testing.T) {
	original := errors.New("overage")
	tests := []struct {
		name       string
		chunks     []ChatChunk
		wantIs     error
		wantString string
		wantTurn   bool
	}{
		{
			name:   "original error",
			chunks: []ChatChunk{{Error: original.Error(), OriginalError: original}},
			wantIs: original,
		},
		{
			name:       "string error",
			chunks:     []ChatChunk{{Delta: Message{Content: "partial"}, Error: "turn failed"}},
			wantString: "turn failed",
			wantTurn:   true,
		},
		{
			name:       "missing terminal chunk",
			chunks:     []ChatChunk{{Delta: Message{Content: "partial"}}},
			wantString: "claude_subscription stream closed without a terminal chunk",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := foldClaudeSubChunks(tt.chunks)
			if err == nil {
				t.Fatal("foldClaudeSubChunks() error = nil, want an error")
			}
			if !reflect.DeepEqual(got, ChatResponse{}) {
				t.Errorf("response = %+v, want the zero response", got)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("error = %v, want %v", err, tt.wantIs)
			}
			if tt.wantString != "" && err.Error() != tt.wantString {
				t.Errorf("error = %q, want %q", err.Error(), tt.wantString)
			}
			if tt.wantTurn {
				var turn *claudeSubTurnError
				if !errors.As(err, &turn) {
					t.Errorf("error = %T, want *claudeSubTurnError", err)
				}
			}
		})
	}
}

// TestClaudeSubChatCompletionCancellationAfterPartialChunk proves a cancel that
// lands after a partial chunk but before any terminal chunk still reports the
// context error, not the missing-terminal error, and no response. It drives the
// fold stream ChatCompletion drains, so the chunk is delivered before cancel.
func TestClaudeSubChatCompletionCancellationAfterPartialChunk(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := make(chan ChatChunk)
	go func() {
		stream <- ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: "partial"}}
		cancel()
		close(stream)
	}()
	got, err := claudeSubFoldStream(ctx, stream)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("claudeSubFoldStream() error = %v, want context.Canceled", err)
	}
	if !reflect.DeepEqual(got, ChatResponse{}) {
		t.Errorf("response = %+v, want the zero response", got)
	}
}

// TestClaudeSubChatCompletionCancellation proves a cancelled turn reports the
// context error and no partial response.
func TestClaudeSubChatCompletionCancellation(t *testing.T) {
	pool, _ := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := ChatRequest{
		TransportSession: "cancel",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("marker-prompt")},
	}
	got, err := NewClaudeSubscriptionProvider(pool, nil).ChatCompletion(ctx, req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ChatCompletion() error = %v, want context.Canceled", err)
	}
	if !reflect.DeepEqual(got, ChatResponse{}) {
		t.Errorf("response = %+v, want the zero response", got)
	}
}
