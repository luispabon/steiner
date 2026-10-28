package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenAICompatChatCompletionRetries429ThenSucceeds(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch atomic.AddInt32(&attempts, 1) {
		case 1:
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, `{"error":"rate limited"}`)
		case 2:
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		default:
			t.Errorf("unexpected attempt %d", attempts)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}))
	defer server.Close()

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: 25 * time.Millisecond,
		MaxBackoff:     time.Second,
		RetryAfterMax:  10 * time.Second,
	})

	var slept []time.Duration
	provider.sleep = func(_ context.Context, delay time.Duration) error {
		slept = append(slept, delay)
		return nil
	}

	resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got, want := resp.Message.Content, "ok"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if len(slept) != 1 || slept[0] != 3*time.Second {
		t.Fatalf("sleep delays = %v, want [3s]", slept)
	}
}

func TestOpenAICompatChatCompletionRetries503ThenSucceeds(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch atomic.AddInt32(&attempts, 1) {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "busy")
		case 2:
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		default:
			t.Errorf("unexpected attempt %d", attempts)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}))
	defer server.Close()

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: 25 * time.Millisecond,
		MaxBackoff:     time.Second,
	})

	var slept []time.Duration
	provider.sleep = func(_ context.Context, _ time.Duration) error {
		return nil
	}
	provider.jitter = func(cap time.Duration) time.Duration {
		slept = append(slept, cap)
		return cap
	}

	resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got, want := resp.Message.Content, "ok"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if len(slept) != 1 || slept[0] != 25*time.Millisecond {
		t.Fatalf("backoff caps = %v, want [25ms]", slept)
	}
}

func TestOpenAICompatChatCompletionDoesNotRetry400(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, "bad request")
	}))
	defer server.Close()

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: 25 * time.Millisecond,
		MaxBackoff:     time.Second,
	})

	calls := 0
	provider.sleep = func(_ context.Context, _ time.Duration) error {
		calls++
		return nil
	}

	_, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want HTTP error")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error type = %T, want *HTTPError", err)
	}
	if got, want := httpErr.StatusCode, http.StatusBadRequest; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
	if calls != 0 {
		t.Fatalf("sleep calls = %d, want 0", calls)
	}
}

func TestOpenAICompatChatCompletionCapsRetryAfter(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch atomic.AddInt32(&attempts, 1) {
		case 1:
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, "retry later")
		case 2:
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		default:
			t.Errorf("unexpected attempt %d", attempts)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}))
	defer server.Close()

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: 25 * time.Millisecond,
		MaxBackoff:     time.Second,
		RetryAfterMax:  30 * time.Second,
	})

	var delays []time.Duration
	provider.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}

	_, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if len(delays) != 1 || delays[0] != 30*time.Second {
		t.Fatalf("delays = %v, want [30s]", delays)
	}
}

