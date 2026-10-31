package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestAdvanceReusesPrepareTurnFitForEachModelCall(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-stream", true: "stream"}[streaming], func(t *testing.T) {
			tracker := &trackingTokenCounter{TokenCounter: provider.NewTokenCounter()}
			restore := provider.SwapDefaultTokenCounter(tracker)
			defer restore()

			response := provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "ok"}}
			prov := &fakeProvider{}
			if streaming {
				prov.streamFn = func(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
					chunks := make(chan provider.ChatChunk, 1)
					chunks <- provider.ChatChunk{Done: true, FinishReason: "stop"}
					close(chunks)
					return chunks, nil
				}
			} else {
				prov.chatFn = func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
					return response, nil
				}
			}

			state := RunState{
				Conversation: []Message{{Role: MessageRoleUser, Content: "hello"}},
				Lineage:      newConversationLineage([]Message{{Role: MessageRoleUser, Content: "hello"}}),
			}
			req := RunRequest{
				Provider:           prov,
				Prompt:             prompt.AssemblyOptions{},
				ModelBudget:        prompt.ModelTokenBudget{ContextSize: 4096, MaxCompletionTokens: 128},
				ResolvedModel:      provider.ResolvedModel{Alias: "test-model", BackendModelID: "test-model"},
				Limits:             Limits{MaxTurns: 1},
				Events:             output.NoopSink{},
				StreamingPreferred: streaming,
			}

			outcome := newTurnProgressor(req, prompt.AssemblyOptions{}, nil).advance(context.Background(), state)
			if outcome.Error != nil {
				t.Fatalf("advance() error = %v", outcome.Error)
			}
			if tracker.estimateCalls != 1 {
				t.Fatalf("estimate calls = %d, want 1", tracker.estimateCalls)
			}
		})
	}
}

func TestAdvanceReusesPrefitNumbersWithoutContextWindow(t *testing.T) {
	tracker := &trackingTokenCounter{TokenCounter: provider.NewTokenCounter()}
	restore := provider.SwapDefaultTokenCounter(tracker)
	defer restore()

	var events []output.Event
	prov := &fakeProvider{
		chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "ok"}}, nil
		},
	}
	state := RunState{
		Conversation: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Lineage:      newConversationLineage([]Message{{Role: MessageRoleUser, Content: "hello"}}),
	}
	req := RunRequest{
		Provider:      prov,
		ModelBudget:   prompt.ModelTokenBudget{},
		ResolvedModel: provider.ResolvedModel{Alias: "test-model", BackendModelID: "test-model"},
		Limits:        Limits{MaxTurns: 1},
		Events:        output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	}

	outcome := newTurnProgressor(req, prompt.AssemblyOptions{}, nil).advance(context.Background(), state)
	if outcome.Error != nil {
		t.Fatalf("advance() error = %v", outcome.Error)
	}
	if tracker.estimateCalls != 1 {
		t.Fatalf("estimate calls = %d, want 1", tracker.estimateCalls)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(prov.requests))
	}
	var event output.APIRequestEvent
	for _, candidate := range events {
		if payload, ok := candidate.Payload.(output.APIRequestEvent); ok {
			event = payload
			break
		}
	}
	if event.EstimatedPromptTokens <= 0 || event.RawPromptTokens <= 0 {
		t.Fatalf("API request estimates = (%d, %d), want positive prefit values", event.EstimatedPromptTokens, event.RawPromptTokens)
	}
	if got := prov.requests[0].RawPromptTokenEstimate; got != event.RawPromptTokens {
		t.Fatalf("request raw estimate = %d, want event raw estimate %d", got, event.RawPromptTokens)
	}
}

