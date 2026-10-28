package agent

import (
	"encoding/json"
	"testing"
)

func TestConversationGenerationViewsArePrefixAware(t *testing.T) {
	generation := newConversationGeneration(7,
		[]Message{
			{Role: MessageRoleSummary, Content: "summary prefix one"},
			{Role: MessageRoleSummary, Content: "summary prefix two"},
		},
		[]Message{
			{Role: MessageRoleUser, Content: "raw user"},
			{Role: MessageRoleAssistant, Content: "raw assistant"},
		},
	)

	full := generation.FullMessages()
	if got, want := len(full), 4; got != want {
		t.Fatalf("full len = %d, want %d", got, want)
	}
	if got, want := full[0].Content, "summary prefix one"; got != want {
		t.Fatalf("full[0] = %q, want %q", got, want)
	}
	if got, want := full[3].Content, "raw assistant"; got != want {
		t.Fatalf("full[3] = %q, want %q", got, want)
	}

	stripped := generation.SummaryPrefixStrippedMessages()
	if got, want := len(stripped), 2; got != want {
		t.Fatalf("stripped len = %d, want %d", got, want)
	}
	if got, want := stripped[0].Content, "raw user"; got != want {
		t.Fatalf("stripped[0] = %q, want %q", got, want)
	}
	if got, want := stripped[1].Content, "raw assistant"; got != want {
		t.Fatalf("stripped[1] = %q, want %q", got, want)
	}

	full[0].Content = "changed"
	stripped[0].Content = "changed"
	if got, want := generation.SummaryPrefix[0].Content, "summary prefix one"; got != want {
		t.Fatalf("generation summary prefix mutated = %q, want %q", got, want)
	}
	if got, want := generation.Messages[0].Content, "raw user"; got != want {
		t.Fatalf("generation raw messages mutated = %q, want %q", got, want)
	}
}

func TestConversationLineageChoosesHighestFidelityCandidateDeterministically(t *testing.T) {
	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{
				{Role: MessageRoleUser, Content: "gen1 user"},
				{Role: MessageRoleAssistant, Content: "gen1 assistant"},
			}),
			newConversationGeneration(2,
				[]Message{{Role: MessageRoleSummary, Content: "summary for gen2"}},
				[]Message{
					{Role: MessageRoleUser, Content: "gen2 user"},
					{Role: MessageRoleAssistant, Content: "gen2 assistant"},
				},
			),
			newConversationGeneration(3,
				[]Message{
					{Role: MessageRoleSummary, Content: "summary for gen2"},
					{Role: MessageRoleSummary, Content: "summary for gen3"},
				},
				[]Message{
					{Role: MessageRoleUser, Content: "gen3 user"},
					{Role: MessageRoleAssistant, Content: "gen3 assistant"},
				},
			),
		},
		NextGenerationID: 4,
	}

	candidates := lineage.Candidates()
	if got, want := len(candidates), 5; got != want {
		t.Fatalf("candidate count = %d, want %d", got, want)
	}
	if got, want := candidates[0].GenerationID, 3; got != want {
		t.Fatalf("candidate[0] generation = %d, want %d", got, want)
	}
	if got, want := candidates[0].View, ConversationViewFull; got != want {
		t.Fatalf("candidate[0] view = %q, want %q", got, want)
	}
	if got, want := candidates[1].View, ConversationViewSummaryPrefixStripped; got != want {
		t.Fatalf("candidate[1] view = %q, want %q", got, want)
	}
	if got, want := candidates[1].Messages[0].Content, "gen3 user"; got != want {
		t.Fatalf("candidate[1] first message = %q, want %q", got, want)
	}
	if got, want := candidates[2].GenerationID, 2; got != want {
		t.Fatalf("candidate[2] generation = %d, want %d", got, want)
	}

	candidate, ok := lineage.HighestFidelityCandidate(func(messages []Message) bool {
		return len(messages) <= 2
	})
	if !ok {
		t.Fatal("HighestFidelityCandidate() ok = false, want true")
	}
	if got, want := candidate.GenerationID, 3; got != want {
		t.Fatalf("candidate generation = %d, want %d", got, want)
	}
	if got, want := candidate.View, ConversationViewSummaryPrefixStripped; got != want {
		t.Fatalf("candidate view = %q, want %q", got, want)
	}
	if got, want := len(candidate.Messages), 2; got != want {
		t.Fatalf("candidate messages len = %d, want %d", got, want)
	}
	if got, want := candidate.Messages[0].Content, "gen3 user"; got != want {
		t.Fatalf("candidate first message = %q, want %q", got, want)
	}

	fallback, ok := lineage.HighestFidelityCandidate(func(messages []Message) bool {
		return len(messages) == 0
	})
	if !ok {
		t.Fatal("fallback candidate ok = false, want true")
	}
	if got, want := fallback.GenerationID, 3; got != want {
		t.Fatalf("fallback generation = %d, want %d", got, want)
	}
	if got, want := fallback.View, ConversationViewFull; got != want {
		t.Fatalf("fallback view = %q, want %q", got, want)
	}
}

