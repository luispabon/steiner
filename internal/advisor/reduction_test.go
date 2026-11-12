package advisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// TestHandlerBelowTriggerMatchesLegacyPayload is the golden compatibility
// check for advisor context reduction. With no frozen reduction state and a
// request below the trigger, the provider must receive exactly the payload
// the pre-reduction prompt builder produced, images and file dedupe included.
func TestHandlerBelowTriggerMatchesLegacyPayload(t *testing.T) {
	t.Parallel()

	workDir, policy := newAdvisorTestPolicy(t)
	fileContent := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(fileContent), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	readJSON, err := json.Marshal(builtin.ReadResult{
		Path:       "main.go",
		StartLine:  1,
		EndLine:    3,
		TotalLines: 3,
		FileHash:   builtin.FileContentHash([]byte(fileContent)),
		Output:     fileContent,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	snapshot := []provider.Message{
		{Role: provider.MessageRoleSystem, Content: "project instructions"},
		{Role: provider.MessageRoleUser, Content: "fix the failing test"},
		{
			Role:    provider.MessageRoleAssistant,
			Content: "I will read the file.",
			ToolCalls: []provider.ToolCall{{
				ID:        "call-1",
				Name:      "read",
				Arguments: map[string]any{"path": "main.go"},
			}},
		},
		{Role: provider.MessageRoleTool, Name: "read", ToolCallID: "call-1", Content: string(readJSON)},
		{
			Role:    provider.MessageRoleUser,
			Content: "what should I check next?",
			Images: []provider.ImageBlock{{
				ID:        "img-1",
				MediaType: "image/png",
				Data:      "aW1hZ2U=",
				SizeBytes: 6,
			}},
		},
		{
			Role:    provider.MessageRoleAssistant,
			Content: "Let me consult the advisor.",
			ToolCalls: []provider.ToolCall{{
				ID:        "advisor-1",
				Name:      ToolName,
				Arguments: map[string]any{"question": "is this sound?"},
			}},
		},
	}
	const question = "is this sound?"
	want := []provider.Message{
		{Role: provider.MessageRoleSystem, Content: advisorSystemPrompt},
		{Role: provider.MessageRoleSystem, Content: "project instructions"},
		{Role: provider.MessageRoleUser, Content: "fix the failing test"},
		{Role: provider.MessageRoleAssistant, Content: "I will read the file.\n[tool_call: read {\"path\":\"main.go\"}]"},
		{Role: provider.MessageRoleUser, Content: "[tool_result: read]\n" + string(readJSON)},
		{
			Role:    provider.MessageRoleUser,
			Content: "what should I check next?",
			Images: []provider.ImageBlock{{
				ID:        "img-1",
				MediaType: "image/png",
				Data:      "aW1hZ2U=",
				SizeBytes: 6,
			}},
		},
		{
			Role:    provider.MessageRoleUser,
			Content: inFlightLabel + "Let me consult the advisor.\n[tool_call: advisor {\"question\":\"is this sound?\"}]\n\n" + "File: main.go (already fully present above from an earlier read in this conversation; content unchanged)\n\n" + question + "\n\n" + advisorUserPrompt,
		},
	}

	prov := &fakeProvider{
		response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}},
	}
	handler := NewHandler(HandlerDeps{
		Provider:   prov,
		Model:      provider.ResolvedModel{BackendModelID: "advisor-model"},
		Config:     Config{MaxUsesPerRun: 1, ReductionTriggerTokens: 1_000_000},
		WorkDir:    workDir,
		PathPolicy: policy,
	})
	ctx := agent.WithConversationSnapshot(context.Background(), snapshot)
	if _, err := handler(ctx, map[string]any{"question": question, "files": []any{"main.go"}}); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.requests))
	}
	got := prov.requests[0].Messages
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("provider messages differ from legacy payload:\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}
