package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func datedLineage() agent.ConversationLineage {
	return agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{ID: 1, Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "hello"}}},
		},
		NextGenerationID: 2,
	}
}

func TestStoreRoundTripsSessionDate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	record, err := NewSession("test-model", datedLineage())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	record.SessionDate = &StartDate{Date: "2026-09-13", Zone: "BST", Offset: "+01:00"}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(record.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.SessionDate == nil {
		t.Fatal("SessionDate = nil after reload, want the stored date")
	}
	if got, want := *loaded.SessionDate, *record.SessionDate; got != want {
		t.Fatalf("SessionDate = %#v, want %#v", got, want)
	}

	data, err := os.ReadFile(filepath.Join(dir, record.ID+sessionExt))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	if !strings.Contains(string(data), `"session_date"`) {
		t.Fatalf("session file does not contain session_date: %s", data)
	}
}

func TestSessionDateOmittedWhenUnset(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(Session{ID: "undated"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "session_date") {
		t.Fatalf("marshalled session contains session_date without a date: %s", data)
	}
}

func TestLoadRecordWithoutSessionDateLeavesItNil(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if _, err := NewStore(dir); err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	legacy := `{"id":"legacy-date","created_at":"2026-09-13T20:00:00Z","updated_at":"2026-09-13T20:00:00Z","title":"Legacy","model":"test-model","lineage":{"generations":[{"id":1,"summary_prefix":null,"messages":[{"role":"user","content":"hi"}]}],"next_generation_id":2}}`
	if err := os.WriteFile(filepath.Join(dir, "legacy-date.json"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy session: %v", err)
	}

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	loaded, err := store.Load("legacy-date")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.SessionDate != nil {
		t.Fatalf("SessionDate = %#v for a record without the field, want nil", loaded.SessionDate)
	}
}

func TestForkCopiesSessionDate(t *testing.T) {
	t.Parallel()

	original := Session{
		ID:          "original",
		Title:       "Original",
		SessionDate: &StartDate{Date: "2026-09-13", Zone: "BST", Offset: "+01:00"},
	}
	forked, err := Fork(original)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if forked.SessionDate == nil {
		t.Fatal("forked SessionDate = nil, want the parent's date")
	}
	if got, want := *forked.SessionDate, *original.SessionDate; got != want {
		t.Fatalf("forked SessionDate = %#v, want %#v", got, want)
	}
	if forked.SessionDate == original.SessionDate {
		t.Fatal("fork shares the parent's SessionDate pointer, want a copy")
	}
}

func TestForkOfUndatedRecordLeavesSessionDateNil(t *testing.T) {
	t.Parallel()

	forked, err := Fork(Session{ID: "legacy", Title: "Legacy"})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if forked.SessionDate != nil {
		t.Fatalf("forked SessionDate = %#v, want nil", forked.SessionDate)
	}
}
