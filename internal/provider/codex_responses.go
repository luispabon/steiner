package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// CodexResponses implements Provider for Codex OAuth via OpenAI's Responses API.
type CodexResponses struct {
	*OpenAICompat
	minInterval time.Duration
	mu          sync.Mutex
	lastRequest time.Time
}

// NewCodexResponses creates a Codex Responses API provider client.
func NewCodexResponses(cfg OpenAICompatConfig) (*CodexResponses, error) {
	base, err := NewOpenAICompat(cfg)
	if err != nil {
		return nil, err
	}
	c := &CodexResponses{
		OpenAICompat: base,
		minInterval:  cfg.MinRequestInterval,
	}
	base.requestPayloadFunc = c.buildResponsesPayload
	base.requestFunc = c.buildResponsesHTTPRequest
	base.nonStreamResponseFunc = c.normalizeResponsesResponse
	return c, nil
}

// ChatCompletion executes a non-streaming Codex Responses API request with rate limiting.
func (c *CodexResponses) ChatCompletion(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	if c == nil || c.OpenAICompat == nil {
		return ChatResponse{}, fmt.Errorf("provider is not initialized")
	}
	if err := c.rateLimit(ctx); err != nil {
		return ChatResponse{}, err
	}
	return c.OpenAICompat.ChatCompletion(ctx, request)
}

// StreamChatCompletion executes a streaming Codex Responses API request with rate limiting.
func (c *CodexResponses) StreamChatCompletion(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error) {
	if c == nil || c.OpenAICompat == nil {
		return nil, fmt.Errorf("provider is not initialized")
	}

	if err := c.rateLimit(ctx); err != nil {
		ch := make(chan ChatChunk)
		close(ch)
		return ch, err
	}

	out := make(chan ChatChunk)
	go func() {
		defer close(out)
		if err := c.streamChatCompletionWithHandler(ctx, request, out, decodeResponsesStreamWithHandler, func(info retryAttemptInfo, _ time.Time, _, _ int, _ http.Header, _ []byte, out chan<- ChatChunk) {
			if !info.PartialStream {
				return
			}
			select {
			case out <- ChatChunk{
				RetryReset: true,
				Diagnostic: retryWarningMessage(info),
				Severity:   "warning",
			}:
			case <-ctx.Done():
			}
		}); err != nil {
			select {
			case out <- ChatChunk{Done: true, Error: err.Error(), OriginalError: err}:
			case <-ctx.Done():
			}
		}
	}()
	return out, nil
}

// rateLimit enforces the minimum interval between consecutive Codex requests.
// If the elapsed time since the last request is less than minInterval,
// it sleeps for the remaining duration.
func (c *CodexResponses) rateLimit(ctx context.Context) error {
	if c.minInterval <= 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	elapsed := time.Since(c.lastRequest)
	if elapsed < c.minInterval {
		delay := c.minInterval - elapsed
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	c.lastRequest = time.Now()
	return nil
}

func (c *CodexResponses) buildResponsesHTTPRequest(ctx context.Context, request ChatRequest, body []byte, stream bool) (*http.Request, error) {
	req, err := buildJSONPostRequest(ctx, c.responsesURL(), body, stream, c.apiKey, c.headers)
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

func (c *CodexResponses) responsesURL() string {
	base := *c.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/responses"
	return base.String()
}

func (c *CodexResponses) buildResponsesPayload(request ChatRequest, stream bool) ([]byte, error) {
	wire, err := responsesRequestWire(request, c.model, stream)
	if err != nil {
		return nil, err
	}
	wire.PromptCacheKey = request.PromptCacheKey
	if c.shouldDisableRemoteStorage() {
		wire.Store = boolValuePtr(false)
	}
	// Do NOT add prompt_cache_retention here. It is valid on OpenAI's Platform
	// API (api.openai.com/v1) but the Codex/ChatGPT OAuth backend rejects it with
	// 400 {"detail":"Unsupported parameter: prompt_cache_retention"} (issue #318).
	// Cache-shard affinity is handled by the headers in buildResponsesHTTPRequest.
	return json.Marshal(wire)
}

func (c *CodexResponses) normalizeResponsesResponse(resp *http.Response) (ChatResponse, error) {
	var payload responsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ChatResponse{}, fmt.Errorf("%w: %w", errDecodeChatCompletionResponse, err)
	}
	return normalizeResponsesResponse(payload)
}
