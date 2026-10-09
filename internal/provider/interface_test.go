package provider

import (
	"context"
	"testing"
)

// stubProvider implements Provider without the optional StatefulTranscript
// capability, standing in for existing providers.
type stubProvider struct{}

func (stubProvider) ChatCompletion(context.Context, ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, nil
}

func (stubProvider) StreamChatCompletion(context.Context, ChatRequest) (<-chan ChatChunk, error) {
	return nil, nil
}

func (stubProvider) SupportsUsageStats() bool { return false }

// statefulStubProvider opts in to StatefulTranscript.
type statefulStubProvider struct{ stubProvider }

func (statefulStubProvider) StatefulTranscript() bool { return true }

// optedOutStubProvider implements StatefulTranscript but reports false.
type optedOutStubProvider struct{ stubProvider }

func (optedOutStubProvider) StatefulTranscript() bool { return false }

func TestIsStatefulTranscript(t *testing.T) {
	tests := []struct {
		name string
		p    Provider
		want bool
	}{
		{name: "provider without the capability", p: stubProvider{}, want: false},
		{name: "provider with the capability", p: statefulStubProvider{}, want: true},
		{name: "provider reporting false", p: optedOutStubProvider{}, want: false},
		{name: "nil provider", p: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStatefulTranscript(tt.p); got != tt.want {
				t.Errorf("IsStatefulTranscript() = %v, want %v", got, tt.want)
			}
		})
	}
}
