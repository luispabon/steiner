package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// TestSaveAndLoadPreservesToolMessageArtifactMetadata checks that a saved bash
// tool message keeps its artifact JSON byte-for-byte after a save and load.
func TestSaveAndLoadPreservesToolMessageArtifactMetadata(t *testing.T) {
	result := builtin.BashResult{
		FilePath:         ".steiner/tmp/bash-output/0123456789abcdef0123456789abcdef.log",
		ArtifactStatus:   "partial",
		ArtifactLimitHit: true,
		Message:          "artifact size cap reached; saved output is partial",
		Output:           "1\n2\n3\n",
	}
	content, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal bash result: %v", err)
	}
	toolMsg := agent.Message{Role: agent.MessageRoleTool, ToolCallID: "call-1", Name: "bash", Content: string(content)}

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	sess := Session{
		ID:        "bash-artifact-session",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Title:     "Bash artifact",
		Model:     "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{{
				ID:       1,
				Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "run it"}, toolMsg},
			}},
			NextGenerationID: 2,
		},
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded.Lineage.Generations) != 1 || len(loaded.Lineage.Generations[0].Messages) != 2 {
		t.Fatalf("loaded lineage shape = %+v, want one generation with two messages", loaded.Lineage.Generations)
	}
	got := loaded.Lineage.Generations[0].Messages[1]
	if got.Content != string(content) {
		t.Errorf("tool message content changed on round trip:\n got: %s\nwant: %s", got.Content, content)
	}
	for _, want := range []string{`"artifact_limit_hit":true`, `"artifact_status":"partial"`, `"file_path":"` + result.FilePath + `"`} {
		if !strings.Contains(got.Content, want) {
			t.Errorf("loaded tool message lacks %s: %s", want, got.Content)
		}
	}
}
