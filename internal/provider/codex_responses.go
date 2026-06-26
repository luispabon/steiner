package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CodexResponses implements Provider for Codex OAuth via OpenAI's Responses API.
type CodexResponses struct {
	*OpenAICompat
}

// NewCodexResponses creates a Codex Responses API provider client.
func NewCodexResponses(cfg OpenAICompatConfig) (*CodexResponses, error) {
	base, err := NewOpenAICompat(cfg)
	if err != nil {
		return nil, err
	}
	c := &CodexResponses{OpenAICompat: base}
	base.requestPayloadFunc = c.buildResponsesPayload
	base.requestFunc = c.buildResponsesHTTPRequest
	base.nonStreamResponseFunc = c.normalizeResponsesResponse
	return c, nil
}

// StreamChatCompletion executes a streaming Codex Responses API request.
func (c *CodexResponses) StreamChatCompletion(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error) {
	if c == nil || c.OpenAICompat == nil {
		return nil, fmt.Errorf("provider is not initialized")
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

func (c *CodexResponses) buildResponsesHTTPRequest(ctx context.Context, body []byte, stream bool) (*http.Request, error) {
	return buildJSONPostRequest(ctx, c.responsesURL(), body, stream, c.apiKey, c.headers)
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
	return json.Marshal(wire)
}

func (c *CodexResponses) normalizeResponsesResponse(resp *http.Response) (ChatResponse, error) {
	var payload responsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ChatResponse{}, fmt.Errorf("%w: %w", errDecodeChatCompletionResponse, err)
	}
	return normalizeResponsesResponse(payload)
}
