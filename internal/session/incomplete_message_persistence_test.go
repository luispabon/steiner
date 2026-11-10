package session

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

// TestIncompleteAssistantMessageSurvivesSaveLoadAndFork pins the interrupted
// partial commit through session persistence: the Incomplete marker is stored,
// loaded back, and carried into a fork's latest generation.
func TestIncompleteAssistantMessageSurvivesSaveLoadAndFork(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	lineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{
				ID: 1,
				Messages: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "write the report", Turn: 1},
					{Role: agent.MessageRoleAssistant, Content: "the partial report", Turn: 2, Incomplete: true},
				},
			},
		},
		NextGenerationID: 2,
	}
	saved := Session{
		ID:        "partial-session",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Title:     "Partial output",
		Model:     "test-model",
		Lineage:   lineage,
	}
	if err := store.Save(saved); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load(saved.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	messages := loaded.Lineage.FullMessages()
	if len(messages) != 2 {
		t.Fatalf("loaded messages = %+v, want two", messages)
	}
	last := messages[len(messages)-1]
	if !last.Incomplete || last.Content != "the partial report" || last.Turn != 2 {
		t.Fatalf("loaded last message = %+v, want the incomplete partial at turn 2", last)
	}

	forked, err := Fork(loaded)
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}
	forkedMessages := forked.Lineage.FullMessages()
	if len(forkedMessages) != 2 {
		t.Fatalf("forked messages = %+v, want two", forkedMessages)
	}
	forkedLast := forkedMessages[len(forkedMessages)-1]
	if !forkedLast.Incomplete || forkedLast.Content != "the partial report" {
		t.Fatalf("forked last message = %+v, want the incomplete partial", forkedLast)
	}
}
