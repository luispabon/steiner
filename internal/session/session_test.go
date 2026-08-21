package session

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

func TestFork(t *testing.T) {
	tests := []struct {
		name          string
		originalTitle string
		expectedTitle string
	}{
		{
			name:          "basic fork",
			originalTitle: "My Conversation",
			expectedTitle: "Fork of: My Conversation",
		},
		{
			name:          "fork with long title truncation",
			originalTitle: "This is a very long conversation title that should be truncated to eighty characters in total",
			expectedTitle: "Fork of: This is a very long conversation title that should be truncated to eigh",
		},
		{
			name:          "fork with empty title",
			originalTitle: "",
			expectedTitle: "Fork of:",
		},
		{
			name:          "fork with whitespace collapse",
			originalTitle: "Title  with   multiple    spaces",
			expectedTitle: "Fork of: Title with multiple spaces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalLineage := agent.ConversationLineage{
				Generations: []agent.ConversationGeneration{
					{
						ID:            1,
						SummaryPrefix: nil,
						Messages:      []agent.Message{{Role: "user", Content: "Hello"}},
					},
				},
				NextGenerationID: 2,
			}

			original := Session{
				ID:        "original-id-123",
				CreatedAt: time.Now().UTC().Add(-time.Hour),
				UpdatedAt: time.Now().UTC().Add(-time.Hour),
				Title:     tt.originalTitle,
				Model:     "gpt-4",
				Group:     "run-123",
				Lineage:   originalLineage,
			}

			forked, err := Fork(original)
			if err != nil {
				t.Fatalf("Fork failed: %v", err)
			}

			// Check ID is different
			if forked.ID == original.ID {
				t.Errorf("forked ID should be different from original, got same ID: %s", forked.ID)
			}

			// Check ID is valid hex
			if len(forked.ID) != 32 {
				t.Errorf("forked ID should be 32 hex chars, got %d: %s", len(forked.ID), forked.ID)
			}

			// Check model is same
			if forked.Model != original.Model {
				t.Errorf("forked model should match original, got %s want %s", forked.Model, original.Model)
			}
			if forked.Group != original.Group {
				t.Errorf("forked group should match original, got %q want %q", forked.Group, original.Group)
			}

			// Check title follows fork pattern
			if forked.Title != tt.expectedTitle {
				t.Errorf("forked title mismatch, got %q want %q", forked.Title, tt.expectedTitle)
			}

			// Check timestamps are fresh
			if forked.CreatedAt.Before(original.UpdatedAt) {
				t.Errorf("forked CreatedAt should be after original UpdatedAt")
			}
			if forked.UpdatedAt.Before(original.UpdatedAt) {
				t.Errorf("forked UpdatedAt should be after original UpdatedAt")
			}

			// Check lineage is cloned (not same reference)
			if &forked.Lineage == &original.Lineage {
				t.Errorf("forked lineage should be a different object from original")
			}

			// Check lineage content is equivalent
			if len(forked.Lineage.Generations) != len(original.Lineage.Generations) {
				t.Errorf("forked lineage should have same number of generations, got %d want %d",
					len(forked.Lineage.Generations), len(original.Lineage.Generations))
			}

			// Verify lineage independence: mutate original's lineage
			original.Lineage.NextGenerationID = 999
			if forked.Lineage.NextGenerationID == 999 {
				t.Errorf("forked lineage should not be affected by original mutation")
			}
		})
	}
}

func TestForkLineageIndependence(t *testing.T) {
	// Create original session with a generation containing messages
	originalLineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{
				ID:            1,
				SummaryPrefix: nil,
				Messages: []agent.Message{
					{Role: "user", Content: "First message"},
					{Role: "assistant", Content: "First response"},
				},
			},
			{
				ID:            2,
				SummaryPrefix: []agent.Message{{Role: "system", Content: "Summary"}},
				Messages: []agent.Message{
					{Role: "user", Content: "Second message"},
				},
			},
		},
		NextGenerationID: 3,
	}

	original := Session{
		ID:        "original",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Title:     "Original",
		Model:     "gpt-4",
		Lineage:   originalLineage,
	}

	forked, err := Fork(original)
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	// Verify structure matches
	if len(forked.Lineage.Generations) != 2 {
		t.Errorf("forked lineage should have 2 generations, got %d", len(forked.Lineage.Generations))
	}

	// Mutate original's generation at index 0
	original.Lineage.Generations[0].Messages[0].Content = "MUTATED"

	// Verify fork is unaffected
	if forked.Lineage.Generations[0].Messages[0].Content != "First message" {
		t.Errorf("forked lineage was affected by original mutation, got %q",
			forked.Lineage.Generations[0].Messages[0].Content)
	}
}