func TestRunStateUpdateHelpersPreserveDurableContext(t *testing.T) {
	original := RunState{
		TurnCount:  3,
		TokenCount: 27,
		StopReason: StopReasonMaxTokens,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "keep working"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "keep working"},
		}),
		Context: ContextState{
			RecentToolCalls: []string{"read"},
			TurnCount:       2,
		},
	}

	withConversation := original.WithConversation([]Message{
		{Role: MessageRoleAssistant, Content: "new turn"},
	})

	if got, want := withConversation.TurnCount, original.TurnCount; got != want {
		t.Fatalf("TurnCount = %d, want %d", got, want)
	}
	if got, want := withConversation.TokenCount, original.TokenCount; got != want {
		t.Fatalf("TokenCount = %d, want %d", got, want)
	}
	if got, want := withConversation.StopReason, original.StopReason; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(withConversation.Conversation), 1; got != want {
		t.Fatalf("Conversation len = %d, want %d", got, want)
	}
	if got, want := len(withConversation.Lineage.Generations), 1; got != want {
		t.Fatalf("Lineage generations = %d, want %d", got, want)
	}
	if got, want := withConversation.Lineage.FullMessages()[0].Content, "new turn"; got != want {
		t.Fatalf("Lineage full content = %q, want %q", got, want)
	}
	if got, want := withConversation.Context.RecentToolCalls[0], "read"; got != want {
		t.Fatalf("RecentToolCalls[0] = %q, want %q", got, want)
	}

	withConversation.Context.RecentToolCalls[0] = "changed"

	if got, want := original.Context.RecentToolCalls[0], "read"; got != want {
		t.Fatalf("original RecentToolCalls[0] = %q, want %q", got, want)
	}
	if got, want := original.Lineage.FullMessages()[0].Content, "keep working"; got != want {
		t.Fatalf("original lineage content = %q, want %q", got, want)
	}

	withContext := original.WithContext(ContextState{
		RecentToolCalls: []string{"mutate"},
	})

	if got, want := len(withContext.Conversation), len(original.Conversation); got != want {
		t.Fatalf("Conversation len = %d, want %d", got, want)
	}
	if got, want := withContext.Conversation[0].Content, "keep working"; got != want {
		t.Fatalf("Conversation content = %q, want %q", got, want)
	}
	if got, want := withContext.Context.RecentToolCalls[0], "mutate"; got != want {
		t.Fatalf("replacement RecentToolCalls[0] = %q, want %q", got, want)
	}
	if got, want := withContext.Lineage.FullMessages()[0].Content, "keep working"; got != want {
		t.Fatalf("WithContext lineage content = %q, want %q", got, want)
	}
}

func TestConversationLineageJSONRoundTrip(t *testing.T) {
	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{
				{Role: MessageRoleUser, Content: "gen1 user"},
				{Role: MessageRoleAssistant, Content: "gen1 assistant"},
			}),
			newConversationGeneration(2,
				[]Message{{Role: MessageRoleSummary, Content: "summary for gen2"}},
				[]Message{
					{Role: MessageRoleUser, Content: "gen2 user"},
					{Role: MessageRoleAssistant, Content: "gen2 assistant"},
				},
			),
		},
		NextGenerationID: 3,
	}

	data, err := json.Marshal(lineage)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored ConversationLineage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got, want := len(restored.Generations), 2; got != want {
		t.Fatalf("restored generation count = %d, want %d", got, want)
	}
	if got, want := restored.Generations[0].ID, 1; got != want {
		t.Fatalf("restored generation[0] id = %d, want %d", got, want)
	}
	if got, want := len(restored.Generations[0].Messages), 2; got != want {
		t.Fatalf("restored generation[0] messages len = %d, want %d", got, want)
	}
	if got, want := restored.Generations[0].Messages[0].Content, "gen1 user"; got != want {
		t.Fatalf("restored generation[0] message[0] = %q, want %q", got, want)
	}
	if got, want := restored.Generations[1].ID, 2; got != want {
		t.Fatalf("restored generation[1] id = %d, want %d", got, want)
	}
	if got, want := len(restored.Generations[1].SummaryPrefix), 1; got != want {
		t.Fatalf("restored generation[1] summary prefix len = %d, want %d", got, want)
	}
	if got, want := restored.Generations[1].SummaryPrefix[0].Content, "summary for gen2"; got != want {
		t.Fatalf("restored generation[1] summary = %q, want %q", got, want)
	}
	if got, want := len(restored.Generations[1].Messages), 2; got != want {
		t.Fatalf("restored generation[1] messages len = %d, want %d", got, want)
	}
	if got, want := restored.NextGenerationID, 3; got != want {
		t.Fatalf("restored next generation id = %d, want %d", got, want)
	}
}