func TestOpenAICompatChatCompletionExhaustsAttemptsWithTypedError(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, "busy")
	}))
	defer server.Close()

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: 25 * time.Millisecond,
		MaxBackoff:     time.Second,
	})
	provider.sleep = func(_ context.Context, _ time.Duration) error { return nil }
	provider.jitter = func(cap time.Duration) time.Duration { return cap }

	_, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want failure")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error type = %T, want *HTTPError", err)
	}
	if got, want := httpErr.StatusCode, http.StatusServiceUnavailable; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestOpenAICompatChatCompletionCancellationDuringBackoffStopsImmediately(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch atomic.AddInt32(&attempts, 1) {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "busy")
		default:
			t.Errorf("unexpected attempt %d", attempts)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}))
	defer server.Close()

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: time.Second,
		MaxBackoff:     time.Second,
	})

	sleepStarted := make(chan struct{})
	provider.sleep = func(ctx context.Context, _ time.Duration) error {
		close(sleepStarted)
		<-ctx.Done()
		return ctx.Err()
	}
	provider.jitter = func(cap time.Duration) time.Duration { return cap }

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := provider.ChatCompletion(ctx, ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		})
		errCh <- err
	}()

	select {
	case <-sleepStarted:
	case <-time.After(time.Second):
		t.Fatal("retry sleep did not start")
	}

	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ChatCompletion() did not return after cancellation")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func TestOpenAICompatStreamChatCompletionRetries503BeforeSSEBody(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch atomic.AddInt32(&attempts, 1) {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "busy")
		case 2:
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Errorf("writer does not support flushing")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"role":"assistant"}}]}`)
			flusher.Flush()
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"hello"}}]}`)
			flusher.Flush()
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"finish_reason":"stop"}]}`)
			flusher.Flush()
			_, _ = fmt.Fprintf(w, "data: %s\n\n", "[DONE]")
			flusher.Flush()
		default:
			t.Errorf("unexpected attempt %d", attempts)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}))
	defer server.Close()

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: 25 * time.Millisecond,
		MaxBackoff:     time.Second,
	})
	provider.sleep = func(_ context.Context, _ time.Duration) error { return nil }
	provider.jitter = func(cap time.Duration) time.Duration { return cap }

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	var chunks []ChatChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	var sawContent bool
	var sawDone bool
	for _, chunk := range chunks {
		if chunk.Diagnostic != "" {
			t.Fatalf("unexpected diagnostic chunk: %#v", chunk)
		}
		if chunk.Delta.Content != "" {
			sawContent = true
		}
		if chunk.Done {
			sawDone = true
		}
	}
	if !sawContent {
		t.Fatal("expected content chunk")
	}
	if !sawDone {
		t.Fatal("expected final done chunk")
	}
}

func TestOpenAICompatStreamChatCompletionRetriesUnexpectedEOFBeforeFinalDone(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch atomic.AddInt32(&attempts, 1) {
		case 1:
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Errorf("writer does not support flushing")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"hello"}}]}`)
			flusher.Flush()
		case 2:
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Errorf("writer does not support flushing")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"role":"assistant"}}]}`)
			flusher.Flush()
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"world"}}]}`)
			flusher.Flush()
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"finish_reason":"stop"}]}`)
			flusher.Flush()
			_, _ = fmt.Fprintf(w, "data: %s\n\n", "[DONE]")
			flusher.Flush()
		default:
			t.Errorf("unexpected attempt %d", attempts)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}))
	defer server.Close()

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: 25 * time.Millisecond,
		MaxBackoff:     time.Second,
	})
	provider.sleep = func(_ context.Context, _ time.Duration) error { return nil }
	provider.jitter = func(cap time.Duration) time.Duration { return cap }

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	var sawRetryReset bool
	var sawContent int
	for chunk := range ch {
		if chunk.RetryReset {
			sawRetryReset = true
			if chunk.RetryAttempt != 2 || chunk.RetryMaxAttempts != 2 || chunk.RetryDelay <= 0 {
				t.Errorf("retry reset attempt = %d, max = %d, delay = %s, want attempt 2, max 2, positive delay", chunk.RetryAttempt, chunk.RetryMaxAttempts, chunk.RetryDelay)
			}
		}
		if chunk.Delta.Content != "" {
			sawContent++
		}
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if !sawRetryReset {
		t.Fatal("expected retry reset diagnostic chunk")
	}
	if sawContent < 2 {
		t.Fatalf("content chunk count = %d, want at least 2", sawContent)
	}
}

func TestOpenAICompatStreamChatCompletionDoesNotRetryCallerCancellation(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch atomic.AddInt32(&attempts, 1) {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "busy")
		default:
			t.Errorf("unexpected attempt %d", attempts)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}))
	defer server.Close()

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: time.Second,
		MaxBackoff:     time.Second,
	})

	sleepStarted := make(chan struct{})
	provider.sleep = func(ctx context.Context, _ time.Duration) error {
		close(sleepStarted)
		<-ctx.Done()
		return ctx.Err()
	}
	provider.jitter = func(cap time.Duration) time.Duration { return cap }

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := provider.StreamChatCompletion(ctx, ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	select {
	case <-sleepStarted:
	case <-time.After(time.Second):
		t.Fatal("retry sleep did not start")
	}
	cancel()

	for range ch {
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func TestOpenAICompatStreamChatCompletionAfterExhaustionSendsFinalErrorChunk(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, "busy")
	}))
	defer server.Close()

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: 25 * time.Millisecond,
		MaxBackoff:     time.Second,
	})
	provider.sleep = func(_ context.Context, _ time.Duration) error { return nil }
	provider.jitter = func(cap time.Duration) time.Duration { return cap }

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	chunk, ok := <-ch
	if !ok {
		t.Fatal("stream closed before final error chunk")
	}
	if !chunk.Done {
		t.Fatal("final error chunk Done = false, want true")
	}
	if chunk.Error == "" {
		t.Fatal("final error chunk Error = empty")
	}
	if chunk.OriginalError == nil {
		t.Fatal("final error chunk OriginalError = nil")
	}
	var httpErr *HTTPError
	if !errors.As(chunk.OriginalError, &httpErr) {
		t.Fatalf("OriginalError type = %T, want *HTTPError", chunk.OriginalError)
	}
	if got, want := httpErr.StatusCode, http.StatusServiceUnavailable; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}
	if _, ok := <-ch; ok {
		t.Fatal("stream produced unexpected extra chunk")
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

// scriptedTransport answers each request with script, called with the 1-based
// attempt number, so retry tests run without a network listener.
type scriptedTransport struct {
	attempts int32
	script   func(attempt int32, req *http.Request) (*http.Response, error)
}

func (s *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return s.script(atomic.AddInt32(&s.attempts, 1), req)
}

// failingReader returns err from Read. onRead, when set, runs first to simulate
// the caller cancelling while the body is read.
type failingReader struct {
	onRead func()
	err    error
}

func (r failingReader) Read([]byte) (int, error) {
	if r.onRead != nil {
		r.onRead()
	}
	return 0, r.err
}

// testResponse builds a 200 response with the given content type and body.
func testResponse(contentType string, body io.Reader) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(body),
	}
}

func TestOpenAICompatChatCompletionRetriesClosedNetworkConnection(t *testing.T) {
	transport := &scriptedTransport{script: func(attempt int32, _ *http.Request) (*http.Response, error) {
		if attempt == 1 {
			return nil, &net.OpError{Op: "write", Net: "tcp", Err: net.ErrClosed}
		}
		return testResponse("application/json", strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)), nil
	}}
	provider := mustRetryTestOpenAICompat(t, "http://provider.test", RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	provider.httpClient = &http.Client{Transport: transport}
	var sleeps int
	provider.sleep = func(context.Context, time.Duration) error {
		sleeps++
		return nil
	}

	resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got, want := resp.Message.Content, "ok"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if got := atomic.LoadInt32(&transport.attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if sleeps != 1 {
		t.Fatalf("retry sleeps = %d, want 1", sleeps)
	}
}

func TestOpenAICompatChatCompletionDoesNotRetryDecodeWrappedClosedConnection(t *testing.T) {
	closedErr := &net.OpError{Op: "read", Net: "tcp", Err: net.ErrClosed}
	transport := &scriptedTransport{script: func(int32, *http.Request) (*http.Response, error) {
		return testResponse("application/json", failingReader{err: closedErr}), nil
	}}
	provider := mustRetryTestOpenAICompat(t, "http://provider.test", RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	provider.httpClient = &http.Client{Transport: transport}
	var sleeps int
	provider.sleep = func(context.Context, time.Duration) error {
		sleeps++
		return nil
	}

	_, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if !errors.Is(err, errDecodeChatCompletionResponse) || !errors.Is(err, net.ErrClosed) {
		t.Fatalf("ChatCompletion() error = %v, want decode error wrapping closed connection", err)
	}
	if got := atomic.LoadInt32(&transport.attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
	if sleeps != 0 {
		t.Fatalf("retry sleeps = %d, want 0", sleeps)
	}
}

func TestOpenAICompatChatCompletionDoesNotRetryClosedNetworkConnectionOnCallerCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &scriptedTransport{script: func(int32, *http.Request) (*http.Response, error) {
		cancel()
		return nil, &net.OpError{Op: "write", Net: "tcp", Err: net.ErrClosed}
	}}
	provider := mustRetryTestOpenAICompat(t, "http://provider.test", RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	provider.httpClient = &http.Client{Transport: transport}
	var sleeps int
	provider.sleep = func(context.Context, time.Duration) error {
		sleeps++
		return nil
	}

	_, err := provider.ChatCompletion(ctx, ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want closed connection error")
	}
	if got := atomic.LoadInt32(&transport.attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
	if sleeps != 0 {
		t.Fatalf("retry sleeps = %d, want 0", sleeps)
	}
}

func TestOpenAICompatStreamChatCompletionDoesNotRetryClosedConnectionAfterVisibleOutput(t *testing.T) {
	const firstChunk = "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"
	closedErr := &net.OpError{Op: "read", Net: "tcp", Err: net.ErrClosed}
	transport := &scriptedTransport{script: func(int32, *http.Request) (*http.Response, error) {
		body := io.MultiReader(strings.NewReader(firstChunk), failingReader{err: closedErr})
		return testResponse("text/event-stream", body), nil
	}}
	provider := mustRetryTestOpenAICompat(t, "http://provider.test", RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	provider.httpClient = &http.Client{Transport: transport}

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	var retryResets int
	var content strings.Builder
	var final ChatChunk
	for chunk := range ch {
		if chunk.RetryReset {
			retryResets++
		}
		// The Done chunk repeats the final attempt's accumulated content, so only
		// streamed deltas are counted.
		if !chunk.Done {
			content.WriteString(chunk.Delta.Content)
		}
		final = chunk
	}
	if got := atomic.LoadInt32(&transport.attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
	if retryResets != 0 {
		t.Fatalf("retry reset chunks = %d, want 0", retryResets)
	}
	if got, want := content.String(), "hello"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if !final.Done || !errors.Is(final.OriginalError, net.ErrClosed) {
		t.Fatalf("final chunk = %+v, want done with closed connection error", final)
	}
}

func TestOpenAICompatStreamChatCompletionDoesNotRetryClosedConnectionOnCallerCancel(t *testing.T) {
	const firstChunk = "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &scriptedTransport{script: func(int32, *http.Request) (*http.Response, error) {
		body := io.MultiReader(strings.NewReader(firstChunk), failingReader{
			onRead: cancel,
			err:    &net.OpError{Op: "read", Net: "tcp", Err: net.ErrClosed},
		})
		return testResponse("text/event-stream", body), nil
	}}
	provider := mustRetryTestOpenAICompat(t, "http://provider.test", RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	provider.httpClient = &http.Client{Transport: transport}
	var sleeps int
	provider.sleep = func(context.Context, time.Duration) error {
		sleeps++
		return nil
	}
	logPath := attachStreamErrorLog(t, provider)

	ch, err := provider.StreamChatCompletion(ctx, ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	var retryResets int
	for chunk := range ch {
		if chunk.RetryReset {
			retryResets++
		}
	}
	if got := atomic.LoadInt32(&transport.attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
	if retryResets != 0 {
		t.Fatalf("retry reset chunks = %d, want 0", retryResets)
	}
	if sleeps != 0 {
		t.Fatalf("retry sleeps = %d, want 0", sleeps)
	}

	// The terminal error is recorded even though no chunk may reach a
	// cancelled caller, so the diagnostics record is the deterministic check.
	records := readStreamErrorRecords(t, logPath)
	if len(records) != 1 {
		t.Fatalf("stream error log records = %d, want 1", len(records))
	}
	if got, want := records[0].Outcome, "cancelled"; got != want {
		t.Errorf("outcome = %q, want %q", got, want)
	}
	if !strings.Contains(records[0].Error, "use of closed network connection") {
		t.Errorf("error = %q, want closed network connection error", records[0].Error)
	}
}

func TestOpenAICompatStreamChatCompletionRetriesClosedConnectionBeforeContent(t *testing.T) {
	closedErr := &net.OpError{Op: "read", Net: "tcp", Err: net.ErrClosed}
	transport := &scriptedTransport{script: func(attempt int32, _ *http.Request) (*http.Response, error) {
		if attempt == 1 {
			return testResponse("text/event-stream", failingReader{err: closedErr}), nil
		}
		return testResponse("text/event-stream", strings.NewReader(
			"data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n"+
				"data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n"+
				"data: [DONE]\n\n")), nil
	}}
	provider := mustRetryTestOpenAICompat(t, "http://provider.test", RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	provider.httpClient = &http.Client{Transport: transport}
	logPath := attachStreamErrorLog(t, provider)

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	var retryResets int
	var content strings.Builder
	var final ChatChunk
	for chunk := range ch {
		if chunk.RetryReset {
			retryResets++
		}
		if !chunk.Done {
			content.WriteString(chunk.Delta.Content)
		}
		final = chunk
	}
	if got := atomic.LoadInt32(&transport.attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if retryResets != 0 {
		t.Fatalf("retry reset chunks = %d, want 0 before visible output", retryResets)
	}
	if got, want := content.String(), "world"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if !final.Done || final.Error != "" {
		t.Fatalf("final chunk = %+v, want successful done", final)
	}

	records := readStreamErrorRecords(t, logPath)
	if len(records) != 1 {
		t.Fatalf("stream error log records = %d, want 1", len(records))
	}
	if got, want := records[0].Outcome, "retried"; got != want {
		t.Errorf("outcome = %q, want %q", got, want)
	}
	if got, want := records[0].Attempts, 2; got != want {
		t.Errorf("attempts = %d, want %d", got, want)
	}
	if records[0].ErrorClass != "" {
		t.Errorf("error_class = %q, want empty on success", records[0].ErrorClass)
	}
}

func TestOpenAICompatStreamChatCompletionExhaustsClosedConnectionBeforeContent(t *testing.T) {
	closedErr := &net.OpError{Op: "read", Net: "tcp", Err: net.ErrClosed}
	transport := &scriptedTransport{script: func(int32, *http.Request) (*http.Response, error) {
		return testResponse("text/event-stream", failingReader{err: closedErr}), nil
	}}
	provider := mustRetryTestOpenAICompat(t, "http://provider.test", RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	provider.httpClient = &http.Client{Transport: transport}
	logPath := attachStreamErrorLog(t, provider)

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	var retryResets int
	var final ChatChunk
	for chunk := range ch {
		if chunk.RetryReset {
			retryResets++
		}
		final = chunk
	}
	if got := atomic.LoadInt32(&transport.attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if retryResets != 0 {
		t.Fatalf("retry reset chunks = %d, want 0 before visible output", retryResets)
	}
	if !final.Done || !errors.Is(final.OriginalError, net.ErrClosed) {
		t.Fatalf("final chunk = %+v, want done with closed connection error", final)
	}

	records := readStreamErrorRecords(t, logPath)
	if len(records) != 1 {
		t.Fatalf("stream error log records = %d, want 1", len(records))
	}
	if got, want := records[0].Outcome, "exhausted"; got != want {
		t.Errorf("outcome = %q, want %q", got, want)
	}
	if got, want := records[0].Attempts, 2; got != want {
		t.Errorf("attempts = %d, want %d", got, want)
	}
	if got, want := records[0].ErrorClass, "connection_reset"; got != want {
		t.Errorf("error_class = %q, want %q", got, want)
	}
}

// attachStreamErrorLog points provider at a diagnostics log in a temp dir and
// returns the log path.
func attachStreamErrorLog(t *testing.T, provider *Client) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "stream-errors.log")
	streamErrorLog, err := NewStreamErrorLogger(logPath)
	if err != nil {
		t.Fatalf("NewStreamErrorLogger() error = %v", err)
	}
	t.Cleanup(func() { _ = streamErrorLog.Close() })
	provider.streamErrorLog = streamErrorLog
	return logPath
}

func mustRetryTestOpenAICompat(t *testing.T, baseURL string, retry RetryConfig) *Client {
	t.Helper()
	provider := mustTestOpenAICompat(t, baseURL)
	provider.retry = retry
	provider.sleep = func(context.Context, time.Duration) error { return nil }
	provider.jitter = func(cap time.Duration) time.Duration { return cap }
	return provider
}
