package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

type fakeProvider struct {
	requests  []provider.ChatRequest
	responses []provider.ChatResponse
	chatFn    func(context.Context, provider.ChatRequest) (provider.ChatResponse, error)
	streamFn  func(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error)
}

func (p *fakeProvider) ChatCompletion(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if p.chatFn != nil {
		return p.chatFn(ctx, req)
	}
	if len(p.responses) == 0 {
		return provider.ChatResponse{}, errors.New("no response configured")
	}
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

func (p *fakeProvider) StreamChatCompletion(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	if p.streamFn != nil {
		p.requests = append(p.requests, req)
		return p.streamFn(ctx, req)
	}
	return nil, errors.New("stream not used")
}

func (p *fakeProvider) SupportsUsageStats() bool { return true }

type fakeExecutor struct {
	calls []struct {
		tool string
		args map[string]any
	}
	execute func(context.Context, string, map[string]any) (any, error)
}

func (e *fakeExecutor) Execute(ctx context.Context, toolName string, input map[string]any) (any, error) {
	e.calls = append(e.calls, struct {
		tool string
		args map[string]any
	}{tool: toolName, args: cloneInput(input)})
	return e.execute(ctx, toolName, input)
}

type cancelContextKey struct{}

func rolesOf(messages []provider.Message) []string {
	roles := make([]string, 0, len(messages))
	for _, message := range messages {
		roles = append(roles, string(message.Role))
	}
	return roles
}

func eventTypes(events []output.Event) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func messageContentsContain(messages []provider.Message, needle string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

func toolMessages(messages []provider.Message) []provider.Message {
	filtered := make([]provider.Message, 0, len(messages))
	for _, message := range messages {
		if message.Role == provider.MessageRoleTool {
			filtered = append(filtered, message)
		}
	}
	return filtered
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSequence(values, sequence []string) bool {
	if len(sequence) == 0 || len(values) < len(sequence) {
		return false
	}
	for start := 0; start <= len(values)-len(sequence); start++ {
		match := true
		for i := range sequence {
			if values[start+i] != sequence[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func intPtr(v int) *int { return &v }