func TestRunState_Clone(t *testing.T) {
	original := RunState{
		TurnCount:         3,
		TokenCount:        27,
		InputTokens:       11,
		CacheReadTokens:   22,
		CacheCreateTokens: 5,
		StopReason:        StopReasonMaxTokens,
	}

	cloned := original.Clone()

	if got, want := cloned.InputTokens, original.InputTokens; got != want {
		t.Fatalf("cloned.InputTokens = %d, want %d", got, want)
	}
	if got, want := cloned.CacheReadTokens, original.CacheReadTokens; got != want {
		t.Fatalf("cloned.CacheReadTokens = %d, want %d", got, want)
	}
	if got, want := cloned.CacheCreateTokens, original.CacheCreateTokens; got != want {
		t.Fatalf("cloned.CacheCreateTokens = %d, want %d", got, want)
	}
}

func TestConversationLineageClonePreservesRetentionMetadata(t *testing.T) {
	original := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{
				{
					Role:    MessageRoleTool,
					Content: "visible output",
					Retention: &MessageRetention{
						Kind:    "delegate_summary",
						AgentID: "child-1",
					},
				},
			}),
		},
		NextGenerationID: 2,
	}

	cloned := original.Clone()
	if cloned.Generations[0].Messages[0].Retention == nil {
		t.Fatal("cloned retention = nil, want copied metadata")
	}
	if cloned.Generations[0].Messages[0].Retention.AgentID != "child-1" {
		t.Fatalf("cloned retention agent = %q, want child-1", cloned.Generations[0].Messages[0].Retention.AgentID)
	}
	cloned.Generations[0].Messages[0].Retention.AgentID = "changed"
	if original.Generations[0].Messages[0].Retention.AgentID != "child-1" {
		t.Fatal("original retention mutated through clone")
	}
}

func TestConversationLineageOnlyLatest(t *testing.T) {
	tests := []struct {
		name        string
		generations []int
		nextID      int
		wantIDs     []int
	}{
		{name: "zero generations", nextID: 1},
		{name: "one generation", generations: []int{1}, nextID: 2, wantIDs: []int{1}},
		{name: "three generations keep latest", generations: []int{1, 2, 3}, nextID: 4, wantIDs: []int{3}},
		{name: "next ID stays ahead of latest", generations: []int{1, 2}, nextID: 9, wantIDs: []int{2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := ConversationLineage{NextGenerationID: tt.nextID}
			for _, id := range tt.generations {
				original.Generations = append(original.Generations,
					newConversationGeneration(id, nil, []Message{{Role: MessageRoleUser, Content: "message"}}))
			}

			got := original.OnlyLatest()
			if len(got.Generations) != len(tt.wantIDs) {
				t.Fatalf("generations = %d, want %d", len(got.Generations), len(tt.wantIDs))
			}
			for i, id := range tt.wantIDs {
				if gotID := got.Generations[i].ID; gotID != id {
					t.Errorf("generation %d ID = %d, want %d", i, gotID, id)
				}
			}
			if got.NextGenerationID != tt.nextID {
				t.Errorf("NextGenerationID = %d, want %d", got.NextGenerationID, tt.nextID)
			}
		})
	}
}

func TestConversationLineageOnlyLatestIsDeepCopy(t *testing.T) {
	original := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{{Role: MessageRoleUser, Content: "old"}}),
			newConversationGeneration(2, nil, []Message{{Role: MessageRoleUser, Content: "latest"}}),
		},
		NextGenerationID: 3,
	}

	got := original.OnlyLatest()
	original.Generations[1].Messages[0].Content = "MUTATED"
	if content := got.Generations[0].Messages[0].Content; content != "latest" {
		t.Errorf("OnlyLatest message = %q after source mutation, want latest", content)
	}
}

