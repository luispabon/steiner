package agent

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestCompleteScaffoldInferenceCallEmitsScaffoldChunkSources(t *testing.T) {
	chunks := make(chan provider.ChatChunk, 3)
	chunks <- provider.ChatChunk{
		Thinking: "infer intent",
		Delta:    provider.Message{Content: `{"intent":"inspect"`},
	}
	chunks <- provider.ChatChunk{
		Done:         true,
		Delta:        provider.Message{Content: `{"intent":"inspect","next":"read file"}`},
		FinishReason: "stop",
	}
	close(chunks)

	prov := &fakeProvider{
		streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
			return chunks, nil
		},
	}

	var events []output.Event
	_, err := completeScaffoldInferenceCall(context.Background(), RunRequest{
		Provider: prov,
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 128,
		},
		Events:             output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		StreamingPreferred: true,
	}, 3, provider.ChatRequest{Model: "test"})
	if err != nil {
		t.Fatalf("completeScaffoldInferenceCall() error = %v", err)
	}

	var gotThinkingSource, gotAssistantSource output.ChunkSource
	for _, event := range events {
		switch payload := event.Payload.(type) {
		case output.ThinkingChunkEvent:
			gotThinkingSource = payload.Source
		case output.AssistantChunkEvent:
			gotAssistantSource = payload.Source
		}
	}
	if gotThinkingSource != output.ChunkSourceScaffoldInference {
		t.Fatalf("thinking chunk source = %q, want %q", gotThinkingSource, output.ChunkSourceScaffoldInference)
	}
	if gotAssistantSource != output.ChunkSourceScaffoldInference {
		t.Fatalf("assistant chunk source = %q, want %q", gotAssistantSource, output.ChunkSourceScaffoldInference)
	}
}

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
	_, err := completeModelCall(context.Background(), RunRequest{
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
