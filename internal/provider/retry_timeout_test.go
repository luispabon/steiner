package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// headerTimeoutClientTimeout is short enough that a deliberately stalled
// handler trips it, and long enough that a fast follow-up attempt fits.
const headerTimeoutClientTimeout = 100 * time.Millisecond

func writeTestChatCompletion(w http.ResponseWriter) {
	_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
}

func writeTestChatStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
	flush()
	_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
	flush()
	_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n")
	flush()
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flush()
}

func TestChatCompletionRetriesClientTimeout(t *testing.T) {
	release := make(chan struct{})
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			<-release
			return
		}
		writeTestChatCompletion(w)
	}))
	defer server.Close()
	defer close(release)

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	provider.httpClient = &http.Client{Timeout: headerTimeoutClientTimeout}
	var sleeps []time.Duration
	provider.sleep = func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
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
	if len(sleeps) != 1 {
		t.Fatalf("retry sleeps = %v, want one", sleeps)
	}
}

func TestChatCompletionExhaustsClientTimeoutRetries(t *testing.T) {
	release := make(chan struct{})
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		<-release
	}))
	defer server.Close()
	defer close(release)

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	provider.httpClient = &http.Client{Timeout: headerTimeoutClientTimeout}
	var sleeps []time.Duration
	provider.sleep = func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return nil
	}

	_, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if len(sleeps) != 2 {
		t.Fatalf("retry sleeps = %v, want two", sleeps)
	}
}

func TestStreamChatCompletionRetriesClientTimeout(t *testing.T) {
	release := make(chan struct{})
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			<-release
			return
		}
		writeTestChatStream(w)
	}))
	defer server.Close()
	defer close(release)

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	provider.httpClient = &http.Client{Timeout: headerTimeoutClientTimeout}

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	var sawContent bool
	var sawDone bool
	var retryResets int
	for chunk := range ch {
		if chunk.RetryReset {
			retryResets++
		}
		if chunk.Error != "" {
			t.Fatalf("unexpected stream error = %v", chunk.Error)
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
	if retryResets != 0 {
		t.Fatalf("retry reset chunks = %d, want 0 before any content", retryResets)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestStreamChatCompletionExhaustsClientTimeoutRetries(t *testing.T) {
	release := make(chan struct{})
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		<-release
	}))
	defer server.Close()
	defer close(release)

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	provider.httpClient = &http.Client{Timeout: headerTimeoutClientTimeout}

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	var final *ChatChunk
	var retryResets int
	for chunk := range ch {
		if chunk.RetryReset {
			retryResets++
		}
		c := chunk
		final = &c
	}
	if final == nil || !final.Done {
		t.Fatal("stream did not end with a done chunk")
	}
	if final.Error == "" || final.OriginalError == nil {
		t.Fatalf("final chunk error = %q, original = %v, want timeout", final.Error, final.OriginalError)
	}
	if !errors.Is(final.OriginalError, context.DeadlineExceeded) {
		t.Fatalf("final error = %v, want context.DeadlineExceeded", final.OriginalError)
	}
	if retryResets != 0 {
		t.Fatalf("retry reset chunks = %d, want 0 before any content", retryResets)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestTimeoutRetryRespectsCallerDeadline(t *testing.T) {
	tests := []struct {
		name   string
		stream bool
	}{
		{name: "chat completion"},
		{name: "streaming", stream: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			var attempts int32
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&attempts, 1)
				<-release
			}))
			defer server.Close()
			defer close(release)

			provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: time.Millisecond,
				MaxBackoff:     time.Millisecond,
			})
			// The per-request timeout is far larger than the caller deadline, so
			// the failing error comes from the caller context, not the client.
			provider.httpClient = &http.Client{Timeout: 5 * time.Second}
			var sleeps int
			provider.sleep = func(context.Context, time.Duration) error {
				sleeps++
				return nil
			}

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			retryResets := 0
			if tt.stream {
				ch, err := provider.StreamChatCompletion(ctx, ChatRequest{
					Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
					Stream:   true,
				})
				if err != nil {
					t.Fatalf("StreamChatCompletion() error = %v", err)
				}
				for chunk := range ch {
					if chunk.RetryReset {
						retryResets++
					}
				}
			} else {
				_, err := provider.ChatCompletion(ctx, ChatRequest{
					Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
				})
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("ChatCompletion() error = %v, want context.DeadlineExceeded", err)
				}
			}

			if got := atomic.LoadInt32(&attempts); got != 1 {
				t.Fatalf("attempts = %d, want 1", got)
			}
			if sleeps != 0 {
				t.Fatalf("retry sleeps = %d, want 0", sleeps)
			}
			if retryResets != 0 {
				t.Fatalf("retry reset chunks = %d, want 0", retryResets)
			}
		})
	}
}

// headerResponseHeaderTimeout exercises Transport.ResponseHeaderTimeout
// directly. A provider Timeout config cannot be used here: NewOpenAICompat
// clones the client and clears ResponseHeaderTimeout when Timeout is set.
const headerResponseHeaderTimeout = 100 * time.Millisecond

