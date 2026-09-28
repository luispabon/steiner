package delegation

import (
	"context"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
)

func TestFollowUpPreservesHistoryBytesAndSummaryRole(t *testing.T) {
	conversation := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "read files"},
		{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "full", Name: "read"}}},
		{Role: agent.MessageRoleTool, ToolCallID: "full", Content: "full read output"},
		{Role: agent.MessageRoleAssistant, Content: "stub summary", ToolCalls: []agent.ToolCall{{ID: "stub", Name: "read"}}},
		{Role: agent.MessageRoleTool, ToolCallID: "stub", Content: "[read output omitted]"},
		{Role: agent.MessageRoleSummary, Content: "summary role survives", Source: "compaction", Retention: &agent.MessageRetention{Kind: "summary", AgentID: "child"}},
		{Role: agent.MessageRoleAssistant, Content: "answer", Source: "assistant-source", Retention: &agent.MessageRetention{Kind: "keep", AgentID: "child"}},
	}
	images := []provider.ImageBlock{{ID: "image", FilePath: "/tmp/image.png", MediaType: "image/png", Data: "payload", Width: 5, Height: 7}}
	request := buildContinuationRequest(agent.RunRequest{}, conversation, "follow up", images, 2, Limits{MaxTurns: 3})
	want := append(agent.ReplaySafeConversation(conversation), agent.Message{
		Role: agent.MessageRoleUser, Content: "follow up",
		Images: []agent.ImageBlock{{ID: "image", FilePath: "/tmp/image.png", MediaType: "image/png", Data: "payload", Width: 5, Height: 7}},
	})
	if request.Prompt.Conversation != nil {
		t.Fatalf("Prompt.Conversation = %#v, want nil", request.Prompt.Conversation)
	}
	if !reflect.DeepEqual(request.SourceConversation, want) {
		t.Fatalf("SourceConversation mismatch:\ngot  %#v\nwant %#v", request.SourceConversation, want)
	}
	manager := agent.NewContextStateManager()
	state, err := manager.PostIngestion(context.Background(), agent.RunState{Conversation: request.SourceConversation})
	if err != nil {
		t.Fatalf("PostIngestion error: %v", err)
	}
	if !reflect.DeepEqual(state.Conversation, want) {
		t.Fatalf("PostIngestion changed native history:\ngot  %#v\nwant %#v", state.Conversation, want)
	}
}
