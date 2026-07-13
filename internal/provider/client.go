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
	scheduler      *Scheduler
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
func (c *Client) ChatCompletion(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	if c == nil {
		return ChatResponse{}, fmt.Errorf("provider is not initialized")
	}
	if err := c.pace(ctx); err != nil {
		return ChatResponse{}, err
	}
	if err := c.acquire(ctx); err != nil {
		return ChatResponse{}, err
	}
	defer c.release()

	body, err := c.wire.Payload(request, false)
	if err != nil {
		return ChatResponse{}, err
	}

	var response ChatResponse
	err = c.withRetry(ctx, func(_ int) (bool, error) {
		resp, err := c.executeRequest(ctx, request, body, false)
		if err != nil {
			return false, err
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		response, err = c.wire.DecodeResponse(resp)
		if err == nil {
			observePromptTokenUsage(ctx, request, response.Usage)
		}
		return false, err
	}, c.classifyRetryError, nil)
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
	if err := c.pace(ctx); err != nil {
		out := make(chan ChatChunk)
		close(out)
		return out, err
	}

	out := make(chan ChatChunk)
	go func() {
		defer close(out)

		if err := c.streamWithRetry(ctx, request, out); err != nil {
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
func (c *Client) streamWithRetry(ctx context.Context, request ChatRequest, out chan<- ChatChunk) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()

	body, err := c.wire.Payload(request, true)
	if err != nil {
		return err
	}

	var (
		streamStart     time.Time
		chunksReceived  int
		contentBytes    int
		lastRespHeaders http.Header
	)

	return c.withRetry(ctx, func(_ int) (bool, error) {
		resp, err := c.executeRequest(ctx, request, body, true)
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
		err = c.wire.DecodeStream(ctx, resp.Body, func(chunk ChatChunk) error {
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
	}, c.classifyRetryError, func(info retryAttemptInfo) {
		c.streamErrorLog.Log(streamErrorRecord{
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
			RequestURL:      c.baseURLString(),
			RequestHeaders:  providerConfigHeaders(c),
			ResponseHeaders: sanitizeHeaders(lastRespHeaders),
			RequestBody:     json.RawMessage(append([]byte(nil), body...)),
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
	})
}

func (c *Client) executeRequest(ctx context.Context, request ChatRequest, body []byte, stream bool) (*http.Response, error) {
	req, err := c.wire.HTTPRequest(ctx, request, body, stream)
	if err != nil {
		return nil, err
	}
	return c.executeHTTP(ctx, req)
}

func (c *Client) acquire(ctx context.Context) error {
	if c.scheduler == nil {
		return nil
	}
	return c.scheduler.Acquire(ctx)
}

func (c *Client) release() {
	if c.scheduler == nil {
		return
	}
	c.scheduler.Release()
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

// providerConfigHeaders returns the client's configured headers with the API key stripped.
func providerConfigHeaders(c *Client) map[string]string {
	if c == nil {
		return nil
	}
	out := make(map[string]string, len(c.headers))
	for k, v := range c.headers {
		if strings.EqualFold(k, "authorization") {
			continue
		}
		out[k] = v
	}
	return out
}
