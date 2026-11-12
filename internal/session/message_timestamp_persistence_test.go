package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

func TestMessageTimestampsSurviveSaveLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	userTime := time.Date(2025, 3, 4, 5, 6, 7, 89, time.UTC)
	assistantTime := userTime.Add(90 * time.Second)
	lineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{{
			ID: 1,
			Messages: []agent.Message{
				{Role: agent.MessageRoleUser, Content: "hello", Timestamp: userTime},
				{Role: agent.MessageRoleAssistant, Content: "hi", Turn: 1, Timestamp: assistantTime},
			},
		}},
		NextGenerationID: 2,
	}
	saved := Session{
		ID:        "timestamped",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Title:     "Timestamps",
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
	assertMessageTimestamps(t, loaded.Lineage.FullMessages(), userTime, assistantTime)

	// A second resume reads the re-saved session, so the times must survive
	// another write rather than drifting to the save clock.
	if err := store.Save(loaded); err != nil {
		t.Fatalf("re-Save failed: %v", err)
	}
	resumed, err := store.Load(saved.ID)
	if err != nil {
		t.Fatalf("second Load failed: %v", err)
	}
	assertMessageTimestamps(t, resumed.Lineage.FullMessages(), userTime, assistantTime)
}

func TestLegacySessionWithoutTimestampsLoadsZero(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	legacy := `{"id":"legacy","created_at":"2025-01-02T03:04:05Z","updated_at":"2025-01-02T03:04:05Z","title":"Old","model":"m","lineage":{"generations":[{"id":1,"summary_prefix":null,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]}],"next_generation_id":2}}`
	path := filepath.Join(dir, "legacy.json")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy session: %v", err)
	}
	loaded, err := store.Load("legacy")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	messages := loaded.Lineage.FullMessages()
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
	for i, msg := range messages {
		if !msg.Timestamp.IsZero() {
			t.Fatalf("message %d Timestamp = %v, want zero", i, msg.Timestamp)
		}
	}
}

func assertMessageTimestamps(t *testing.T, messages []agent.Message, want ...time.Time) {
	t.Helper()
	if len(messages) != len(want) {
		t.Fatalf("messages = %d, want %d", len(messages), len(want))
	}
	for i := range want {
		if !messages[i].Timestamp.Equal(want[i]) {
			t.Fatalf("message %d Timestamp = %v, want %v", i, messages[i].Timestamp, want[i])
		}
	}
}