func TestResponseHeaderTimeoutRetries(t *testing.T) {
	tests := []struct {
		name         string
		stream       bool
		maxAttempts  int
		stallAll     bool
		wantAttempts int32
		wantSleeps   int
		wantErr      bool
	}{
		{name: "chat completion recovers", maxAttempts: 2, wantAttempts: 2, wantSleeps: 1},
		{name: "chat completion exhausts", maxAttempts: 3, stallAll: true, wantAttempts: 3, wantSleeps: 2, wantErr: true},
		{name: "streaming recovers", stream: true, maxAttempts: 2, wantAttempts: 2, wantSleeps: 1},
		{name: "streaming exhausts", stream: true, maxAttempts: 3, stallAll: true, wantAttempts: 3, wantSleeps: 2, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			var requests int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempt := atomic.AddInt32(&requests, 1)
				if tt.stallAll || attempt == 1 {
					<-release
					return
				}
				if tt.stream {
					writeTestChatStream(w)
					return
				}
				writeTestChatCompletion(w)
			}))
			defer server.Close()
			defer close(release)

			provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
				Enabled:        true,
				MaxAttempts:    tt.maxAttempts,
				InitialBackoff: time.Millisecond,
				MaxBackoff:     time.Millisecond,
			})
			client := &http.Client{Transport: &http.Transport{
				ResponseHeaderTimeout: headerResponseHeaderTimeout,
			}}
			provider.httpClient = client
			defer client.CloseIdleConnections()

			var sleeps int
			provider.sleep = func(context.Context, time.Duration) error {
				sleeps++
				return nil
			}

			if tt.stream {
				ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
					Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
					Stream:   true,
				})
				if err != nil {
					t.Fatalf("StreamChatCompletion() error = %v", err)
				}

				var final *ChatChunk
				var sawContent bool
				var sawDone bool
				var retryResets int
				for chunk := range ch {
					if chunk.RetryReset {
						retryResets++
					}
					if chunk.Delta.Content != "" {
						sawContent = true
					}
					if chunk.Done {
						sawDone = true
					}
					c := chunk
					final = &c
				}
				if retryResets != 0 {
					t.Fatalf("retry reset chunks = %d, want 0 before any content", retryResets)
				}
				if tt.wantErr {
					if !sawDone || final == nil || final.Error == "" || !errors.Is(final.OriginalError, context.DeadlineExceeded) {
						t.Fatalf("final chunk = %+v, want done with response header timeout", final)
					}
				} else {
					if !sawContent || !sawDone {
						t.Fatalf("stream content = %v, done = %v, want both", sawContent, sawDone)
					}
					if final != nil && final.Error != "" {
						t.Fatalf("unexpected stream error = %v", final.Error)
					}
				}
			} else {
				resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
					Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
				})
				if tt.wantErr {
					if err == nil {
						t.Fatal("ChatCompletion() error = nil, want response header timeout")
					}
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("error = %v, want context.DeadlineExceeded", err)
					}
				} else {
					if err != nil {
						t.Fatalf("ChatCompletion() error = %v", err)
					}
					if got, want := resp.Message.Content, "ok"; got != want {
						t.Fatalf("content = %q, want %q", got, want)
					}
				}
			}

			if got := atomic.LoadInt32(&requests); got != tt.wantAttempts {
				t.Fatalf("requests = %d, want %d", got, tt.wantAttempts)
			}
			if sleeps != tt.wantSleeps {
				t.Fatalf("retry sleeps = %d, want %d", sleeps, tt.wantSleeps)
			}
		})
	}
}

func TestStreamChatCompletionDoesNotRetryClientTimeoutAfterContent(t *testing.T) {
	release := make(chan struct{})
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	}))
	defer server.Close()
	defer close(release)

	provider := mustRetryTestOpenAICompat(t, server.URL, RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	provider.httpClient = &http.Client{Timeout: headerTimeoutClientTimeout}
	var sleeps int
	provider.sleep = func(context.Context, time.Duration) error {
		sleeps++
		return nil
	}

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	var final *ChatChunk
	var sawContent bool
	var retryResets int
	for chunk := range ch {
		if chunk.RetryReset {
			retryResets++
		}
		if chunk.Delta.Content != "" {
			sawContent = true
		}
		c := chunk
		final = &c
	}
	if !sawContent {
		t.Fatal("expected content chunk before the timeout")
	}
	if retryResets != 0 {
		t.Fatalf("retry reset chunks = %d, want 0 after visible content", retryResets)
	}
	if final == nil || !final.Done || final.Error == "" {
		t.Fatalf("final chunk = %+v, want done with timeout error", final)
	}
	if !errors.Is(final.OriginalError, context.DeadlineExceeded) {
		t.Fatalf("final error = %v, want context.DeadlineExceeded", final.OriginalError)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
	if sleeps != 0 {
		t.Fatalf("retry sleeps = %d, want 0", sleeps)
	}
}
