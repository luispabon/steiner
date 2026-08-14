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

func TestConversationLineagePruneObsoleteIsConservative(t *testing.T) {
	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{{Role: MessageRoleUser, Content: "old user"}}),
			newConversationGeneration(2, []Message{{Role: MessageRoleSummary, Content: "summary"}}, []Message{{Role: MessageRoleUser, Content: "new user"}}),
		},
		NextGenerationID: 3,
	}

	kept := lineage.PruneObsolete()
	if got, want := len(kept.Generations), 2; got != want {
		t.Fatalf("kept generation count = %d, want %d", got, want)
	}
	if got, want := kept.Generations[0].ID, 1; got != want {
		t.Fatalf("kept generation[0] id = %d, want %d", got, want)
	}
	if got, want := kept.Generations[1].ID, 2; got != want {
		t.Fatalf("kept generation[1] id = %d, want %d", got, want)
	}
}

func TestConversationLineagePruneGenerationsBeforeDropsOnlyProvenObsoleteHistory(t *testing.T) {
	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{{Role: MessageRoleUser, Content: "old user"}}),
			newConversationGeneration(2, []Message{{Role: MessageRoleSummary, Content: "summary"}}, []Message{{Role: MessageRoleUser, Content: "new user"}}),
		},
		NextGenerationID: 3,
	}

	pruned := lineage.PruneGenerationsBefore(2)
	if got, want := len(pruned.Generations), 1; got != want {
		t.Fatalf("pruned generation count = %d, want %d", got, want)
	}
	if got, want := pruned.Generations[0].ID, 2; got != want {
		t.Fatalf("pruned generation id = %d, want %d", got, want)
	}
	if got, want := len(pruned.FullMessages()), 2; got != want {
		t.Fatalf("pruned full message count = %d, want %d", got, want)
	}
	if got, want := pruned.FullMessages()[0].Content, "summary"; got != want {
		t.Fatalf("pruned full[0] = %q, want %q", got, want)
	}
	if got, want := pruned.FullMessages()[1].Content, "new user"; got != want {
		t.Fatalf("pruned full[1] = %q, want %q", got, want)
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
			RetainedSummaries: []RetainedSummary{
				{Title: "earlier progress", Text: "implemented the scheduler", Source: "compaction", Turn: 2},
			},
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
	if got, want := withConversation.Context.RetainedSummaries[0].Text, "implemented the scheduler"; got != want {
		t.Fatalf("RetainedSummary text = %q, want %q", got, want)
	}

	withConversation.Context.RetainedSummaries[0].Text = "changed"

	if got, want := original.Context.RetainedSummaries[0].Text, "implemented the scheduler"; got != want {
		t.Fatalf("original retained summary text = %q, want %q", got, want)
	}
	if got, want := original.Lineage.FullMessages()[0].Content, "keep working"; got != want {
		t.Fatalf("original lineage content = %q, want %q", got, want)
	}

	withContext := original.WithContext(ContextState{
		RetainedSummaries: []RetainedSummary{
			{Title: "replacement", Text: "render compacted context blocks", Source: "planner", Turn: 4},
		},
	})

	if got, want := len(withContext.Conversation), len(original.Conversation); got != want {
		t.Fatalf("Conversation len = %d, want %d", got, want)
	}
	if got, want := withContext.Conversation[0].Content, "keep working"; got != want {
		t.Fatalf("Conversation content = %q, want %q", got, want)
	}
	if got, want := withContext.Context.RetainedSummaries[0].Text, "render compacted context blocks"; got != want {
		t.Fatalf("replacement RetainedSummary text = %q, want %q", got, want)
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
						Summary: "retained summary",
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
	if cloned.Generations[0].Messages[0].Retention.Summary != "retained summary" {
		t.Fatalf("cloned retention summary = %q, want retained summary", cloned.Generations[0].Messages[0].Retention.Summary)
	}
	cloned.Generations[0].Messages[0].Retention.Summary = "changed"
	if original.Generations[0].Messages[0].Retention.Summary != "retained summary" {
		t.Fatal("original retention mutated through clone")
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
						Summary:    "hidden summary",
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
	if got, want := restored.Generations[0].Messages[0].Retention.Summary, "hidden summary"; got != want {
		t.Fatalf("restored retention summary = %q, want %q", got, want)
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
