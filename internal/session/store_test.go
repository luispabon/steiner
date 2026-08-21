package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

func TestNewStore(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	if store == nil {
		t.Fatal("expected store, got nil")
	}

	stat, err := os.Stat(tmpDir)
	if err != nil || !stat.IsDir() {
		t.Fatalf("store directory not created: %v", err)
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	lineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{
				ID: 1,
				Messages: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "hello"},
				},
			},
		},
		NextGenerationID: 2,
	}

	original := Session{
		ID:        "test-session-1",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Title:     "Test Session",
		Model:     "test-model",
		Group:     "run-123",
		Lineage:   lineage,
	}

	if err := store.Save(original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load(original.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.ID != original.ID {
		t.Errorf("ID mismatch: got %q, want %q", loaded.ID, original.ID)
	}
	if loaded.Title != original.Title {
		t.Errorf("Title mismatch: got %q, want %q", loaded.Title, original.Title)
	}
	if loaded.Model != original.Model {
		t.Errorf("Model mismatch: got %q, want %q", loaded.Model, original.Model)
	}
	if loaded.Group != original.Group {
		t.Errorf("Group mismatch: got %q, want %q", loaded.Group, original.Group)
	}

	if len(loaded.Lineage.Generations) != len(original.Lineage.Generations) {
		t.Errorf("Lineage generations mismatch: got %d, want %d",
			len(loaded.Lineage.Generations), len(original.Lineage.Generations))
	}

	if loaded.Lineage.NextGenerationID != original.Lineage.NextGenerationID {
		t.Errorf("NextGenerationID mismatch: got %d, want %d",
			loaded.Lineage.NextGenerationID, original.Lineage.NextGenerationID)
	}

	gen := loaded.Lineage.Generations[0]
	if len(gen.Messages) != 1 || gen.Messages[0].Content != "hello" {
		t.Errorf("Message content mismatch: got %v, want [hello]", gen.Messages)
	}
}

func TestSaveAndLoadPreservesPromptCacheKey(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	original := Session{
		ID:             "test-session-cache-key",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		Title:          "Test Session",
		Model:          "test-model",
		PromptCacheKey: "explicit-cache-key",
	}

	if err := store.Save(original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load(original.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got, want := loaded.PromptCacheKey, "explicit-cache-key"; got != want {
		t.Errorf("PromptCacheKey mismatch: got %q, want %q", got, want)
	}
	if got, want := loaded.CacheKey(), "explicit-cache-key"; got != want {
		t.Errorf("CacheKey() mismatch: got %q, want %q", got, want)
	}
}

func TestLoadFallsBackToIDForRecordWrittenWithoutPromptCacheKey(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Simulate a session file written before the prompt_cache_key field
	// existed: no such key present in the JSON at all.
	legacy := map[string]any{
		"id":         "legacy-session",
		"created_at": time.Now().UTC(),
		"updated_at": time.Now().UTC(),
		"title":      "Legacy Session",
		"model":      "test-model",
		"lineage":    agent.ConversationLineage{},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "legacy-session.json"), data, 0o644); err != nil {
		t.Fatalf("write legacy session file: %v", err)
	}

	loaded, err := store.Load("legacy-session")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.PromptCacheKey != "" {
		t.Errorf("PromptCacheKey = %q, want empty for a record with no stored key", loaded.PromptCacheKey)
	}
	if got, want := loaded.CacheKey(), "legacy-session"; got != want {
		t.Errorf("CacheKey() = %q, want fallback to session ID %q", got, want)
	}
}

func TestSaveAndLoadPreservesToolCallTranscript(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	now := time.Now().UTC()
	original := Session{
		ID:        "tool-call-session",
		CreatedAt: now,
		UpdatedAt: now,
		Title:     "Tool Call Session",
		Model:     "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID: 1,
					Messages: []agent.Message{
						{Role: agent.MessageRoleUser, Content: "inspect the file"},
						{
							Role: agent.MessageRoleAssistant,
							ToolCalls: []agent.ToolCall{
								{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "README.md"}},
							},
						},
						{Role: agent.MessageRoleTool, Content: "file contents", ToolCallID: "call_1", Name: "read"},
						{Role: agent.MessageRoleAssistant, Content: "done"},
					},
				},
			},
			NextGenerationID: 2,
		},
	}

	if err := store.Save(original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load(original.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	got := loaded.Lineage.FullMessages()
	if len(got) != 4 {
		t.Fatalf("loaded conversation length = %d, want 4", len(got))
	}
	if got[1].Role != agent.MessageRoleAssistant {
		t.Fatalf("loaded assistant role = %q, want assistant", got[1].Role)
	}
	if len(got[1].ToolCalls) != 1 {
		t.Fatalf("loaded assistant tool calls = %#v, want 1 tool call", got[1].ToolCalls)
	}
	if got[1].ToolCalls[0].ID != "call_1" || got[1].ToolCalls[0].Name != "read" {
		t.Fatalf("loaded assistant tool call = %#v, want call_1/read", got[1].ToolCalls[0])
	}
	if got[1].ToolCalls[0].Arguments["path"] != "README.md" {
		t.Fatalf("loaded assistant tool call args = %#v, want path README.md", got[1].ToolCalls[0].Arguments)
	}
	if got[2].Role != agent.MessageRoleTool || got[2].ToolCallID != "call_1" {
		t.Fatalf("loaded tool result = %#v, want tool call id call_1", got[2])
	}
}

func TestLoadLegacyConversationFallbackPreservesToolAndDelegationMessages(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	now := time.Now().UTC()
	payload := struct {
		ID           string          `json:"id"`
		CreatedAt    time.Time       `json:"created_at"`
		UpdatedAt    time.Time       `json:"updated_at"`
		Title        string          `json:"title"`
		Model        string          `json:"model"`
		Conversation []agent.Message `json:"conversation"`
	}{
		ID:        "legacy-session",
		CreatedAt: now,
		UpdatedAt: now,
		Title:     "Legacy Session",
		Model:     "test-model",
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "delegate and inspect"},
			{
				Role: agent.MessageRoleAssistant,
				ToolCalls: []agent.ToolCall{
					{ID: "call_1", Name: "delegate", Arguments: map[string]any{"task": "inspect README"}},
				},
			},
			{
				Role:       agent.MessageRoleTool,
				Name:       "delegate",
				ToolCallID: "call_1",
				Content:    "visible delegate output",
				Retention: &agent.MessageRetention{
					Kind:       "delegate_summary",
					Summary:    "retained delegate summary",
					AgentID:    "child-1",
					Status:     "complete",
					TurnCount:  3,
					TokenCount: 512,
				},
			},
			{Role: agent.MessageRoleAssistant, Content: "done"},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal legacy payload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, payload.ID+sessionExt), data, 0o644); err != nil {
		t.Fatalf("write legacy session: %v", err)
	}

	loaded, err := store.Load(payload.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Group != "" {
		t.Fatalf("loaded legacy group = %q, want empty", loaded.Group)
	}

	got := loaded.Lineage.FullMessages()
	if got, want := len(got), 4; got != want {
		t.Fatalf("loaded conversation length = %d, want %d", got, want)
	}
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].Name != "delegate" {
		t.Fatalf("loaded assistant tool call = %#v, want preserved delegate call", got[1].ToolCalls)
	}
	if got[2].ToolCallID != "call_1" || got[2].Name != "delegate" {
		t.Fatalf("loaded tool result = %#v, want preserved delegate result", got[2])
	}
	if got[2].Retention == nil {
		t.Fatal("loaded tool retention = nil, want preserved delegate retention")
	}
	if got, want := got[2].Retention.Summary, "retained delegate summary"; got != want {
		t.Fatalf("loaded retention summary = %q, want %q", got, want)
	}
	if got, want := loaded.Lineage.NextGenerationID, 2; got != want {
		t.Fatalf("loaded next generation id = %d, want %d", got, want)
	}
}

