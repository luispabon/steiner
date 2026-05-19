package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var defaultHTTPClient = &http.Client{}

// OpenAICompatConfig configures an OpenAI-compatible provider client.
type OpenAICompatConfig struct {
	BaseURL    string
	APIKey     string
	Headers    map[string]string
	Model      string
	Timeout    time.Duration
	Retry      RetryConfig
	HTTPClient *http.Client
	Scheduler  *Scheduler
}

// RetryConfig controls retry behavior for transient provider failures.
type RetryConfig struct {
	Enabled        bool
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	RetryAfterMax  time.Duration
}

// OpenAICompat implements the Provider interface for OpenAI-compatible APIs.
type OpenAICompat struct {
	baseURL    *url.URL
	apiKey     string
	headers    map[string]string
	model      string
	retry      RetryConfig
	httpClient *http.Client
	scheduler  *Scheduler
	sleep      func(context.Context, time.Duration) error
	jitter     func(time.Duration) time.Duration
	randMu     sync.Mutex
	rand       *rand.Rand
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
	if cfg.Timeout > 0 {
		cloned := *client
		cloned.Timeout = cfg.Timeout
		if cloned.Transport != nil {
			if transport, ok := cloned.Transport.(*http.Transport); ok {
				transportClone := transport.Clone()
				transportClone.ResponseHeaderTimeout = cfg.Timeout
				cloned.Transport = transportClone
			}
		} else {
			cloned.Transport = &http.Transport{
				ResponseHeaderTimeout: cfg.Timeout,
			}
		}
		client = &cloned
	}
	provider := &OpenAICompat{
		baseURL:    parsed,
		apiKey:     cfg.APIKey,
		headers:    copyHeaders(cfg.Headers),
		model:      cfg.Model,
		retry:      cfg.Retry,
		httpClient: client,
		scheduler:  cfg.Scheduler,
		sleep:      defaultRetrySleep,
		rand:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	provider.jitter = provider.fullJitter
	return provider, nil
}

func copyHeaders(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// SupportsUsageStats reports whether the provider returns usage metadata.
func (p *OpenAICompat) SupportsUsageStats() bool {
	return p != nil
}

// ChatCompletion executes a non-streaming chat completion request.
func (p *OpenAICompat) ChatCompletion(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	if p == nil {
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
		resp, err := p.buildAndExecuteHTTPRequest(ctx, body, false)
		if err != nil {
			return false, err
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		payload, err := p.decodeNonStreamResponse(resp)
		if err != nil {
			return false, err
		}
		response, err = normalizeChatResponse(payload)
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

func (p *OpenAICompat) streamChatCompletion(ctx context.Context, request ChatRequest, out chan<- ChatChunk) error {
	body, err := p.buildRequestPayload(request, true)
	if err != nil {
		return err
	}

	return p.withRetry(ctx, func(_ int) (bool, error) {
		resp, err := p.buildAndExecuteHTTPRequest(ctx, body, true)
		if err != nil {
			return false, err
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		partialStream := false
		err = decodeChatStreamWithHandler(ctx, resp.Body, func(chunk ChatChunk) error {
			if chunk.Done {
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

func (p *OpenAICompat) buildAndExecuteHTTPRequest(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
	req, err := p.buildHTTPRequest(ctx, body, stream)
	if err != nil {
		return nil, err
	}
	return p.executeHTTP(ctx, req)
}

func (p *OpenAICompat) classifyRetryError(err error) retryDecision {
	if err == nil {
		return retryDecision{}
	}
	if strings.HasPrefix(err.Error(), "decode chat completion response:") {
		return retryDecision{}
	}
	if strings.HasPrefix(err.Error(), "decode tool call ") {
		return retryDecision{}
	}
	errText := err.Error()
	if errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(errText, "unexpected EOF") || strings.Contains(errText, "stream completed without a final chunk") {
		return retryDecision{
			retry:  true,
			reason: errText,
		}
	}
	if strings.HasPrefix(errText, "decode stream chunk:") && strings.Contains(errText, "unexpected end of JSON input") {
		return retryDecision{
			retry:  true,
			reason: errText,
		}
	}
	if httpErr := asHTTPError(err); httpErr != nil {
		if !isRetryableHTTPStatus(httpErr.StatusCode) {
			return retryDecision{}
		}
		delay, _ := retryAfterDelay(httpErr.Header, p.retry.RetryAfterMax)
		return retryDecision{
			retry:      true,
			reason:     httpErr.Error(),
			retryAfter: delay,
		}
	}
	if !isRetryableTransportError(err) {
		return retryDecision{}
	}
	return retryDecision{
		retry:  true,
		reason: err.Error(),
	}
}

func chunkVisible(chunk ChatChunk) bool {
	if chunk.Delta.Content != "" {
		return true
	}
	if chunk.Thinking != "" {
		return true
	}
	return len(chunk.Delta.ToolCalls) > 0
}

func retryWarningMessage(info retryAttemptInfo) string {
	message := fmt.Sprintf("retrying attempt %d/%d", info.Attempt, info.MaxAttempts)
	if info.Delay > 0 {
		message = fmt.Sprintf("%s in %s", message, info.Delay)
	}
	if info.Reason != "" {
		message = fmt.Sprintf("%s: %s", message, info.Reason)
	}
	return message
}

func defaultRetrySleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *OpenAICompat) fullJitter(cap time.Duration) time.Duration {
	if cap <= 0 {
		return 0
	}
	if p == nil || p.rand == nil {
		return cap
	}
	max := int64(cap)
	if max <= 0 {
		return 0
	}
	p.randMu.Lock()
	defer p.randMu.Unlock()
	return time.Duration(p.rand.Int63n(max + 1))
}
