package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

var defaultHTTPClient = &http.Client{}

type OpenAICompatConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
	Scheduler  *Scheduler
}

type OpenAICompat struct {
	baseURL    *url.URL
	apiKey     string
	model      string
	httpClient *http.Client
	scheduler  *Scheduler
}

// NewOpenAICompat creates a new OpenAI-compatible provider client.
func NewOpenAICompat(cfg OpenAICompatConfig) (*OpenAICompat, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if cfg.Scheduler == nil {
		return nil, fmt.Errorf("scheduler is required")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = defaultHTTPClient
	}
	return &OpenAICompat{
		baseURL:    parsed,
		apiKey:     cfg.APIKey,
		model:      cfg.Model,
		httpClient: client,
		scheduler:  cfg.Scheduler,
	}, nil
}

func (p *OpenAICompat) SupportsUsageStats() bool {
	return p != nil
}

func (p *OpenAICompat) ChatCompletion(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	if p == nil {
		return ChatResponse{}, fmt.Errorf("provider is not initialized")
	}
	if err := p.acquire(ctx); err != nil {
		return ChatResponse{}, err
	}
	defer p.release()

	payload, err := p.doChatCompletion(ctx, request, false)
	if err != nil {
		return ChatResponse{}, err
	}
	return normalizeChatResponse(payload)
}

func (p *OpenAICompat) StreamChatCompletion(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error) {
	if p == nil {
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

func (p *OpenAICompat) acquire(ctx context.Context) error {
	if p.scheduler == nil {
		return nil
	}
	return p.scheduler.Acquire(ctx)
}

func (p *OpenAICompat) release() {
	if p.scheduler == nil {
		return
	}
	p.scheduler.Release()
}

func (p *OpenAICompat) doChatCompletion(ctx context.Context, request ChatRequest, stream bool) (*openAIResponse, error) {
	resp, err := p.executeRequest(ctx, requestExecutionInput{request: request, stream: stream})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return p.decodeNonStreamResponse(resp)
}

func (p *OpenAICompat) streamChatCompletion(ctx context.Context, request ChatRequest, out chan<- ChatChunk) error {
	resp, err := p.executeRequest(ctx, requestExecutionInput{request: request, stream: true})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeChatStream(ctx, resp.Body, out)
}
