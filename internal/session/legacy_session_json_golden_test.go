package session

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

// The committed golden was captured from origin/main with this flag, before
// agent.Message.Segment existed. Rerun it only to accept a deliberate saved
// session shape change, and capture the new bytes from origin/main.
var updateLegacySessionGolden = flag.Bool("update-legacy-session-golden", false, "rewrite the legacy saved-session golden")

// legacyConversation is the plain user/summary/assistant conversation a
// session saved before summary segments existed holds.
func legacyConversation() []agent.Message {
	return []agent.Message{
		{Role: agent.MessageRoleUser, Content: "old question"},
		{Role: agent.MessageRoleSummary, Content: "old summary"},
		{Role: agent.MessageRoleAssistant, Content: "old answer"},
	}
}

// legacyGoldenSession is a saved session with a plain summary message and no
// segment metadata: the shape every pre-segmented-compaction session file has.
func legacyGoldenSession() Session {
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return Session{
		ID:            "legacy-golden-session",
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt.Add(time.Hour),
		Title:         "Legacy session",
		Model:         "claude-3-5-sonnet-20241022",
		ModelRef:      "claude/sonnet",
		ModelProvider: "anthropic",
		Mode:          "build",
		Skills:        []string{"review"},
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{{
				ID:       1,
				Messages: legacyConversation(),
			}},
			NextGenerationID: 2,
		},
	}
}

// TestLegacySessionSaveMatchesGolden pins that the store's real save path writes
// a session without segment metadata byte-identically to origin/main.
func TestLegacySessionSaveMatchesGolden(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	session := legacyGoldenSession()
	if err := store.Save(session); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(store.dir, session.ID+sessionExt))
	if err != nil {
		t.Fatalf("read saved session: %v", err)
	}

	path := filepath.Join("testdata", "legacy_session_saved.json")
	if *updateLegacySessionGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("saved legacy session drifted from %s\ngot:  %s\nwant: %s\nregenerate with:\n  go test ./internal/session -run TestLegacySessionSaveMatchesGolden -update-legacy-session-golden", path, got, want)
	}
	if bytes.Contains(got, []byte(`"segment"`)) {
		t.Fatalf("saved legacy session holds a segment key: %s", got)
	}
}
