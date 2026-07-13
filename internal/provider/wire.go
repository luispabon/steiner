package provider

import (
	"context"
	"io"
	"net/http"
)

// Wire is the Provider Wire seam: everything format-specific about one backend.
// A Wire owns the request payload shape, HTTP request construction (URL, auth
// headers, provider-specific headers), non-stream response decoding, and stream
// decoding. It owns no retry, scheduling, pacing, or transport policy; those
// belong to Client, the shared Provider Request Execution engine.
type Wire interface {
	// Payload marshals request into the backend's request format.
	Payload(request ChatRequest, stream bool) ([]byte, error)
	// HTTPRequest builds the HTTP request that carries body to the backend.
	//
	// request is required even though most wires only need body: the Codex
	// Responses wire derives its cache-shard affinity headers (session-id,
	// thread-id, originator) from request.PromptCacheKey. Removing this parameter
	// silently drops the Codex prompt-cache hit rate from ~0.89 to ~0.68 (issue
	// #318). See responsesWire.HTTPRequest and TestClientCodexCacheHintsReachTheBackend.
	HTTPRequest(ctx context.Context, request ChatRequest, body []byte, stream bool) (*http.Request, error)
	// DecodeResponse decodes a non-streaming backend response.
	DecodeResponse(resp *http.Response) (ChatResponse, error)
	// DecodeStream decodes a streaming backend response, calling emit once per chunk.
	DecodeStream(ctx context.Context, body io.Reader, emit func(ChatChunk) error) error
}

// retryRefiner is optionally implemented by a Wire that can interpret errors the
// engine cannot. The engine classifies first and offers the decision for
// refinement; the wire never sees the retry loop itself.
type retryRefiner interface {
	RefineRetry(err error, decision retryDecision) retryDecision
}
