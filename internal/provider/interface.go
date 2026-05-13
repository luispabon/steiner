package provider

import "context"

// Provider executes chat-completion requests against a model backend.
type Provider interface {
	ChatCompletion(ctx context.Context, request ChatRequest) (ChatResponse, error)
	StreamChatCompletion(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error)
	SupportsUsageStats() bool
}
