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
	path := filepath.Join(t.TempDir(), "note.txt")
	fileContent := "line one\nline two\nline three\n"
	if err := os.WriteFile(path, []byte(fileContent), 0o644); err != nil {
		t.Fatal(err)
	}
	read := builtin.ReadResult{Path: path, StartLine: 1, EndLine: 3, TotalLines: 3, FileHash: "hash-1", Output: fileContent}
	readJSON, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}

	carried := []Message{
		{Role: MessageRoleUser, Content: "inspect"},
		{Role: MessageRoleAssistant, Content: "reading the file",
			ToolCalls: []ToolCall{{ID: "call_1", Name: "read"}}},
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call_1", Turn: 1, Content: string(readJSON)},
		{Role: MessageRoleAssistant, Content: "reading it again",
			ToolCalls: []ToolCall{{ID: "call_2", Name: "read"}}},
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call_2", Turn: 2, Content: string(readJSON)},
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
	before := contentsOf(reloaded)

	manager := NewContextStateManager()
	state := RunState{TurnCount: 2, Conversation: reloaded, Lineage: newConversationLineage(reloaded)}
	first, err := manager.PostIngestion(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	after := contentsOf(first.Conversation)
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("PostIngestion changed carried history at message %d:\nbefore %q\nafter  %q", i, before[i], after[i])
		}
	}

	second, err := manager.PostIngestion(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	twice := contentsOf(second.Conversation)
	for i := range after {
		if twice[i] != after[i] {
			t.Fatalf("second PostIngestion changed carried history at message %d:\nfirst  %q\nsecond %q", i, after[i], twice[i])
		}
	}
}

func contentsOf(messages []Message) []string {
	out := make([]string, len(messages))
	for i, message := range messages {
		out[i] = message.Content
	}
	return out
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
	got, outcome := dedupReadResult(string(content), prior)
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