func TestLineageMultiGeneration(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	lineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{
				ID: 1,
				SummaryPrefix: []agent.Message{
					{Role: agent.MessageRoleSummary, Content: "prefix"},
				},
				Messages: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "msg1"},
				},
			},
			{
				ID: 2,
				SummaryPrefix: []agent.Message{
					{Role: agent.MessageRoleSummary, Content: "prefix2"},
				},
				Messages: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "msg2"},
				},
			},
		},
		NextGenerationID: 3,
	}

	session := Session{
		ID:        "multi-gen-session",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Title:     "Multi Generation",
		Model:     "test-model",
		Lineage:   lineage,
	}

	if err := store.Save(session); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded.Lineage.Generations) != 2 {
		t.Errorf("expected 2 generations, got %d", len(loaded.Lineage.Generations))
	}

	if loaded.Lineage.Generations[0].ID != 1 || loaded.Lineage.Generations[1].ID != 2 {
		t.Errorf("generation IDs mismatch")
	}

	if len(loaded.Lineage.Generations[1].SummaryPrefix) != 1 {
		t.Errorf("expected 1 summary prefix message, got %d",
			len(loaded.Lineage.Generations[1].SummaryPrefix))
	}
}

func TestList(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	sessions := []Session{
		{
			ID:        "session-1",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC().Add(-2 * time.Second),
			Title:     "First",
			Model:     "model-1",
			Group:     "run-a",
			Lineage:   agent.ConversationLineage{},
		},
		{
			ID:        "session-2",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC().Add(-1 * time.Second),
			Title:     "Second",
			Model:     "model-2",
			Group:     "run-b",
			Lineage:   agent.ConversationLineage{},
		},
		{
			ID:        "session-3",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
			Title:     "Third",
			Model:     "model-3",
			Group:     "run-c",
			Lineage:   agent.ConversationLineage{},
		},
	}

	for _, s := range sessions {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	entries, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}

	if entries[0].ID != "session-3" {
		t.Errorf("newest should be first, got %q", entries[0].ID)
	}
	if entries[0].Group != "run-c" {
		t.Errorf("newest group = %q, want %q", entries[0].Group, "run-c")
	}
	if entries[1].ID != "session-2" {
		t.Errorf("second newest should be second, got %q", entries[1].ID)
	}
	if entries[1].Group != "run-b" {
		t.Errorf("second newest group = %q, want %q", entries[1].Group, "run-b")
	}
	if entries[2].ID != "session-1" {
		t.Errorf("oldest should be last, got %q", entries[2].ID)
	}
	if entries[2].Group != "run-a" {
		t.Errorf("oldest group = %q, want %q", entries[2].Group, "run-a")
	}
}

