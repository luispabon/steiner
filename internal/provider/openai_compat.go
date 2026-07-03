package provider

import (
	"context"
	"encoding/json"
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
	BaseURL            string
	APIKey             string
	Headers            map[string]string
	Model              string
	Timeout            time.Duration
	Retry              RetryConfig
	HTTPClient         *http.Client
	Scheduler          *Scheduler
	ProviderType       string
	StreamErrorLog     *StreamErrorLogger
	MinRequestInterval time.Duration
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
	baseURL               *url.URL
	apiKey                string
	headers               map[string]string
	model                 string
	retry                 RetryConfig
	httpClient            *http.Client
	scheduler             *Scheduler
	providerType          string
	streamErrorLog        *StreamErrorLogger
	requestPayloadFunc    func(ChatRequest, bool) ([]byte, error)
	requestFunc           func(context.Context, ChatRequest, []byte, bool) (*http.Request, error)
	nonStreamResponseFunc func(*http.Response) (ChatResponse, error)
	sleep                 func(context.Context, time.Duration) error
	jitter                func(time.Duration) time.Duration
	randMu                sync.Mutex
	rand                  *rand.Rand
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
		// Clone the client AND its transport. A shallow client copy shares the
		// Transport pointer, so the upstream ResponseHeaderTimeout would still
		// fire first on slow servers and surface as
		// `net/http: timeout awaiting response headers` regardless of
		// Client.Timeout. Clear ResponseHeaderTimeout on the cloned transport;
		// Client.Timeout already bounds the entire request, including headers.
		cloned := *client
		cloned.Timeout = cfg.Timeout
		if base, ok := cloned.Transport.(*http.Transport); ok && base != nil {
			t := base.Clone()
			t.ResponseHeaderTimeout = 0
			cloned.Transport = t
		}
		client = &cloned
	}
	provider := &OpenAICompat{
		baseURL:        parsed,
		apiKey:         cfg.APIKey,
		headers:        copyHeaders(cfg.Headers),
		model:          cfg.Model,
		retry:          cfg.Retry,
		httpClient:     client,
		scheduler:      cfg.Scheduler,
		providerType:   cfg.ProviderType,
		streamErrorLog: cfg.StreamErrorLog,
		sleep:          defaultRetrySleep,
		rand:           rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	provider.requestPayloadFunc = provider.buildRequestPayload
	provider.requestFunc = provider.buildHTTPRequest
	provider.nonStreamResponseFunc = provider.normalizeOpenAIChatResponse
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
	return true
}

