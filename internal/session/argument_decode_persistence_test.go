package session

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

// TestSaveLoadPreservesArgumentDecodeError round trips a call marked with
// ArgumentDecodeError through the real store. The store is reopened before
// loading so the assertions read what was written to disk.
func TestSaveLoadPreservesArgumentDecodeError(t *testing.T) {
	const decodeErr = `decode "read" arguments: unexpected end of JSON input`
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	original := Session{
		ID:        "decode-error-session",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Title:     "Decode error",
		Model:     "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{{
				ID: 1,
				Messages: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "read it"},
					{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{
						ID:                  "call_bad",
						Name:                "read",
						Arguments:           map[string]any{},
						RawArguments:        "{}",
						ArgumentDecodeError: decodeErr,
					}}},
				},
			}},
			NextGenerationID: 2,
		},
	}
	if err := store.Save(original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	reopened, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen NewStore failed: %v", err)
	}
	loaded, err := reopened.Load(original.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	messages := loaded.Lineage.Generations[0].Messages
	if len(messages) != 2 || len(messages[1].ToolCalls) != 1 {
		t.Fatalf("loaded messages = %+v, want assistant with one tool call", messages)
	}
	call := messages[1].ToolCalls[0]
	if len(call.Arguments) != 0 {
		t.Fatalf("loaded Arguments = %#v, want empty", call.Arguments)
	}
	if call.RawArguments != "{}" {
		t.Fatalf("loaded RawArguments = %q, want {}", call.RawArguments)
	}
	if call.ArgumentDecodeError != decodeErr {
		t.Fatalf("loaded ArgumentDecodeError = %q, want %q", call.ArgumentDecodeError, decodeErr)
	}
}
