package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/usagestats"
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
	}, 2, provider.ChatRequest{Model: "test"}, nil, budget, nil)
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
	}, nil, prompt.ModelTokenBudget{}, nil)
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
	}, nil, prompt.ModelTokenBudget{}, nil)
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
	}, nil, prompt.ModelTokenBudget{}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("completeModelCall() error = %v, want %v", err, boom)
	}
	if got := len(prov.requests); got != 2 {
		t.Fatalf("provider requests = %d, want 2", got)
	}
}

type testRecorder struct {
	obs []usagestats.Observation
}

func (t *testRecorder) Record(o usagestats.Observation) {
	t.obs = append(t.obs, o)
}

func TestRecordModelUsage(t *testing.T) {
	tests := []struct {
		name      string
		usage     *provider.UsageStats
		recorder  usageRecorder
		wantCount int
	}{
		{
			name: "usage-bearing response records one observation",
			usage: &provider.UsageStats{
				PromptTokens:             100,
				CompletionTokens:         50,
				CacheReadInputTokens:     10,
				CacheCreationInputTokens: 5,
			},
			recorder:  &testRecorder{},
			wantCount: 1,
		},
		{
			name:      "nil usage records nothing",
			usage:     nil,
			recorder:  &testRecorder{},
			wantCount: 0,
		},
		{
			name: "nil recorder records nothing",
			usage: &provider.UsageStats{
				PromptTokens:     100,
				CompletionTokens: 50,
			},
			recorder:  nil,
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := RunRequest{
				ResolvedModel: provider.ResolvedModel{
					ProviderAlias:         "gpt-4-test",
					EffectiveProviderType: config.ProviderTypeOpenAI,
					BackendModelID:        "gpt-4",
				},
				UsageRecorder: tc.recorder,
			}

			recordModelUsage(req, tc.usage)

			if tc.recorder == nil {
				return
			}

			tr, ok := tc.recorder.(*testRecorder)
			if !ok {
				return
			}

			if got := len(tr.obs); got != tc.wantCount {
				t.Errorf("observations count = %d, want %d", got, tc.wantCount)
			}

			if tc.wantCount > 0 {
				obs := tr.obs[0]
				if obs.PromptTokens != 100 {
					t.Errorf("PromptTokens = %d, want 100", obs.PromptTokens)
				}
				if obs.CompletionTokens != 50 {
					t.Errorf("CompletionTokens = %d, want 50", obs.CompletionTokens)
				}
				if obs.CacheReadTokens != 10 {
					t.Errorf("CacheReadTokens = %d, want 10", obs.CacheReadTokens)
				}
				if obs.CacheCreateTokens != 5 {
					t.Errorf("CacheCreateTokens = %d, want 5", obs.CacheCreateTokens)
				}
				if obs.ProviderAlias != "gpt-4-test" {
					t.Errorf("ProviderAlias = %q, want %q", obs.ProviderAlias, "gpt-4-test")
				}
				if obs.BackendModelID != "gpt-4" {
					t.Errorf("BackendModelID = %q, want %q", obs.BackendModelID, "gpt-4")
				}
				if obs.ProviderType != "openai" {
					t.Errorf("ProviderType = %q, want %q", obs.ProviderType, "openai")
				}
			}
		})
	}
}

func TestStreamRequiredDetection(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantTrue bool
	}{
		{
			name:     "detects stream required 400",
			err:      &provider.HTTPError{StatusCode: 400, Body: "Stream must be set to true"},
			wantTrue: true,
		},
		{
			name:     "detects stream required case insensitive",
			err:      &provider.HTTPError{StatusCode: 400, Body: "stream is required"},
			wantTrue: true,
		},
		{
			name:     "ignores non-400 errors",
			err:      &provider.HTTPError{StatusCode: 401, Body: "Stream must be set to true"},
			wantTrue: false,
		},
		{
			name:     "ignores 400 without stream message",
			err:      &provider.HTTPError{StatusCode: 400, Body: "invalid request"},
			wantTrue: false,
		},
		{
			name:     "ignores nil error",
			err:      nil,
			wantTrue: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isStreamRequiredError(tc.err)
			if got != tc.wantTrue {
				t.Errorf("isStreamRequiredError() = %v, want %v", got, tc.wantTrue)
			}
		})
	}
}

func TestAdaptiveStreamFallback(t *testing.T) {
	var skipNonStream bool

	prov := &fakeProvider{
		chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{}, &provider.HTTPError{
				StatusCode: 400,
				Status:     "400 Bad Request",
				Body:       "Stream must be set to true",
			}
		},
		streamFn: func(_ context.Context, _ provider.ChatRequest) (<-chan provider.ChatChunk, error) {
			chunks := make(chan provider.ChatChunk, 2)
			chunks <- provider.ChatChunk{Delta: provider.Message{Content: "hello"}}
			chunks <- provider.ChatChunk{Done: true, FinishReason: "stop"}
			close(chunks)
			return chunks, nil
		},
	}

	var events []output.Event
	eventSink := output.SinkFunc(func(event output.Event) { events = append(events, event) })

	// First call should detect the error and set skipNonStream
	_, _, err := completeModelCall(context.Background(), RunRequest{
		Provider:           prov,
		ModelBudget:        prompt.ModelTokenBudget{},
		Events:             eventSink,
		StreamingPreferred: false,
	}, 1, provider.ChatRequest{Model: "test"}, nil, prompt.ModelTokenBudget{}, &skipNonStream)

	if err != nil {
		t.Fatalf("completeModelCall() error = %v", err)
	}

	if !skipNonStream {
		t.Error("skipNonStream flag not set after stream required error")
	}

	// Check that a diagnostic event was emitted
	var sawDiagnostic bool
	for _, event := range events {
		if payload, ok := event.Payload.(output.ProviderDiagnosticEvent); ok && payload.Kind == "stream_required" {
			sawDiagnostic = true
		}
	}
	if !sawDiagnostic {
		t.Error("expected stream_required diagnostic event")
	}

	// Second call with skipNonStream = true should skip non-stream attempt
	events = nil
	prov.requests = nil
	_, _, err = completeModelCall(context.Background(), RunRequest{
		Provider:           prov,
		ModelBudget:        prompt.ModelTokenBudget{},
		Events:             eventSink,
		StreamingPreferred: false,
	}, 2, provider.ChatRequest{Model: "test"}, nil, prompt.ModelTokenBudget{}, &skipNonStream)

	if err != nil {
		t.Fatalf("completeModelCall() error = %v", err)
	}

	// Should only have streaming request, not non-stream
	if len(prov.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1 (streaming only)", len(prov.requests))
	}
}
