package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Anthropic implements the Provider interface for Anthropic Messages APIs.
type Anthropic struct {
	*OpenAICompat
}

// NewAnthropic creates a new Anthropic Messages provider client.
func NewAnthropic(cfg OpenAICompatConfig) (*Anthropic, error) {
	if strings.TrimSpace(cfg.ProviderType) == "" {
		cfg.ProviderType = "anthropic"
	}
	base, err := NewOpenAICompat(cfg)
	if err != nil {
		return nil, err
	}
	return &Anthropic{OpenAICompat: base}, nil
}

// SupportsUsageStats reports whether the provider returns usage metadata.
func (p *Anthropic) SupportsUsageStats() bool {
	return true
}

// ChatCompletion executes a non-streaming chat completion request.
func (p *Anthropic) ChatCompletion(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	if p == nil || p.OpenAICompat == nil {
		return ChatResponse{}, fmt.Errorf("provider is not initialized")
	}
	if err := p.acquire(ctx); err != nil {
		return ChatResponse{}, err
	}
	defer p.release()

	body, err := p.buildRequestPayload(request, false)
	if err != nil {
		return ChatResponse{}, err
	}

	var response ChatResponse
	err = p.withRetry(ctx, func(_ int) (bool, error) {
		req, err := p.buildHTTPRequest(ctx, body, false)
		if err != nil {
			return false, err
		}
		//nolint:bodyclose // response body is closed in this closure on all success/error paths
		resp, err := p.httpClient.Do(req)
		if err != nil {
			if resp != nil {
				closeResponseBody(resp.Body)
			}
			return false, err
		}
		defer closeResponseBody(resp.Body)
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return false, p.readErrorResponse(resp)
		}

		payload, err := p.decodeNonStreamResponse(resp)
		if err != nil {
			return false, err
		}
		response, err = normalizeAnthropicChatResponse(payload)
		if err == nil {
			observePromptTokenUsage(ctx, request, response.Usage)
		}
		return false, err
	}, p.classifyRetryError, nil)
	if err != nil {
		return ChatResponse{}, err
	}
	return response, nil
}

// StreamChatCompletion executes a streaming chat completion request.
func (p *Anthropic) StreamChatCompletion(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error) {
	if p == nil || p.OpenAICompat == nil {
		return nil, fmt.Errorf("provider is not initialized")
	}
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}

	out := make(chan ChatChunk)
	go func() {
		defer close(out)
		defer p.release()

		if err := p.streamChatCompletion(ctx, request, out); err != nil {
			select {
			case out <- ChatChunk{Done: true, Error: err.Error(), OriginalError: err}:
			case <-ctx.Done():
			}
		}
	}()

	return out, nil
}

func (p *Anthropic) streamChatCompletion(ctx context.Context, request ChatRequest, out chan<- ChatChunk) error {
	body, err := p.buildRequestPayload(request, true)
	if err != nil {
		return err
	}

	return p.withRetry(ctx, func(_ int) (bool, error) {
		req, err := p.buildHTTPRequest(ctx, body, true)
		if err != nil {
			return false, err
		}
		//nolint:bodyclose // response body is closed in this closure on all success/error paths
		resp, err := p.httpClient.Do(req)
		if err != nil {
			if resp != nil {
				closeResponseBody(resp.Body)
			}
			return false, err
		}
		defer closeResponseBody(resp.Body)
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return false, p.readErrorResponse(resp)
		}

		partialStream := false
		err = decodeAnthropicStreamWithHandler(ctx, resp.Body, func(chunk ChatChunk) error {
			if chunk.Done && chunk.Error == "" {
				observePromptTokenUsage(ctx, request, chunk.Usage)
			}
			if chunkVisible(chunk) {
				partialStream = true
			}
			select {
			case out <- chunk:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		return partialStream, err
	}, p.classifyRetryError, func(info retryAttemptInfo) {
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
	})
}

func (p *Anthropic) buildRequestPayload(request ChatRequest, stream bool) ([]byte, error) {
	return p.marshalRequest(request, stream)
}

func (p *Anthropic) buildHTTPRequest(ctx context.Context, body []byte, stream bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.messagesURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if strings.TrimSpace(p.apiKey) != "" {
		req.Header.Set("x-api-key", p.apiKey)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	for key, value := range p.headers {
		req.Header.Set(key, value)
	}
	return req, nil
}

func (p *Anthropic) decodeNonStreamResponse(resp *http.Response) (*anthropicResponse, error) {
	var payload anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: %w", errDecodeChatCompletionResponse, err)
	}
	return &payload, nil
}

func (p *Anthropic) messagesURL() string {
	base := *p.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/messages"
	return base.String()
}

func (p *Anthropic) marshalRequest(request ChatRequest, stream bool) ([]byte, error) {
	wire := anthropicRequestWire(request, p.model, stream)
	return json.Marshal(wire)
}
