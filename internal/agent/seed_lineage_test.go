package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

// seedLineageWithState returns a single-generation lineage over messages whose
// generation carries state.
func seedLineageWithState(messages []Message, state json.RawMessage) ConversationLineage {
	lineage := newConversationLineage(messages)
	lineage.Generations[0].BackendState = state
	return lineage
}

func TestInitializeRunStateSeedLineage(t *testing.T) {
	t.Parallel()
	user := func(text string) Message { return Message{Role: MessageRoleUser, Content: text} }
	conversation := []Message{user("a"), user("b")}
	state := json.RawMessage(`{"session":"s1"}`)

	multi := newConversationLineage([]Message{user("old")})
	multi.Generations[0].BackendState = json.RawMessage(`{"stale":true}`)
	multi = multi.WithNewGeneration(nil, conversation)
	multi.Generations[1].BackendState = state

	tests := []struct {
		name string
		seed ConversationLineage
		want json.RawMessage
	}{
		{name: "empty seed starts without backend state", seed: ConversationLineage{}, want: nil},
		{name: "matching seed carries its backend state", seed: seedLineageWithState(conversation, state), want: state},
		{name: "multi-generation seed uses only the latest generation state", seed: multi, want: state},
		{name: "mismatched conversation drops backend state", seed: seedLineageWithState([]Message{user("other")}, state), want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := initializeRunState(RunRequest{SourceConversation: conversation, SeedLineage: tt.seed})
			if !reflect.DeepEqual(got.Lineage.latestBackendState(), tt.want) {
				t.Errorf("backend state = %s, want %s", got.Lineage.latestBackendState(), tt.want)
			}
			if len(got.Lineage.Generations) != 1 {
				t.Fatalf("generations = %d, want 1", len(got.Lineage.Generations))
			}
			if !reflect.DeepEqual(got.Lineage.FullMessages(), conversation) {
				t.Errorf("lineage messages = %v, want %v", got.Lineage.FullMessages(), conversation)
			}
		})
	}
}

func TestInitializeRunStateSeedLineageNoAliasing(t *testing.T) {
	t.Parallel()
	conversation := []Message{{Role: MessageRoleUser, Content: "a"}}
	seed := seedLineageWithState(conversation, json.RawMessage(`{"session":"s1"}`))

	state := initializeRunState(RunRequest{SourceConversation: conversation, SeedLineage: seed})
	state.Lineage.Generations[0].BackendState[2] = 'X'

	if got := string(seed.Generations[0].BackendState); got != `{"session":"s1"}` {
		t.Errorf("seed backend state mutated to %s", got)
	}
}

func TestRunSeedLineageBackendStateRoundTrip(t *testing.T) {
	t.Parallel()
	conversation := []Message{{Role: MessageRoleUser, Content: "hi"}}
	seed := seedLineageWithState(conversation, json.RawMessage(`{"session":"loaded"}`))
	prov := &fakeProvider{responses: []provider.ChatResponse{{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "ok"},
		FinishReason: "stop",
		BackendState: json.RawMessage(`{"session":"next"}`),
	}}}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider:           prov,
		Executor:           &fakeExecutor{},
		SourceConversation: conversation,
		SeedLineage:        seed,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(prov.requests))
	}
	if got := string(prov.requests[0].BackendState); got != `{"session":"loaded"}` {
		t.Errorf("request BackendState = %s, want the seeded state", got)
	}
	if got := string(state.Lineage.latestBackendState()); got != `{"session":"next"}` {
		t.Errorf("run BackendState = %s, want the response state", got)
	}
}