func TestForkTitleTruncation(t *testing.T) {
	// Create a session with title that, when prefixed, exceeds 80 chars
	original := Session{
		ID:        "id",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Title:     "12345678901234567890123456789012345678901234567890", // 50 chars
		Model:     "gpt-4",
		Lineage:   agent.ConversationLineage{NextGenerationID: 1},
	}

	forked, err := Fork(original)
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	// "Fork of: " is 9 chars + 50 = 59, should fit
	// Verify it doesn't exceed 80 chars
	if len(forked.Title) > 80 {
		t.Errorf("forked title exceeds 80 chars: %d chars", len(forked.Title))
	}

	// Now test with a very long original title
	veryLong := Session{
		ID:        "id",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Title:     "This is an extremely long title that definitely exceeds eighty characters when combined with fork prefix",
		Model:     "gpt-4",
		Lineage:   agent.ConversationLineage{NextGenerationID: 1},
	}

	forkedLong, err := Fork(veryLong)
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	if len(forkedLong.Title) > 80 {
		t.Errorf("forked title exceeds 80 chars: %d chars, title: %q", len(forkedLong.Title), forkedLong.Title)
	}
}

func TestForkDoesNotModifyOriginal(t *testing.T) {
	originalLineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{
				ID:       1,
				Messages: []agent.Message{{Role: "user", Content: "test"}},
			},
		},
		NextGenerationID: 2,
	}

	original := Session{
		ID:        "original",
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour),
		UpdatedAt: time.Now().UTC().Add(-2 * time.Hour),
		Title:     "Original Title",
		Model:     "gpt-4",
		Lineage:   originalLineage,
	}

	originalID := original.ID
	originalTitle := original.Title
	originalModel := original.Model
	originalCreatedAt := original.CreatedAt
	originalUpdateAt := original.UpdatedAt

	// Fork the session
	_, err := Fork(original)
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	// Verify original was not modified
	if original.ID != originalID {
		t.Errorf("original ID was modified")
	}
	if original.Title != originalTitle {
		t.Errorf("original Title was modified")
	}
	if original.Model != originalModel {
		t.Errorf("original Model was modified")
	}
	if original.CreatedAt != originalCreatedAt {
		t.Errorf("original CreatedAt was modified")
	}
	if original.UpdatedAt != originalUpdateAt {
		t.Errorf("original UpdatedAt was modified")
	}
}

func TestNewSessionWithGroup(t *testing.T) {
	t.Parallel()

	sess, err := NewSession("gpt-4", agent.ConversationLineage{}, "run-abc")
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if got, want := sess.Group, "run-abc"; got != want {
		t.Fatalf("session group = %q, want %q", got, want)
	}
}

func TestNewSessionDoesNotMintPromptCacheKey(t *testing.T) {
	t.Parallel()

	sess, err := NewSession("gpt-4", agent.ConversationLineage{})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if sess.PromptCacheKey != "" {
		t.Fatalf("PromptCacheKey = %q, want empty (NewSession must not mint a key)", sess.PromptCacheKey)
	}
}

func TestCacheKeyFallsBackToID(t *testing.T) {
	t.Parallel()

	sess := Session{ID: "session-id-only"}
	if got, want := sess.CacheKey(), "session-id-only"; got != want {
		t.Fatalf("CacheKey() = %q, want %q", got, want)
	}
}

func TestCacheKeyPrefersStoredValue(t *testing.T) {
	t.Parallel()

	sess := Session{ID: "session-id", PromptCacheKey: "stored-key"}
	if got, want := sess.CacheKey(), "stored-key"; got != want {
		t.Fatalf("CacheKey() = %q, want %q", got, want)
	}
}

func TestForkPropagatesStoredPromptCacheKey(t *testing.T) {
	t.Parallel()

	original := Session{ID: "parent-id", PromptCacheKey: "parent-cache-key"}
	forked, err := Fork(original)
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}
	if got, want := forked.PromptCacheKey, "parent-cache-key"; got != want {
		t.Fatalf("forked PromptCacheKey = %q, want %q (inherited from parent)", got, want)
	}
	if forked.ID == original.ID {
		t.Fatalf("forked ID should differ from original")
	}
}

func TestForkOfKeylessRecordMaterializesKeyOnFirstFork(t *testing.T) {
	t.Parallel()

	// A pre-change record has no stored PromptCacheKey.
	original := Session{ID: "legacy-id"}

	firstFork, err := Fork(original)
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}
	if got, want := firstFork.PromptCacheKey, "legacy-id"; got != want {
		t.Fatalf("first fork PromptCacheKey = %q, want %q (healed to parent ID)", got, want)
	}

	secondFork, err := Fork(firstFork)
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}
	if got, want := secondFork.PromptCacheKey, "legacy-id"; got != want {
		t.Fatalf("fork-of-fork PromptCacheKey = %q, want %q (propagated, not empty)", got, want)
	}
}
