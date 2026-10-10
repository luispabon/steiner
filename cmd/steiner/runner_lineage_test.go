package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// TestRunWithHooksLineageBackendStateRoundTrip checks that a loaded lineage's
// backend state reaches the provider request and that the state the response
// returns is carried by the lineage the next run is seeded with.
func TestRunWithHooksLineageBackendStateRoundTrip(t *testing.T) {
	firstPrompt := agent.Message{Role: agent.MessageRoleUser, Content: "first prompt"}
	seed := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{{
			ID:           1,
			Messages:     []agent.Message{firstPrompt},
			BackendState: json.RawMessage(`{"session":"loaded"}`),
		}},
		NextGenerationID: 2,
	}
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message:      provider.Message{Content: "first turn"},
				FinishReason: "stop",
				BackendState: json.RawMessage(`{"session":"next"}`),
			},
			{Message: provider.Message{Content: "second turn"}, FinishReason: "stop"},
		},
	}
	runner := cliRunner{
		runtime: cliRuntime{
			cfg:      testRuntimeConfig("test-model"),
			provider: prov,
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			events:   output.NoopSink{},
		},
		runMode: "interactive",
	}

	first, err := runner.RunWithHooks(context.Background(), seed.FullMessages(), nil, runHooks{lineage: seed})
	if err != nil {
		t.Fatalf("RunWithHooks() turn 1 error = %v", err)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(prov.requests))
	}
	if got := string(prov.requests[0].BackendState); got != `{"session":"loaded"}` {
		t.Fatalf("turn 1 request BackendState = %s, want the loaded state", got)
	}

	secondPrompt := agent.Message{Role: agent.MessageRoleUser, Content: "second prompt"}
	next := first.Lineage.WithAppendedMessages([]agent.Message{secondPrompt})
	if _, err := runner.RunWithHooks(context.Background(), next.FullMessages(), nil, runHooks{lineage: next}); err != nil {
		t.Fatalf("RunWithHooks() turn 2 error = %v", err)
	}
	if len(prov.requests) != 2 {
		t.Fatalf("captured %d requests, want 2", len(prov.requests))
	}
	if got := string(prov.requests[1].BackendState); got != `{"session":"next"}` {
		t.Errorf("turn 2 request BackendState = %s, want the state from turn 1", got)
	}
}
