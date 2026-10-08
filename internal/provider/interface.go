package provider

import "context"

// Provider executes chat-completion requests against a model backend.
type Provider interface {
	ChatCompletion(ctx context.Context, request ChatRequest) (ChatResponse, error)
	StreamChatCompletion(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error)
	SupportsUsageStats() bool
}

// StatefulTranscript is implemented by providers whose backend owns the live conversation
// transcript, so callers must not rewrite already-sent history (for example by compacting).
type StatefulTranscript interface {
	StatefulTranscript() bool
}

// IsStatefulTranscript reports whether p owns its conversation transcript.
func IsStatefulTranscript(p Provider) bool {
	s, ok := p.(StatefulTranscript)
	return ok && s.StatefulTranscript()
}