func TestAdvanceReestimatesWhenVisionStripChangesRequest(t *testing.T) {
	tracker := &trackingTokenCounter{TokenCounter: provider.NewTokenCounter()}
	restore := provider.SwapDefaultTokenCounter(tracker)
	defer restore()

	vision := false
	image := ImageBlock{MediaType: "image/png", Data: "image-data"}
	state := RunState{
		Conversation: []Message{{Role: MessageRoleUser, Content: "describe", Images: []ImageBlock{image}}},
		Lineage:      newConversationLineage([]Message{{Role: MessageRoleUser, Content: "describe", Images: []ImageBlock{image}}}),
	}
	prov := &fakeProvider{
		chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "ok"}}, nil
		},
	}
	req := RunRequest{
		Provider:      prov,
		ModelBudget:   prompt.ModelTokenBudget{ContextSize: 4096, MaxCompletionTokens: 128},
		ResolvedModel: provider.ResolvedModel{Alias: "test-model", BackendModelID: "test-model", Vision: &vision},
		Limits:        Limits{MaxTurns: 1},
		Events:        output.NoopSink{},
	}

	outcome := newTurnProgressor(req, prompt.AssemblyOptions{}, nil).advance(context.Background(), state)
	if outcome.Error != nil {
		t.Fatalf("advance() error = %v", outcome.Error)
	}
	if tracker.estimateCalls != 2 {
		t.Fatalf("estimate calls = %d, want 2", tracker.estimateCalls)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(prov.requests))
	}
	stripped := prov.requests[0]
	want, err := tracker.TokenCounter.EstimateChatRequestTokens(context.Background(), stripped)
	if err != nil {
		t.Fatalf("estimate stripped request: %v", err)
	}
	if stripped.RawPromptTokenEstimate != want.RawTokens {
		t.Fatalf("request raw estimate = %d, want fresh stripped estimate %d", stripped.RawPromptTokenEstimate, want.RawTokens)
	}
}

func TestCompleteModelCallInlineVisionRetryReestimates(t *testing.T) {
	tracker := &trackingTokenCounter{TokenCounter: provider.NewTokenCounter()}
	restore := provider.SwapDefaultTokenCounter(tracker)
	defer restore()

	vision := true
	prov := &fakeProvider{
		chatFn: func(_ context.Context, request provider.ChatRequest) (provider.ChatResponse, error) {
			for _, message := range request.Messages {
				if len(message.Images) > 0 {
					return provider.ChatResponse{}, &provider.HTTPError{StatusCode: 400, Body: "image input is not supported"}
				}
			}
			return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "ok"}}, nil
		},
		streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
			return nil, errors.New("stream unavailable")
		},
	}
	chatRequest := provider.ChatRequest{
		Model:    "test-model",
		Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "describe", Images: []provider.ImageBlock{{MediaType: "image/png", Data: "image-data"}}}},
	}
	prefitEstimate, err := tracker.EstimateChatRequestTokens(context.Background(), chatRequest)
	if err != nil {
		t.Fatalf("estimate prefit request: %v", err)
	}
	prefit := prompt.RequestTokenBudget{
		EstimatedPromptTokens:    prefitEstimate.Tokens,
		RawEstimatedPromptTokens: prefitEstimate.RawTokens,
		Fits:                     true,
	}
	_, _, err = completeModelCallWithOptions(context.Background(), RunRequest{
		Provider:           prov,
		ResolvedModel:      provider.ResolvedModel{Alias: "test-model", BackendModelID: "test-model", Vision: &vision},
		StreamingPreferred: false,
	}, 1, chatRequest, nil, prompt.ModelTokenBudget{ContextSize: 4096}, nil, chatExecOptions{prefit: &prefit})
	if err != nil {
		t.Fatalf("completeModelCallWithOptions() error = %v", err)
	}
	// One estimate builds the prefit and one re-estimates the stripped retry.
	// Forwarding the incoming prefit into the retry leaves this at 1.
	if tracker.estimateCalls != 2 {
		t.Fatalf("estimate calls = %d, want 2", tracker.estimateCalls)
	}
	if len(prov.requests) < 2 || len(prov.requests[len(prov.requests)-1].Messages[0].Images) != 0 {
		t.Fatalf("retry request = %+v, want stripped images", prov.requests)
	}
	if prov.requests[0].RawPromptTokenEstimate == prov.requests[len(prov.requests)-1].RawPromptTokenEstimate {
		t.Fatalf("first and retry raw estimates = %d, want different values", prov.requests[0].RawPromptTokenEstimate)
	}
}

func TestExecuteChatRequestReusesNonFitPrefit(t *testing.T) {
	prov := &fakeProvider{}
	fit := prompt.RequestTokenBudget{EstimatedPromptTokens: 12, RawEstimatedPromptTokens: 9, Fits: false}
	_, _, err := executeChatRequestWithOptions(context.Background(), prov, 1, provider.ChatRequest{Model: "test-model"}, prompt.ModelTokenBudget{ContextSize: 4096}, output.NoopSink{}, nil, false, true, nil, chatExecOptions{prefit: &fit}, nil)
	if err == nil || err.Error() != "request exceeds context window: prompt=12 usage=0% hard_limit=0 compact=false fits=false" {
		t.Fatalf("error = %v, want context-window error", err)
	}
}
