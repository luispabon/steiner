package agent

import (
	"context"
	"regexp"
	"slices"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestWithToolCallIDs(t *testing.T) {
	t.Run("all present returns input unchanged", func(t *testing.T) {
		calls := []provider.ToolCall{{ID: "a", Name: "read"}, {ID: "b", Name: "read"}}
		got := withToolCallIDs(calls)
		if &got[0] != &calls[0] {
			t.Fatal("withToolCallIDs copied a slice with no missing ids")
		}
	})
	t.Run("missing ids are filled on a copy", func(t *testing.T) {
		calls := []provider.ToolCall{{Name: "read"}, {ID: "keep", Name: "read"}, {Name: "read"}}
		got := withToolCallIDs(calls)
		if calls[0].ID != "" || calls[2].ID != "" {
			t.Fatalf("input mutated: %#v", calls)
		}
		if got[1].ID != "keep" {
			t.Fatalf("provided id = %q, want keep", got[1].ID)
		}
		pattern := regexp.MustCompile(`^call_[0-9a-f]{8}_[0-9]+$`)
		for _, i := range []int{0, 2} {
			if !pattern.MatchString(got[i].ID) {
				t.Fatalf("assigned id %q does not match %s", got[i].ID, pattern)
			}
		}
		if got[0].ID == got[2].ID {
			t.Fatalf("assigned ids collide: %q", got[0].ID)
		}
	})
}

func TestRunnerAssignsIDsToToolCallsWithoutOne(t *testing.T) {
	first := []provider.ToolCall{
		{Name: "read", Arguments: map[string]any{"path": "a"}},
		{ID: "given", Name: "read", Arguments: map[string]any{"path": "b"}},
		{Name: "read", Arguments: map[string]any{"path": "c"}},
	}
	providerStub := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: first}},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "done"}},
	}}
	executor := &fakeExecutor{execute: func(context.Context, string, map[string]any) (any, error) {
		return "ok", nil
	}}
	var mu sync.Mutex
	var started []string
	sink := output.SinkFunc(func(event output.Event) {
		if payload, ok := event.Payload.(output.ToolCallStartedEvent); ok {
			mu.Lock()
			started = append(started, payload.CallID)
			mu.Unlock()
		}
	})

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub, Executor: executor, Events: sink,
		Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "go"}}},
		ResolvedModel: provider.ResolvedModel{Alias: "m", BackendModelID: "m"},
		Limits:        Limits{MaxTurns: 3},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if first[0].ID != "" || first[2].ID != "" {
		t.Fatalf("provider response mutated: %#v", first)
	}

	var assistantIDs, resultIDs []string
	for _, message := range ToProviderMessages(state.Conversation) {
		for _, call := range message.ToolCalls {
			assistantIDs = append(assistantIDs, call.ID)
		}
		if message.Role == provider.MessageRoleTool {
			resultIDs = append(resultIDs, message.ToolCallID)
		}
	}
	if len(assistantIDs) != 3 || assistantIDs[0] == "" || assistantIDs[2] == "" || assistantIDs[0] == assistantIDs[2] {
		t.Fatalf("persisted call ids = %q, want three distinct non-empty ids", assistantIDs)
	}
	if assistantIDs[1] != "given" {
		t.Fatalf("provided call id = %q, want given", assistantIDs[1])
	}
	if !slices.Equal(resultIDs, assistantIDs) {
		t.Fatalf("tool result ids = %q, want %q", resultIDs, assistantIDs)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(started, assistantIDs) {
		t.Fatalf("ToolCallStarted ids = %q, want %q", started, assistantIDs)
	}

	sent := providerStub.requests[1].Messages
	var resent []string
	for _, message := range sent {
		for _, call := range message.ToolCalls {
			resent = append(resent, call.ID)
		}
	}
	if !slices.Equal(resent, assistantIDs) {
		t.Fatalf("ids sent back to provider = %q, want %q", resent, assistantIDs)
	}
}