func (p *OpenAICompat) shouldDisableRemoteStorage() bool {
	if p == nil || p.baseURL == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(p.baseURL.Hostname()))
	return host == "api.openai.com" ||
		strings.HasSuffix(host, ".openai.com") ||
		host == "chatgpt.com" ||
		strings.HasSuffix(host, ".chatgpt.com")
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

	body, err := p.requestPayload(request, false)
	if err != nil {
		return ChatResponse{}, err
	}

	var response ChatResponse
	err = p.withRetry(ctx, func(_ int) (bool, error) {
		resp, err := p.executeHTTPRequest(ctx, request, body, false)
		if err != nil {
			return false, err
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		response, err = p.normalizeNonStreamResponse(resp)
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

	out := make(chan ChatChunk)
	go func() {
		defer close(out)

		if err := p.streamChatCompletionWithHandler(ctx, request, out, decodeChatStreamWithHandler, func(info retryAttemptInfo, streamStart time.Time, chunksReceived, contentBytes int, lastRespHeaders http.Header, body []byte, out chan<- ChatChunk) {
			p.streamErrorLog.Log(streamErrorRecord{
				Timestamp:       time.Now(),
				Event:           "stream_retry",
				Attempt:         info.Attempt,
				Max:             info.MaxAttempts,
				Error:           info.Reason,
				StreamAlive:     time.Since(streamStart).String(),
				ChunksReceived:  chunksReceived,
				ContentBytes:    contentBytes,
				PartialStream:   info.PartialStream,
				RetryDelay:      info.Delay.String(),
				RequestURL:      p.baseURL.String(),
				RequestHeaders:  providerConfigHeaders(p),
				ResponseHeaders: sanitizeHeaders(lastRespHeaders),
				RequestBody:     json.RawMessage(body),
			})
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

func (p *OpenAICompat) requestPayload(request ChatRequest, stream bool) ([]byte, error) {
	if p != nil && p.requestPayloadFunc != nil {
		return p.requestPayloadFunc(request, stream)
	}
	return p.buildRequestPayload(request, stream)
}

func (p *OpenAICompat) requestHTTPRequest(ctx context.Context, request ChatRequest, body []byte, stream bool) (*http.Request, error) {
	if p != nil && p.requestFunc != nil {
		return p.requestFunc(ctx, request, body, stream)
	}
	return p.buildHTTPRequest(ctx, request, body, stream)
}

func (p *OpenAICompat) normalizeNonStreamResponse(resp *http.Response) (ChatResponse, error) {
	if p != nil && p.nonStreamResponseFunc != nil {
		return p.nonStreamResponseFunc(resp)
	}
	return p.normalizeOpenAIChatResponse(resp)
}

func (p *OpenAICompat) normalizeOpenAIChatResponse(resp *http.Response) (ChatResponse, error) {
	payload, err := p.decodeNonStreamResponse(resp)
	if err != nil {
		return ChatResponse{}, err
	}
	return normalizeChatResponse(payload)
}

func (p *OpenAICompat) executeHTTPRequest(ctx context.Context, request ChatRequest, body []byte, stream bool) (*http.Response, error) {
	req, err := p.requestHTTPRequest(ctx, request, body, stream)
	if err != nil {
		return nil, err
	}
	return p.executeHTTP(ctx, req)
}

func (p *OpenAICompat) streamChatCompletionWithHandler(
	ctx context.Context,
	request ChatRequest,
	out chan<- ChatChunk,
	processStream func(context.Context, io.Reader, func(ChatChunk) error) error,
	onRetry func(retryAttemptInfo, time.Time, int, int, http.Header, []byte, chan<- ChatChunk),
) error {
	if p == nil {
		return fmt.Errorf("provider is not initialized")
	}
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()

	body, err := p.requestPayload(request, true)
	if err != nil {
		return err
	}

	var (
		streamStart     time.Time
		chunksReceived  int
		contentBytes    int
		lastRespHeaders http.Header
	)

	err = p.withRetry(ctx, func(_ int) (bool, error) {
		resp, err := p.executeHTTPRequest(ctx, request, body, true)
		if err != nil {
			return false, err
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		streamStart = time.Now()
		chunksReceived = 0
		contentBytes = 0
		lastRespHeaders = resp.Header

		partialStream := false
		err = processStream(ctx, resp.Body, func(chunk ChatChunk) error {
			if chunk.Done {
				observePromptTokenUsage(ctx, request, chunk.Usage)
			}
			if chunkVisible(chunk) {
				partialStream = true
			}
			select {
			case out <- chunk:
				if chunk.Delta.Content != "" {
					contentBytes += len(chunk.Delta.Content)
				}
				chunksReceived++
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		return partialStream, err
	}, p.classifyRetryError, func(info retryAttemptInfo) {
		if onRetry != nil {
			onRetry(info, streamStart, chunksReceived, contentBytes, lastRespHeaders, append([]byte(nil), body...), out)
		}
	})
	if err != nil {
		return err
	}
	return nil
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

func (p *OpenAICompat) classifyRetryError(err error) retryDecision {
	if err == nil {
		return retryDecision{}
	}
	if errors.Is(err, errDecodeChatCompletionResponse) {
		return retryDecision{}
	}
	if errors.Is(err, errDecodeToolCallArguments) {
		return retryDecision{retry: true, reason: err.Error()}
	}
	if errors.Is(err, errDecodeStreamChunkUnexpected) {
		return retryDecision{
			retry:  true,
			reason: err.Error(),
		}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return retryDecision{
			retry:  true,
			reason: err.Error(),
		}
	}
	if httpErr := asHTTPError(err); httpErr != nil {
		return p.classifyHTTPError(httpErr)
	}
	if !isRetryableTransportError(err) {
		return retryDecision{}
	}
	return retryDecision{
		retry:  true,
		reason: err.Error(),
	}
}

// classifyHTTPError maps an HTTPError to a retryDecision, applying
// litellm-specific body parsing when the provider type is litellm.
func (p *OpenAICompat) classifyHTTPError(httpErr *HTTPError) retryDecision {
	if !isRetryableHTTPStatus(httpErr.StatusCode) {
		return retryDecision{}
	}
	// litellm-specific: check for non-retryable budget exhaustion before
	// attempting retry-after parsing. litellm returns 429 for both rate
	// limits and budget exhaustion; only the latter is permanent.
	if httpErr.StatusCode == 429 && p.providerType == "litellm" && isLiteLLMBudgetExceeded(httpErr.Body) {
		return retryDecision{}
	}
	delay, hasHeader := retryAfterDelay(httpErr.Header, p.retry.RetryAfterMax)
	// litellm-specific: when no Retry-After header, parse delay from body.
	// litellm relays upstream rate limits as "Try again in N seconds" text
	// instead of forwarding the Retry-After header.
	if !hasHeader && httpErr.StatusCode == 429 && p.providerType == "litellm" {
		if parsed, ok := parseLiteLLMRetryAfter(httpErr.Body); ok {
			if p.retry.RetryAfterMax > 0 && parsed > p.retry.RetryAfterMax {
				parsed = p.retry.RetryAfterMax
			}
			delay = parsed
		}
	}
	return retryDecision{
		retry:      true,
		reason:     httpErr.Error(),
		retryAfter: delay,
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

// sanitizeHeaders strips the Authorization header and copies the rest.
func sanitizeHeaders(h http.Header) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if strings.EqualFold(k, "authorization") {
			continue
		}
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

// providerConfigHeaders returns the provider's configured headers with the API key stripped.
func providerConfigHeaders(p *OpenAICompat) map[string]string {
	if p == nil {
		return nil
	}
	out := make(map[string]string, len(p.headers))
	for k, v := range p.headers {
		if strings.EqualFold(k, "authorization") {
			continue
		}
		out[k] = v
	}
	return out
}
