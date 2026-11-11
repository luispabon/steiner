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

// ConnectionReleaser is implemented by providers that keep a transport
// connection alive between requests. ReleaseConnection closes that connection
// without retiring the provider: the next request dials again. It never
// interrupts a request in flight, which ends on its own and closes then. A
// provider owned by a connection cache does not implement it, because the cache
// decides the connection's lifetime.
type ConnectionReleaser interface {
	ReleaseConnection()
}

// ReleaseConnection releases p's idle transport connection when the provider
// supports it, and does nothing for providers that do not.
func ReleaseConnection(p Provider) {
	if p == nil {
		return
	}
	if releaser, ok := p.(ConnectionReleaser); ok {
		releaser.ReleaseConnection()
	}
}
