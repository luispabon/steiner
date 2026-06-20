package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
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
	base.requestPayloadFunc = func(request ChatRequest, stream bool) ([]byte, error) {
		wire := anthropicRequestWire(request, base.model, stream)
		return json.Marshal(wire)
	}
	anthropic := &Anthropic{OpenAICompat: base}
	base.requestFunc = anthropic.buildHTTPRequest
	base.nonStreamResponseFunc = func(resp *http.Response) (ChatResponse, error) {
		var payload anthropicResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return ChatResponse{}, fmt.Errorf("%w: %w", errDecodeChatCompletionResponse, err)
		}
		return normalizeAnthropicChatResponse(&payload)
	}
	return anthropic, nil
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
	return p.OpenAICompat.ChatCompletion(ctx, request)
}

// StreamChatCompletion executes a streaming chat completion request.
func (p *Anthropic) StreamChatCompletion(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error) {
	if p == nil || p.OpenAICompat == nil {
		return nil, fmt.Errorf("provider is not initialized")
	}

	out := make(chan ChatChunk)
	go func() {
		defer close(out)

		if err := p.streamChatCompletionWithHandler(ctx, request, out, func(ctx context.Context, body io.Reader, emit func(ChatChunk) error) error {
			return decodeAnthropicStreamWithHandler(ctx, body, func(chunk ChatChunk) error {
				if chunk.Done && chunk.Error == "" {
					observePromptTokenUsage(ctx, request, chunk.Usage)
				}
				return emit(chunk)
			})
		}, func(info retryAttemptInfo, _ time.Time, _ int, _ int, _ http.Header, _ []byte, out chan<- ChatChunk) {
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

func (p *Anthropic) messagesURL() string {
	base := *p.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/messages"
	return base.String()
}