func TestConversationLineageJSONRoundTripPreservesRetentionMetadata(t *testing.T) {
	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{
				{
					Role:    MessageRoleTool,
					Content: "visible output",
					Retention: &MessageRetention{
						Kind:       "delegate_summary",
						AgentID:    "child-1",
						Status:     "complete",
						TurnCount:  4,
						TokenCount: 8120,
					},
				},
			}),
		},
		NextGenerationID: 2,
	}

	data, err := json.Marshal(lineage)
	if err != nil {
		t.Fatalf("marshal lineage: %v", err)
	}
	if string(data) == "" {
		t.Fatal("marshal produced empty output")
	}

	var restored ConversationLineage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal lineage: %v", err)
	}
	if restored.Generations[0].Messages[0].Retention == nil {
		t.Fatal("restored retention = nil, want persisted metadata")
	}
	if got, want := restored.Generations[0].Messages[0].Retention.AgentID, "child-1"; got != want {
		t.Fatalf("restored retention agent = %q, want %q", got, want)
	}
	if got, want := restored.Generations[0].Messages[0].Retention.Status, "complete"; got != want {
		t.Fatalf("restored retention status = %q, want %q", got, want)
	}
	if got, want := restored.Generations[0].Messages[0].Retention.TurnCount, 4; got != want {
		t.Fatalf("restored retention turn count = %d, want %d", got, want)
	}
	if got, want := restored.Generations[0].Messages[0].Retention.TokenCount, 8120; got != want {
		t.Fatalf("restored retention token count = %d, want %d", got, want)
	}
}

func TestConversationGenerationCloneDeepCopiesBackendState(t *testing.T) {
	original := newConversationLineage([]Message{{Role: MessageRoleUser, Content: "hi"}})
	original.Generations[0].BackendState = json.RawMessage(`{"session":"a"}`)

	clone := original.Clone()
	original.Generations[0].BackendState[12] = 'b'

	if got, want := string(clone.Generations[0].BackendState), `{"session":"a"}`; got != want {
		t.Fatalf("cloned BackendState = %q, want %q (aliased to original)", got, want)
	}
}

func TestConversationGenerationCloneNormalizesEmptyBackendState(t *testing.T) {
	tests := []struct {
		name  string
		state json.RawMessage
	}{
		{name: "nil", state: nil},
		{name: "zero length", state: json.RawMessage{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generation := ConversationGeneration{ID: 1, BackendState: tt.state}
			clone := generation.Clone()
			if clone.BackendState != nil {
				t.Fatalf("cloned BackendState = %q, want nil", clone.BackendState)
			}
			if _, err := json.Marshal(clone); err != nil {
				t.Fatalf("marshal cloned generation: %v", err)
			}
		})
	}
}

func TestConversationLineageWithBackendState(t *testing.T) {
	const first = `{"gen":1}`
	const second = `{"gen":2}`
	const replaced = `{"gen":"new"}`
	tests := []struct {
		name       string
		id         int
		state      json.RawMessage
		clearState bool
		want       [2]string // backend state of generation 1 and 2; "" means nil
	}{
		{name: "replaces latest generation", id: 2, state: json.RawMessage(replaced), want: [2]string{first, replaced}},
		{name: "nil state keeps stored state", id: 2, state: nil, want: [2]string{first, second}},
		{name: "zero length state keeps stored state", id: 2, state: json.RawMessage{}, want: [2]string{first, second}},
		{name: "clear removes latest state", id: 2, clearState: true, want: [2]string{first, ""}},
		{name: "clear takes precedence over state", id: 2, state: json.RawMessage(replaced), clearState: true, want: [2]string{first, ""}},
		{name: "targets earlier generation only", id: 1, state: json.RawMessage(replaced), want: [2]string{replaced, second}},
		{name: "unknown id changes nothing", id: 9, state: json.RawMessage(replaced), want: [2]string{first, second}},
	}
	base := newConversationLineage([]Message{{Role: MessageRoleUser, Content: "one"}}).
		WithNewGeneration(nil, []Message{{Role: MessageRoleUser, Content: "two"}})
	base.Generations[0].BackendState = json.RawMessage(first)
	base.Generations[1].BackendState = json.RawMessage(second)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := base.withBackendState(tt.id, tt.state, tt.clearState)
			for i, want := range tt.want {
				if gotState := string(got.Generations[i].BackendState); gotState != want {
					t.Errorf("generation %d BackendState = %q, want %q", got.Generations[i].ID, gotState, want)
				}
			}
			if string(base.Generations[0].BackendState) != first || string(base.Generations[1].BackendState) != second {
				t.Error("withBackendState mutated the receiver lineage")
			}
		})
	}
}

