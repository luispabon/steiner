package session

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

func TestRequestTextSurvivesSaveLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	const request = "the user's own words"
	lineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{{
			ID: 1,
			Messages: []agent.Message{
				{Role: agent.MessageRoleUser, Content: "[execution mode: plan] Plan mode.\n\nthe user's own words", RequestText: request},
				{Role: agent.MessageRoleUser, Source: agent.MessageSourceRuntimeNotice, Content: "You have used 5 of 10 turns"},
			},
		}},
		NextGenerationID: 2,
	}
	now := time.Now().UTC()
	saved := Session{ID: "request-text", CreatedAt: now, UpdatedAt: now, Title: "Request text", Model: "test-model", Lineage: lineage}
	if err := store.Save(saved); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	loaded, err := store.Load(saved.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	assertRequestText(t, loaded.Lineage.FullMessages(), request)

	// A resumed session re-saves what it loaded, so the field must survive a
	// second write rather than degrade to the content-only fallback.
	if err := store.Save(loaded); err != nil {
		t.Fatalf("re-Save failed: %v", err)
	}
	resumed, err := store.Load(saved.ID)
	if err != nil {
		t.Fatalf("second Load failed: %v", err)
	}
	assertRequestText(t, resumed.Lineage.FullMessages(), request)
}

func assertRequestText(t *testing.T, messages []agent.Message, want string) {
	t.Helper()
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
	if got := messages[0].RequestText; got != want {
		t.Fatalf("RequestText = %q, want %q", got, want)
	}
	if got := messages[1].Source; got != agent.MessageSourceRuntimeNotice {
		t.Fatalf("Source = %q, want %q", got, agent.MessageSourceRuntimeNotice)
	}
}
