package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestPostIngestionNeverRewritesCarriedHistory(t *testing.T) {
	carried := []Message{
		{Role: MessageRoleUser, Content: "inspect"},
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call_1", Turn: 1, Content: `{"path":"missing.txt","start_line":1,"end_line":1,"total_lines":1,"output":"historic full result"}`},
	}
	wire, err := json.Marshal(ToProviderMessages(carried))
	if err != nil {
		t.Fatal(err)
	}
	var lossy []provider.Message
	if err := json.Unmarshal(wire, &lossy); err != nil {
		t.Fatal(err)
	}
	reloaded := fromProviderMessages(lossy)
	before, err := json.Marshal(reloaded)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewContextStateManager()
	state := RunState{TurnCount: 2, Conversation: reloaded, Lineage: newConversationLineage(reloaded)}
	first, err := manager.PostIngestion(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(first.Conversation)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("PostIngestion changed carried history:\nbefore %s\nafter  %s", before, after)
	}
	second, err := manager.PostIngestion(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := json.Marshal(second.Conversation)
	if err != nil {
		t.Fatal(err)
	}
	if string(twice) != string(after) {
		t.Fatalf("second PostIngestion changed carried history:\nfirst  %s\nsecond %s", after, twice)
	}
}

func TestFreshReadDedupUsesResultHashNotDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("initial"), 0o644); err != nil {
		t.Fatal(err)
	}
	read := builtin.ReadResult{Path: path, StartLine: 1, EndLine: 1, TotalLines: 1, FileHash: "same-hash", Output: "read content"}
	content, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	prior := []Message{{Role: MessageRoleTool, Name: "read", Turn: 1, Content: string(content)}}
	if err := os.WriteFile(path, []byte("changed on disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, outcome := dedupReadResult(string(content), 2, prior)
	if outcome.Action != "annotated" || outcome.PreviousTurn != 1 {
		t.Fatalf("outcome = %+v, want annotation using prior read hash", outcome)
	}
	var result builtin.ReadResult
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatal(err)
	}
	if result.Output == read.Output {
		t.Fatalf("output = %q, want hash-based annotation", result.Output)
	}
}

func TestLegacyIngestedJSONLoadsWithoutChangingContent(t *testing.T) {
	const legacy = `{"role":"tool","name":"read","turn":1,"content":"historic","ingested":true}`
	var message Message
	if err := json.Unmarshal([]byte(legacy), &message); err != nil {
		t.Fatal(err)
	}
	if message.Content != "historic" {
		t.Fatalf("legacy content = %q, want historic", message.Content)
	}
}
