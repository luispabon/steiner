package main

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
)

// TestDriverRunLastMainRequestRoundTrip proves the driver adapter seeds an
// episode from DriverRunInput.LastMainRequest and returns the run's request in
// DriverRunOutput.LastMainRequest, on error as well.
func TestDriverRunLastMainRequestRoundTrip(t *testing.T) {
	seedAt := time.Unix(100, 0)
	reply := provider.ChatResponse{Message: provider.Message{Content: "reply"}, FinishReason: "stop"}
	toolCall := provider.ChatResponse{
		Message: provider.Message{
			Role:      provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "probe"}},
		},
		FinishReason: "tool_calls",
	}
	tests := []struct {
		name      string
		responses []provider.ChatResponse
		// wantErr reports the episode ends with a provider error.
		wantErr bool
		// wantCall reports the episode ends with a main call it made, not the seed.
		wantCall bool
	}{
		{name: "success returns the main call", responses: []provider.ChatResponse{reply}, wantCall: true},
		{name: "failed first call keeps the seed", wantErr: true},
		{name: "error after a main call returns that call", responses: []provider.ChatResponse{toolCall}, wantErr: true, wantCall: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := parentUsageTestRunner(t, &fakeProvider{responses: tt.responses})
			seed := lastMainRequestSeedFor(t, runner, seedAt)

			out, err := runner.driverRun(nil, "", nil)(context.Background(), agent.DriverRunInput{
				Conversation:    []agent.Message{{Role: agent.MessageRoleUser, Content: "hi"}},
				LastMainRequest: seed,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("driver run error = %v, want error = %v", err, tt.wantErr)
			}
			got := out.LastMainRequest
			if !tt.wantCall {
				if got != seed {
					t.Errorf("DriverRunOutput.LastMainRequest = %+v, want the seed %+v", got, seed)
				}
				return
			}
			if got.At.IsZero() || got.At.Equal(seedAt) {
				t.Errorf("LastMainRequest.At = %v, want the time of the main call", got.At)
			}
			if got.ProviderAlias != seed.ProviderAlias || got.BackendModelID != seed.BackendModelID || got.PromptCacheKey != seed.PromptCacheKey {
				t.Errorf("LastMainRequest = %+v, want the model identity of %+v", got, seed)
			}
		})
	}
}
