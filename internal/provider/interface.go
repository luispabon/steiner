package provider

import "context"

type Provider interface {
	ChatCompletion(ctx context.Context, request ChatRequest) (ChatResponse, error)
	StreamChatCompletion(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error)
	SupportsUsageStats() bool
}
