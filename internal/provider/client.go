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

// Client is the Provider Request Execution engine: one shared request flow for
// every backend. It owns scheduling, pacing, retry, HTTP execution and stream
// bookkeeping, and delegates every format-specific concern to its Wire.
type Client struct {
	baseURL        *url.URL
	apiKey         string
	headers        map[string]string
	model          string
	retry          RetryConfig
	httpClient     *http.Client
	providerType   string
	streamErrorLog *StreamErrorLogger
	wire           Wire

	minInterval time.Duration
	mu          sync.Mutex
	lastRequest time.Time

	sleep  func(context.Context, time.Duration) error
	jitter func(time.Duration) time.Duration
	randMu sync.Mutex
	rand   *rand.Rand
}

// SupportsUsageStats reports whether the provider returns usage metadata.
func (c *Client) SupportsUsageStats() bool {
	return true
}

// ChatCompletion executes a non-streaming chat completion request.
func (c *Client) ChatCompletion(ctx context.Context, request ChatRequest) (response ChatResponse, err error) {
	if c == nil {
		return ChatResponse{}, fmt.Errorf("provider is not initialized")
	}

	call := providerCallInput{
		log:        c.streamErrorLog,
		provider:   c.providerType,
		model:      c.model,
		transport:  "http",
		start:      time.Now(),
		ctx:        ctx,
		requestURL: c.baseURLString(),
	}
	defer func() {
		call.err = err
		emitProviderCall(call)
	}()

	if err = c.pace(ctx); err != nil {
		return ChatResponse{}, err
	}
	var body []byte
	body, err = c.wire.Payload(request, false)
	if err != nil {
		return ChatResponse{}, err
	}
	call.requestBody = body
	call.requestHeaders = diagnosticRequestHeaders(c)

	var (
		attempts        int
		ttft            time.Duration
		responseHeaders http.Header
	)
	dropCacheAffinity := false
	err = c.withRetry(ctx, func(attempt int) (bool, error) {
		attempts = attempt
		attemptRequest := request
		if dropCacheAffinity {
			attemptRequest.PromptCacheKey = ""
		}
		resp, err := c.executeRequest(ctx, attemptRequest, body, false)
		if err != nil {
			return false, err
		}
		ttft = time.Since(call.start)
		responseHeaders = resp.Header
		defer func() {
			_ = resp.Body.Close()
		}()

		response, err = c.wire.DecodeResponse(resp)
		if err == nil {
			observePromptTokenUsage(ctx, request, response.Usage)
		}
		return false, err
	}, c.classifyRetryErrorAndDropCacheAffinity(&dropCacheAffinity), nil)

	call.ttft = ttft
	call.attempts = attempts
	call.responseHeaders = responseHeaders
	if err != nil {
		return ChatResponse{}, err
	}
	return response, nil
}

// StreamChatCompletion executes a streaming chat completion request.
func (c *Client) StreamChatCompletion(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error) {
	if c == nil {
		return nil, fmt.Errorf("provider is not initialized")
	}

	call := providerCallInput{
		log:        c.streamErrorLog,
		provider:   c.providerType,
		model:      c.model,
		transport:  "sse",
		start:      time.Now(),
		ctx:        ctx,
		requestURL: c.baseURLString(),
	}
	if err := c.pace(ctx); err != nil {
		call.err = err
		emitProviderCall(call)
		out := make(chan ChatChunk)
		close(out)
		return out, err
	}

	out := make(chan ChatChunk)
	go func() {
		var err error
		defer func() {
			call.err = err
			emitProviderCall(call)
			close(out)
		}()

		err = c.streamWithRetry(ctx, request, out, &call)
		if err != nil {
			select {
			case out <- ChatChunk{Done: true, Error: err.Error(), OriginalError: err}:
			case <-ctx.Done():
			}
		}
	}()

	return out, nil
}

