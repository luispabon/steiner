package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

func TestSaveStripsImagePayloadWithoutMutatingCaller(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	lineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{
				ID: 1,
				SummaryPrefix: []agent.Message{{
					Role: agent.MessageRoleUser, Content: "summary",
					Images: []agent.ImageBlock{{ID: "img-prefix", FilePath: "/tmp/prefix.png", MediaType: "image/png", Data: "prefix-bytes"}},
				}},
				Messages: []agent.Message{{
					Role: agent.MessageRoleTool, Name: "read", Content: "read result",
					Images: []agent.ImageBlock{{ID: "img-1", FilePath: "/tmp/a.png", MediaType: "image/png", Data: "image-bytes"}},
				}},
			},
		},
		NextGenerationID: 2,
	}
	original := Session{
		ID:        "img-session",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Title:     "Images",
		Model:     "test-model",
		Lineage:   lineage,
	}

	if err := store.Save(original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// The caller's lineage must be left untouched.
	if got := original.Lineage.Generations[0].Messages[0].Images[0].Data; got != "image-bytes" {
		t.Fatalf("caller message image data = %q, want image-bytes", got)
	}
	if got := original.Lineage.Generations[0].SummaryPrefix[0].Images[0].Data; got != "prefix-bytes" {
		t.Fatalf("caller prefix image data = %q, want prefix-bytes", got)
	}

	// The persisted file must contain no image bytes.
	raw, err := os.ReadFile(filepath.Join(dir, "img-session.json"))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	if strings.Contains(string(raw), "image-bytes") || strings.Contains(string(raw), "prefix-bytes") {
		t.Fatalf("persisted session contains image bytes: %s", raw)
	}

	loaded, err := store.Load(original.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	messages := loaded.Lineage.Generations[0].Messages
	if len(messages) != 1 || len(messages[0].Images) != 0 {
		t.Fatalf("persisted message retained images: %#v", messages)
	}
	if !strings.Contains(messages[0].Content, "[image img-1:") {
		t.Fatalf("persisted message missing placeholder: %q", messages[0].Content)
	}
	if !strings.Contains(loaded.Lineage.Generations[0].SummaryPrefix[0].Content, "[image img-prefix:") {
		t.Fatalf("persisted prefix missing placeholder: %q", loaded.Lineage.Generations[0].SummaryPrefix[0].Content)
	}
}
