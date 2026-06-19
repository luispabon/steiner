package advisor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

type fakeProvider struct {
	requests []provider.ChatRequest
	response provider.ChatResponse
	err      error
}

func (p *fakeProvider) ChatCompletion(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if p.err != nil {
		return provider.ChatResponse{}, p.err
	}
	return p.response, nil
}

func (p *fakeProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, errors.New("unexpected streaming call")
}

func (p *fakeProvider) SupportsUsageStats() bool { return true }

func TestAdviseUsesConversationSnapshotUnmodified(t *testing.T) {
	snapshot := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "fix the failing test"},
		{
			Role: provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{{
				ID:   "call-1",
				Name: "read",
				Arguments: map[string]any{
					"path": "internal/config/config.go",
				},
			}},
		},
		{
			Role:       provider.MessageRoleTool,
			Name:       "read",
			ToolCallID: "call-1",
			Content:    "package config",
		},
	}
	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "Check config validation before touching agent flow."},
		},
	}

	resp, err := advise(context.Background(), prov, "advisor-model", snapshot, intPtr(256))
	if err != nil {
		t.Fatalf("advise() error = %v", err)
	}
	if got, want := resp.Message.Content, "Check config validation before touching agent flow."; got != want {
		t.Fatalf("response content = %q, want %q", got, want)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(prov.requests))
	}

	req := prov.requests[0]
	if got, want := req.Model, "advisor-model"; got != want {
		t.Fatalf("request.Model = %q, want %q", got, want)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 256 {
		t.Fatalf("request.MaxTokens = %#v, want 256", req.MaxTokens)
	}
	if len(req.Tools) != 0 {
		t.Fatalf("request.Tools = %#v, want nil/empty", req.Tools)
	}
	if got, want := len(req.Messages), len(snapshot)+2; got != want {
		t.Fatalf("len(request.Messages) = %d, want %d", got, want)
	}
	if req.Messages[0].Role != provider.MessageRoleSystem || !strings.Contains(req.Messages[0].Content, "internal advisor") {
		t.Fatalf("request.Messages[0] = %#v, want advisor system prompt", req.Messages[0])
	}

	// snapshot[0]: user message passes through unchanged
	if got := req.Messages[1]; got.Role != provider.MessageRoleUser || got.Content != "fix the failing test" {
		t.Fatalf("request.Messages[1] = %#v, want user pass-through", got)
	}

	// snapshot[1]: assistant tool calls flattened to text, ToolCalls cleared
	assistantMsg := req.Messages[2]
	if assistantMsg.Role != provider.MessageRoleAssistant {
		t.Fatalf("request.Messages[2].Role = %q, want assistant", assistantMsg.Role)
	}
	if len(assistantMsg.ToolCalls) != 0 {
		t.Fatalf("request.Messages[2].ToolCalls not cleared: %#v", assistantMsg.ToolCalls)
	}
	if !strings.Contains(assistantMsg.Content, "[tool_call: read") {
		t.Fatalf("request.Messages[2].Content missing tool_call line: %q", assistantMsg.Content)
	}

	// snapshot[2]: tool result flattened to user message, ToolCallID and Name cleared
	toolResultMsg := req.Messages[3]
	if toolResultMsg.Role != provider.MessageRoleUser {
		t.Fatalf("request.Messages[3].Role = %q, want user", toolResultMsg.Role)
	}
	if !strings.HasPrefix(toolResultMsg.Content, "[tool_result: read]") {
		t.Fatalf("request.Messages[3].Content missing prefix: %q", toolResultMsg.Content)
	}
	if !strings.Contains(toolResultMsg.Content, "package config") {
		t.Fatalf("request.Messages[3].Content missing original content: %q", toolResultMsg.Content)
	}
	if toolResultMsg.ToolCallID != "" {
		t.Fatalf("request.Messages[3].ToolCallID not cleared: %q", toolResultMsg.ToolCallID)
	}
	if toolResultMsg.Name != "" {
		t.Fatalf("request.Messages[3].Name not cleared: %q", toolResultMsg.Name)
	}

	last := req.Messages[len(req.Messages)-1]
	if last.Role != provider.MessageRoleUser || !strings.Contains(last.Content, "advisory note") {
		t.Fatalf("last advisor message = %#v, want advisor user prompt", last)
	}

	// snapshot must not be mutated by flattening
	req.Messages[1].Content = "mutated"
	if snapshot[0].Content != "fix the failing test" {
		t.Fatalf("snapshot[0] mutated to %q, want original", snapshot[0].Content)
	}
	if len(snapshot[1].ToolCalls) != 1 {
		t.Fatalf("snapshot[1].ToolCalls mutated, want original tool calls preserved")
	}
	if snapshot[2].ToolCallID != "call-1" {
		t.Fatalf("snapshot[2].ToolCallID mutated to %q, want original", snapshot[2].ToolCallID)
	}
}

func TestAdviseWrapsProviderErrors(t *testing.T) {
	prov := &fakeProvider{err: errors.New("backend failed")}

	_, err := advise(context.Background(), prov, "advisor-model", nil, nil)
	if err == nil {
		t.Fatal("advise() error = nil, want wrapped error")
	}
	if got := err.Error(); !strings.Contains(got, "advisor: backend failed") {
		t.Fatalf("advise() error = %q, want wrapped provider error", got)
	}
}

func intPtr(v int) *int { return &v }