// streamWithRetry runs the retry loop for a streaming request, forwarding chunks
// to out and tracking how much of the stream each attempt already delivered.
func (c *Client) streamWithRetry(ctx context.Context, request ChatRequest, out chan<- ChatChunk, call *providerCallInput) error {
	body, err := c.wire.Payload(request, true)
	if err != nil {
		return err
	}
	call.requestBody = body
	call.requestHeaders = diagnosticRequestHeaders(c)

	var (
		attempts          int
		chunksReceived    int
		partialStream     bool
		firstChunkAt      time.Time
		lastRespHeaders   http.Header
		dropCacheAffinity bool
	)

	err = c.withRetry(ctx, func(attempt int) (bool, error) {
		attempts = attempt
		attemptRequest := request
		if dropCacheAffinity {
			attemptRequest.PromptCacheKey = ""
		}
		resp, err := c.executeRequest(ctx, attemptRequest, body, true)
		if err != nil {
			return false, err
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		chunksReceived = 0
		partialStream = false
		firstChunkAt = time.Time{}
		lastRespHeaders = resp.Header

		err = c.wire.DecodeStream(ctx, resp.Body, func(chunk ChatChunk) error {
			if chunksReceived == 0 {
				firstChunkAt = time.Now()
			}
			if chunk.Done {
				observePromptTokenUsage(ctx, request, chunk.Usage)
			}
			if chunkVisible(chunk) {
				partialStream = true
			}
			select {
			case out <- chunk:
				chunksReceived++
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		return partialStream, err
	}, c.classifyRetryErrorAndDropCacheAffinity(&dropCacheAffinity), func(info retryAttemptInfo) {
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

	if !firstChunkAt.IsZero() {
		call.ttft = firstChunkAt.Sub(call.start)
	}
	call.attempts = attempts
	call.chunks = chunksReceived
	// partial_stream means visible content arrived but the call did not
	// complete: true on every successful stream would carry no
	// information. err != nil here always means the final attempt failed.
	call.partialStream = partialStream && err != nil
	call.responseHeaders = lastRespHeaders

	return err
}

func (c *Client) executeRequest(ctx context.Context, request ChatRequest, body []byte, stream bool) (*http.Response, error) {
	req, err := c.wire.HTTPRequest(ctx, request, body, stream)
	if err != nil {
		return nil, err
	}
	return c.executeHTTP(ctx, req)
}

// pace enforces the minimum interval between consecutive requests. It is a no-op
// when minInterval is zero; only the Codex backend configures one today.
func (c *Client) pace(ctx context.Context) error {
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

// classifyRetryErrorAndDropCacheAffinity wraps classifyRetryError and additionally
// sets *dropCacheAffinity when the retry was triggered by the known Codex
// prompt_cache_retention quirk (see isCodexPromptCacheRetentionRejection), so the
// caller can drop cache-affinity pinning on the next attempt to avoid landing back
// on the same bad backend replica.
func (c *Client) classifyRetryErrorAndDropCacheAffinity(dropCacheAffinity *bool) func(error) retryDecision {
	return func(err error) retryDecision {
		decision := c.classifyRetryError(err)
		if decision.retry && isCodexPromptCacheRetentionRejection(err) {
			*dropCacheAffinity = true
		}
		return decision
	}
}

// classifyRetryError decides whether err is worth another attempt. It applies the
// generic transport/HTTP/decode classification, then offers the decision to the
// wire when the wire can interpret errors the engine cannot.
func (c *Client) classifyRetryError(err error) retryDecision {
	decision := classifyProviderError(err, c.retry.RetryAfterMax)
	refiner, ok := c.wire.(retryRefiner)
	if !ok {
		return decision
	}
	decision = refiner.RefineRetry(err, decision)
	if c.retry.RetryAfterMax > 0 && decision.retryAfter > c.retry.RetryAfterMax {
		decision.retryAfter = c.retry.RetryAfterMax
	}
	return decision
}

func classifyProviderError(err error, retryAfterMax time.Duration) retryDecision {
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
		return retryDecision{retry: true, reason: err.Error()}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return retryDecision{retry: true, reason: err.Error()}
	}
	if httpErr := asHTTPError(err); httpErr != nil {
		if !isRetryableHTTPStatus(httpErr.StatusCode) {
			return retryDecision{}
		}
		delay, _ := retryAfterDelay(httpErr.Header, retryAfterMax)
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

func (c *Client) baseURLString() string {
	if c == nil || c.baseURL == nil {
		return ""
	}
	return c.baseURL.String()
}

func (c *Client) fullJitter(cap time.Duration) time.Duration {
	if cap <= 0 {
		return 0
	}
	if c == nil || c.rand == nil {
		return cap
	}
	max := int64(cap)
	if max <= 0 {
		return 0
	}
	c.randMu.Lock()
	defer c.randMu.Unlock()
	return time.Duration(c.rand.Int63n(max + 1))
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

// providerConfigHeaders returns the client's configured headers with the API
// key stripped. Only worth computing when the diagnostics stream may
// actually capture them; call sites should gate on c.streamErrorLog's
// captureBodies() rather than calling this unconditionally on every request.
func providerConfigHeaders(c *Client) map[string]string {
	if c == nil {
		return nil
	}
	return sanitizeHeaderMap(c.headers)
}

// diagnosticRequestHeaders returns providerConfigHeaders(c) only when the
// stream-error log may capture request bodies/headers, avoiding the map copy
// on every model call when it would just be discarded by emitProviderCall.
func diagnosticRequestHeaders(c *Client) map[string]string {
	if !c.streamErrorLog.captureBodies() {
		return nil
	}
	return providerConfigHeaders(c)
}

// sanitizeHeaderMap strips the Authorization key and copies the rest.
func sanitizeHeaderMap(h map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if strings.EqualFold(k, "authorization") {
			continue
		}
		out[k] = v
	}
	return out
}
