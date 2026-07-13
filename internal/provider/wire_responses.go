package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// responsesWire speaks the OpenAI Responses format used by the Codex OAuth backend.
type responsesWire struct {
	baseURL *url.URL
	apiKey  string
	headers map[string]string
	model   string
}

func (w *responsesWire) Payload(request ChatRequest, stream bool) ([]byte, error) {
	wire, err := responsesRequestWire(request, w.model, stream)
	if err != nil {
		return nil, err
	}
	wire.PromptCacheKey = request.PromptCacheKey
	if shouldDisableRemoteStorage(w.baseURL) {
		wire.Store = boolValuePtr(false)
	}
	// Do NOT add prompt_cache_retention here. It is valid on OpenAI's Platform
	// API (api.openai.com/v1) but the Codex/ChatGPT OAuth backend rejects it with
	// 400 {"detail":"Unsupported parameter: prompt_cache_retention"} (issue #318).
	// Cache-shard affinity is handled by the headers in HTTPRequest.
	return json.Marshal(wire)
}

func (w *responsesWire) HTTPRequest(ctx context.Context, request ChatRequest, body []byte, stream bool) (*http.Request, error) {
	req, err := buildJSONPostRequest(ctx, w.responsesURL(), body, stream, w.apiKey, w.headers)
	if err != nil {
		return nil, err
	}
	// Codex affinity headers (issue #318). The ChatGPT/Codex backend routes each
	// request to a cache shard; without a stable session hint, a conversation's
	// turns scatter across shards and repeatedly miss the warm prefix cache.
	// Sending session-id/thread-id (the stable per-conversation PromptCacheKey)
	// plus originator=codex_cli_rs pins the conversation to one shard and raised
	// the measured hit rate from ~0.68 to ~0.89 on gpt-5.4-mini. Do NOT remove
	// these or change the originator value: they are the primary win here, and
	// prompt_cache_key in the body alone was not sufficient. Deterministic
	// stickiness needs the WebSocket transport (see #322).
	if key := request.PromptCacheKey; key != "" {
		req.Header.Set("session-id", key)
		req.Header.Set("thread-id", key)
		req.Header.Set("originator", "codex_cli_rs")
	}
	return req, nil
}

func (w *responsesWire) DecodeResponse(resp *http.Response) (ChatResponse, error) {
	var payload responsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ChatResponse{}, fmt.Errorf("%w: %w", errDecodeChatCompletionResponse, err)
	}
	return normalizeResponsesResponse(payload)
}

func (w *responsesWire) DecodeStream(ctx context.Context, body io.Reader, emit func(ChatChunk) error) error {
	return decodeResponsesStreamWithHandler(ctx, body, emit)
}

func (w *responsesWire) responsesURL() string {
	base := *w.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/responses"
	return base.String()
}
