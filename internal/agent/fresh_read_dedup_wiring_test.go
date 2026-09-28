package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestFreshReadDedupIngestionAndAnnotationEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(builtin.ReadResult{Path: path, FileHash: "abc123", StartLine: 1, EndLine: 1, TotalLines: 1, Output: "one\n"})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewContextStateManager()
	var events []output.Event
	manager.SetEventSink(output.SinkFunc(func(event output.Event) { events = append(events, event) }))
	prior := []Message{{Role: MessageRoleTool, Name: "read", Turn: 1, Content: string(payload)}}
	got := shapeFreshToolResultForContextManager(manager, 2, "read", nil, string(payload), prior)
	var result builtin.ReadResult
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Output, fileUnchangedAnnotationPrefix) || result.FileHash != "abc123" {
		t.Fatalf("dedup result = %+v", result)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want one annotation", len(events))
	}
	annotation, ok := output.AsContextFileAnnotationEvent(events[0].Payload)
	if !ok || annotation.Action != "annotated" || annotation.Turn != 2 {
		t.Fatalf("annotation event = %#v", events[0].Payload)
	}

	first, outcome := dedupReadResult(string(payload), 1, nil)
	if outcome.Reason != "first read" || first != string(payload) {
		t.Fatalf("first read outcome = %+v content=%s", outcome, first)
	}
	withoutAnnotations := NewContextStateManager(config.ContextManagementConfig{ReadAnnotations: false})
	full := shapeFreshToolResultForContextManager(withoutAnnotations, 2, "read", nil, string(payload), prior)
	if full != string(payload) {
		t.Fatalf("annotations-disabled content = %s", full)
	}
}
