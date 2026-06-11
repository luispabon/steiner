package agent

import (
	"context"
	"errors"
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

func TestCompleteModelCallRetriesHTTP400WithoutImages(t *testing.T) {
	prov := &fakeProvider{
		chatFn: func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
			if len(req.Messages) == 0 {
				t.Fatal("request has no messages")
			}
			if len(req.Messages[0].Images) > 0 {
				return provider.ChatResponse{}, &provider.HTTPError{StatusCode: 400, Status: "400 Bad Request"}
			}
			return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "ok"}}, nil
		},
	}
	var events []output.Event
	vision := true
	_, _, err := completeModelCall(context.Background(), RunRequest{
		Provider: prov,
		ResolvedModel: provider.ResolvedModel{
			Alias:  "test-model",
			Vision: &vision,
		},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	}, 1, provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.Message{{
			Role:    provider.MessageRoleUser,
			Content: "analyze",
			Images:  []provider.ImageBlock{{MediaType: "image/png", Data: "abc"}},
		}},
	}, nil, prompt.ModelTokenBudget{})
	if err != nil {
		t.Fatalf("completeModelCall() error = %v", err)
	}
	if got := len(prov.requests); got != 3 {
		t.Fatalf("provider requests = %d, want 3", got)
	}
	if len(prov.requests[2].Messages[0].Images) != 0 {
		t.Fatal("retry request still contains images")
	}
	var sawFallback bool
	for _, event := range events {
		payload, ok := event.Payload.(output.ProviderDiagnosticEvent)
		if !ok {
			continue
		}
		if payload.Kind == "vision_fallback" {
			sawFallback = true
		}
	}
	if !sawFallback {
		t.Fatal("expected vision_fallback diagnostic event")
	}
}

func TestCompleteModelCallDoesNotRetryWithoutImages(t *testing.T) {
	prov := &fakeProvider{
		chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{}, &provider.HTTPError{StatusCode: 400, Status: "400 Bad Request"}
		},
	}
	_, _, err := completeModelCall(context.Background(), RunRequest{
		Provider: prov,
		ResolvedModel: provider.ResolvedModel{
			Alias: "test-model",
		},
	}, 1, provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.Message{{
			Role:    provider.MessageRoleUser,
			Content: "analyze",
		}},
	}, nil, prompt.ModelTokenBudget{})
	if err == nil {
		t.Fatal("completeModelCall() error = nil, want HTTPError")
	}
	if got := len(prov.requests); got != 2 {
		t.Fatalf("provider requests = %d, want 2", got)
	}
}

func TestCompleteModelCallDoesNotRetryNon400(t *testing.T) {
	boom := errors.New("boom")
	prov := &fakeProvider{
		chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{}, boom
		},
	}
	_, _, err := completeModelCall(context.Background(), RunRequest{
		Provider: prov,
		ResolvedModel: provider.ResolvedModel{
			Alias: "test-model",
		},
	}, 1, provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.Message{{
			Role:    provider.MessageRoleUser,
			Content: "analyze",
			Images:  []provider.ImageBlock{{MediaType: "image/png", Data: "abc"}},
		}},
	}, nil, prompt.ModelTokenBudget{})
	if !errors.Is(err, boom) {
		t.Fatalf("completeModelCall() error = %v, want %v", err, boom)
	}
	if got := len(prov.requests); got != 2 {
		t.Fatalf("provider requests = %d, want 2", got)
	}
}
