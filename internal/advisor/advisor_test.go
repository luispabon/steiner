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
	}
	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "Check config validation before touching agent flow."},
		},
	}

	resp, err := Advise(context.Background(), Request{
		Provider:     prov,
		Model:        "advisor-model",
		Conversation: snapshot,
		MaxTokens:    intPtr(256),
	})
	if err != nil {
		t.Fatalf("Advise() error = %v", err)
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
	for i := range snapshot {
		got := req.Messages[i+1]
		want := snapshot[i]
		if got.Role != want.Role || got.Content != want.Content || len(got.ToolCalls) != len(want.ToolCalls) {
			t.Fatalf("request.Messages[%d] = %#v, want snapshot %#v", i+1, got, want)
		}
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != provider.MessageRoleUser || !strings.Contains(last.Content, "advisory note") {
		t.Fatalf("last advisor message = %#v, want advisor user prompt", last)
	}

	req.Messages[1].Content = "mutated"
	if snapshot[0].Content != "fix the failing test" {
		t.Fatalf("snapshot mutated to %q, want original", snapshot[0].Content)
	}
}

func TestAdviseWrapsProviderErrors(t *testing.T) {
	prov := &fakeProvider{err: errors.New("backend failed")}

	_, err := Advise(context.Background(), Request{
		Provider: prov,
		Model:    "advisor-model",
	})
	if err == nil {
		t.Fatal("Advise() error = nil, want wrapped error")
	}
	if got := err.Error(); !strings.Contains(got, "advisor: backend failed") {
		t.Fatalf("Advise() error = %q, want wrapped provider error", got)
	}
}

func intPtr(v int) *int { return &v }
