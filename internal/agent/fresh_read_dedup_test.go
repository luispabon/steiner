package agent

import (
	"encoding/json"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestFreshReadDedupRecordsAndReusesCurrentTurnResult(t *testing.T) {
	manager := NewContextStateManager()
	read := builtin.ReadResult{Path: "note.txt", StartLine: 1, EndLine: 2, TotalLines: 2, FileHash: "hash", Output: "one\\ntwo\\n"}
	content, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	first := shapeFreshToolResultForContextManager(manager, 3, "read", nil, string(content), nil)
	prior := []Message{{Role: MessageRoleTool, Name: "read", Turn: 3, Content: first}}
	second := shapeFreshToolResultForContextManager(manager, 3, "read", nil, string(content), prior)

	var got builtin.ReadResult
	if err := json.Unmarshal([]byte(second), &got); err != nil {
		t.Fatalf("unmarshal second result: %v", err)
	}
	if got.Output != fileUnchangedAnnotationPrefix+" 3: lines 1-2 of 2 in note.txt]" {
		t.Fatalf("second output = %q", got.Output)
	}
	if got.FileHash != read.FileHash {
		t.Fatalf("second file_hash = %q, want %q", got.FileHash, read.FileHash)
	}
}

func TestFreshReadDedupDisabledKeepsFullResult(t *testing.T) {
	manager := NewContextStateManager(config.ContextManagementConfig{ReadAnnotations: false})
	read := builtin.ReadResult{Path: "note.txt", StartLine: 1, EndLine: 2, TotalLines: 2, FileHash: "hash", Output: "one\\ntwo\\n"}
	content, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	prior := []Message{{Role: MessageRoleTool, Name: "read", Turn: 2, Content: string(content)}}
	got := shapeFreshToolResultForContextManager(manager, 3, "read", nil, string(content), prior)
	if got != string(content) {
		t.Fatalf("content changed with annotations disabled: got %q, want %q", got, content)
	}
}