func TestEvictionAt26Sessions(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	now := time.Now().UTC()
	for i := 0; i < 26; i++ {
		session := Session{
			ID:        fmt.Sprintf("session-%d", i),
			CreatedAt: now,
			UpdatedAt: now.Add(time.Duration(i) * time.Second),
			Title:     "Session",
			Model:     "test",
			Group:     "run-evict",
			Lineage:   agent.ConversationLineage{},
		}
		if err := store.Save(session); err != nil {
			t.Fatalf("Save failed at iteration %d: %v", i, err)
		}
	}

	entries, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(entries) != maxSessions {
		t.Errorf("expected %d sessions after eviction, got %d", maxSessions, len(entries))
	}

	oldest := entries[len(entries)-1]
	if oldest.ID == "session-0" {
		t.Error("oldest session (session-0) was not evicted")
	}
}

func TestDelete(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	session := Session{
		ID:        "delete-test",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Title:     "To Delete",
		Model:     "test",
		Group:     "run-delete",
		Lineage:   agent.ConversationLineage{},
	}

	if err := store.Save(session); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	entries, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if err := store.Delete(session.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	entries, err = store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after delete, got %d", len(entries))
	}

	sessionPath := filepath.Join(tmpDir, session.ID+sessionExt)
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Errorf("session file still exists: %v", err)
	}
}

func TestTitleFromPrompt(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "short prompt",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "long prompt",
			input:    "this is a very long prompt that exceeds eighty characters and should be truncated at the limit",
			expected: "this is a very long prompt that exceeds eighty characters and should be truncate",
		},
		{
			name:     "whitespace collapse",
			input:    "hello   world   how   are   you",
			expected: "hello world how are you",
		},
		{
			name:     "newlines and tabs",
			input:    "hello\n\n\tworld\r\n test",
			expected: "hello world test",
		},
		{
			name:     "exactly 80 chars",
			input:    "123456789012345678901234567890123456789012345678901234567890123456789012345678901",
			expected: "12345678901234567890123456789012345678901234567890123456789012345678901234567890",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TitleFromPrompt(tt.input)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestConcurrentSaves(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	done := make(chan error, 5)
	now := time.Now().UTC()

	for i := 0; i < 5; i++ {
		go func(idx int) {
			session := Session{
				ID:        fmt.Sprintf("concurrent-%d", idx),
				CreatedAt: now,
				UpdatedAt: now.Add(time.Duration(idx) * time.Second),
				Title:     "Concurrent",
				Model:     "test",
				Group:     "run-concurrent",
				Lineage:   agent.ConversationLineage{},
			}
			done <- store.Save(session)
		}(i)
	}

	for i := 0; i < 5; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent save %d failed: %v", i, err)
		}
	}

	entries, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(entries) != 5 {
		t.Errorf("expected 5 entries after concurrent saves, got %d", len(entries))
	}

	indexPath := filepath.Join(tmpDir, indexFile)
	if _, err := os.Stat(indexPath); err != nil {
		t.Errorf("index file not found: %v", err)
	}
}