func TestConversationLineageWithBackendStateCopiesInput(t *testing.T) {
	base := newConversationLineage([]Message{{Role: MessageRoleUser, Content: "hi"}})
	state := json.RawMessage(`{"session":"a"}`)

	got := base.withBackendState(1, state, false)
	state[12] = 'b'

	if got, want := string(got.Generations[0].BackendState), `{"session":"a"}`; got != want {
		t.Fatalf("stored BackendState = %q, want %q (aliased to caller input)", got, want)
	}
}

func TestConversationLineageBackendStateJSON(t *testing.T) {
	lineage := newConversationLineage([]Message{{Role: MessageRoleUser, Content: "hi"}})
	lineage.Generations[0].BackendState = json.RawMessage(`{"session":"abc"}`)

	data, err := json.Marshal(lineage)
	if err != nil {
		t.Fatalf("marshal lineage: %v", err)
	}
	var loaded ConversationLineage
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal lineage: %v", err)
	}
	if got, want := string(loaded.Generations[0].BackendState), `{"session":"abc"}`; got != want {
		t.Errorf("round-tripped BackendState = %q, want %q", got, want)
	}

	legacy := `{"generations":[{"id":1,"summary_prefix":null,"messages":[{"role":"user","content":"hi"}]}],"next_generation_id":2}`
	var old ConversationLineage
	if err := json.Unmarshal([]byte(legacy), &old); err != nil {
		t.Fatalf("unmarshal legacy lineage: %v", err)
	}
	if old.Generations[0].BackendState != nil {
		t.Errorf("legacy BackendState = %q, want nil", old.Generations[0].BackendState)
	}
}

func TestConversationLineageViewsAreFreshClonesAndLatestMessagesAliases(t *testing.T) {
	lineage := newConversationLineage([]Message{
		{Role: MessageRoleUser, Content: "user"},
		{
			Role:    MessageRoleAssistant,
			Content: "assistant",
			ToolCalls: []ToolCall{
				{ID: "call_read", Name: "read", Arguments: map[string]any{"path": "file.go"}},
			},
		},
	})

	full := lineage.FullMessages()
	if got, want := len(full), 2; got != want {
		t.Fatalf("FullMessages() len = %d, want %d", got, want)
	}
	stripped := lineage.SummaryPrefixStrippedMessages()
	if got, want := len(stripped), 2; got != want {
		t.Fatalf("SummaryPrefixStrippedMessages() len = %d, want %d", got, want)
	}

	// Mutating the returned views must not reach the stored generation.
	full[1].Content = "changed"
	full[1].ToolCalls[0].Name = "mutate"
	full[1].ToolCalls[0].Arguments["path"] = "other.go"
	stripped[1].Content = "changed"
	stripped[1].ToolCalls[0].Name = "mutate"
	stripped[1].ToolCalls[0].Arguments["path"] = "other.go"

	stored := lineage.Generations[0].Messages[1]
	if got, want := stored.Content, "assistant"; got != want {
		t.Fatalf("stored message mutated via view = %q, want %q", got, want)
	}
	if got, want := stored.ToolCalls[0].Name, "read"; got != want {
		t.Fatalf("stored tool call name mutated via view = %q, want %q", got, want)
	}
	if got, want := stored.ToolCalls[0].Arguments["path"], "file.go"; got != want {
		t.Fatalf("stored tool call arguments mutated via view = %v, want %q", got, want)
	}

	// latestMessages is the no-clone accessor: same content, shared backing.
	latest := lineage.latestMessages()
	if got, want := len(latest), 2; got != want {
		t.Fatalf("latestMessages() len = %d, want %d", got, want)
	}
	if got, want := latest[1].Content, "assistant"; got != want {
		t.Fatalf("latestMessages()[1].Content = %q, want %q", got, want)
	}
	if got, want := latest[1].ToolCalls[0].Name, "read"; got != want {
		t.Fatalf("latestMessages()[1].ToolCalls[0].Name = %q, want %q", got, want)
	}
	if &latest[0] != &lineage.Generations[0].Messages[0] {
		t.Fatal("latestMessages() did not share backing with the stored generation; expected no clone")
	}

	// Empty lineage: both views and the raw accessor return nil.
	empty := ConversationLineage{}
	if got := empty.FullMessages(); got != nil {
		t.Fatalf("empty FullMessages() = %v, want nil", got)
	}
	if got := empty.SummaryPrefixStrippedMessages(); got != nil {
		t.Fatalf("empty SummaryPrefixStrippedMessages() = %v, want nil", got)
	}
	if got := empty.latestMessages(); got != nil {
		t.Fatalf("empty latestMessages() = %v, want nil", got)
	}
}
