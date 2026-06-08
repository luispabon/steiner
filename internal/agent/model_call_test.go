package agent

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestCompleteModelCallEmitsAssistantChunkSource(t *testing.T) {
	chunks := make(chan provider.ChatChunk, 2)
	chunks <- provider.ChatChunk{
		Thinking: "reasoning",
		Delta:    provider.Message{Content: "hello"},
	}
	chunks <- provider.ChatChunk{
		Done:         true,
		FinishReason: "stop",
	}
	close(chunks)

	prov := &fakeProvider{
		streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
			return chunks, nil
		},
	}

	var events []output.Event
	budget := prompt.ModelTokenBudget{
		ContextSize:         4096,
		MaxCompletionTokens: 128,
	}
	_, _, err := completeModelCall(context.Background(), RunRequest{
		Provider:           prov,
		ModelBudget:        budget,
		Events:             output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		StreamingPreferred: true,
	}, 2, provider.ChatRequest{Model: "test"}, nil, budget)
	if err != nil {
		t.Fatalf("completeModelCall() error = %v", err)
	}

	for _, event := range events {
		switch payload := event.Payload.(type) {
		case output.ThinkingChunkEvent:
			if payload.Source != output.ChunkSourceAssistant {
				t.Fatalf("thinking chunk source = %q, want %q", payload.Source, output.ChunkSourceAssistant)
			}
		case output.AssistantChunkEvent:
			if payload.Source != output.ChunkSourceAssistant {
				t.Fatalf("assistant chunk source = %q, want %q", payload.Source, output.ChunkSourceAssistant)
			}
		}
	}
}

func TestHandleFinalChunkCopiesReasoningContent(t *testing.T) {
	tests := []struct {
		name                  string
		chunkReasoningContent string
		wantReasoningContent  string
	}{
		{
			name:                  "reasoning content copied from final chunk delta",
			chunkReasoningContent: "step by step reasoning",
			wantReasoningContent:  "step by step reasoning",
		},
		{
			name:                  "empty reasoning content leaves message unchanged",
			chunkReasoningContent: "",
			wantReasoningContent:  "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink := output.SinkFunc(func(output.Event) {})
			chunk := provider.ChatChunk{
				Done:         true,
				FinishReason: "stop",
				Delta:        provider.Message{ReasoningContent: tc.chunkReasoningContent},
			}
			var response provider.ChatResponse
			var message provider.Message
			handleFinalChunk(sink, 1, output.ChunkSourceAssistant, chunk, &response, &message)
			if message.ReasoningContent != tc.wantReasoningContent {
				t.Errorf("ReasoningContent=%q, want %q", message.ReasoningContent, tc.wantReasoningContent)
			}
		})
	}
}
