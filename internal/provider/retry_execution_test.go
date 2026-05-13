package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
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
			t.Fatalf("unexpected attempt %d", attempts)
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
			t.Fatalf("unexpected attempt %d", attempts)
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
			t.Fatalf("unexpected attempt %d", attempts)
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
			t.Fatalf("unexpected attempt %d", attempts)
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

func TestOpenAICompatSchedulerParallelismRespectedAcrossRetries(t *testing.T) {
	var attempts int32
	firstAttemptSeen := make(chan struct{})
	releaseSleep := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch atomic.AddInt32(&attempts, 1) {
		case 1:
			close(firstAttemptSeen)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "busy")
		case 2, 3:
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		default:
			t.Fatalf("unexpected attempt %d", attempts)
		}
	}))
	defer server.Close()

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: time.Second,
		MaxBackoff:     time.Second,
	})
	var sleepOnce sync.Once
	sleepStarted := make(chan struct{})
	provider.sleep = func(ctx context.Context, _ time.Duration) error {
		sleepOnce.Do(func() { close(sleepStarted) })
		select {
		case <-releaseSleep:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	provider.jitter = func(cap time.Duration) time.Duration { return cap }

	firstErr := make(chan error, 1)
	go func() {
		_, err := provider.ChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "one"}},
		})
		firstErr <- err
	}()

	select {
	case <-firstAttemptSeen:
	case <-time.After(time.Second):
		t.Fatal("first request did not reach server")
	}
	select {
	case <-sleepStarted:
	case <-time.After(time.Second):
		t.Fatal("retry sleep did not start")
	}

	secondErr := make(chan error, 1)
	go func() {
		_, err := provider.ChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "two"}},
		})
		secondErr <- err
	}()

	<-time.After(50 * time.Millisecond)
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts during sleep = %d, want 1", got)
	}

	close(releaseSleep)

	if err := <-firstErr; err != nil {
		t.Fatalf("first ChatCompletion() error = %v", err)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("second ChatCompletion() error = %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
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
				t.Fatal("writer does not support flushing")
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
			t.Fatalf("unexpected attempt %d", attempts)
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
				t.Fatal("writer does not support flushing")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"hello"}}]}`)
			flusher.Flush()
		case 2:
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("writer does not support flushing")
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
			t.Fatalf("unexpected attempt %d", attempts)
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
			t.Fatalf("unexpected attempt %d", attempts)
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

func mustRetryTestOpenAICompat(t *testing.T, baseURL string, retry RetryConfig) *OpenAICompat {
	t.Helper()
	provider := mustTestOpenAICompat(t, baseURL)
	provider.retry = retry
	provider.sleep = func(context.Context, time.Duration) error { return nil }
	provider.jitter = func(cap time.Duration) time.Duration { return cap }
	return provider
}
