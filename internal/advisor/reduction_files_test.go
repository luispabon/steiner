package advisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// TestHandlerRequestedFileReappearsWhenReadReduced proves that attachments are
// re-deduped only against untouched full reads after a plan is frozen. The read
// that seeded the original dedupe is reduced, so the file content must return.
func TestHandlerRequestedFileReappearsWhenReadReduced(t *testing.T) {
	workDir, policy := newAdvisorTestPolicy(t)
	fileContent := strings.Repeat("package main\n", 300)
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(fileContent), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	readJSON, err := json.Marshal(builtin.ReadResult{
		Path:       "main.go",
		StartLine:  1,
		EndLine:    300,
		TotalLines: 300,
		FileHash:   builtin.FileContentHash([]byte(fileContent)),
		Output:     fileContent,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	snapshot := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "review the file and the command output"},
		assistantCall("r1", "read"),
		{Role: provider.MessageRoleTool, Name: "read", ToolCallID: "r1", Content: string(readJSON)},
		assistantCall("b1", "bash"),
		bigBashMessage("b1"),
	}
	deps := HandlerDeps{WorkDir: workDir, PathPolicy: policy, Model: provider.ResolvedModel{BackendModelID: "advisor-model"}}
	legacyFiles := dedupeAgainstSnapshot([]advisorFile{{DisplayPath: "main.go", Content: fileContent, TotalBytes: len(fileContent)}}, snapshot, workDir)
	if !legacyFiles[0].Deduped {
		t.Fatal("test setup: requested file should start deduped")
	}
	initial := estimateAdvisorMessages(context.Background(), deps, buildMessages(snapshot, "", legacyFiles))

	prov := &fakeProvider{response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}}}
	handler := NewHandler(HandlerDeps{
		Provider: prov, Model: deps.Model, WorkDir: workDir, PathPolicy: policy,
		Config: Config{MaxUsesPerRun: 1, ContextReductionThresholdTokens: initial.tokens},
	})
	ctx := agent.WithConversationSnapshot(context.Background(), snapshot)
	if _, err := handler(ctx, map[string]any{"files": []any{"main.go"}}); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.requests))
	}
	last := prov.requests[0].Messages[len(prov.requests[0].Messages)-1]
	if strings.Contains(last.Content, "already fully present") {
		t.Fatalf("file stayed deduped against a reduced read: %q", last.Content)
	}
	if !strings.Contains(last.Content, "File: main.go") || !strings.Contains(last.Content, strings.Repeat("package main\n", 10)) {
		t.Fatalf("requested file content missing after read reduction: %q", last.Content)
	}
}
