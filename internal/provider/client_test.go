package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeWire is a Provider Wire that speaks no real format: it lets the engine
// (retry, pacing, scheduling, stream bookkeeping) be exercised once, without
// per-provider HTTP fixtures.
type fakeWire struct {
	target string

	payloadErr     error
	payloadCalls   atomic.Int32
	streamPayloads atomic.Int32

	decodeResponse func(*http.Response) (ChatResponse, error)
	decodeStream   func(context.Context, io.Reader, func(ChatChunk) error) error
}

func (w *fakeWire) Payload(_ ChatRequest, stream bool) ([]byte, error) {
	w.payloadCalls.Add(1)
	if stream {
		w.streamPayloads.Add(1)
	}
	if w.payloadErr != nil {
		return nil, w.payloadErr
	}
	return []byte(`{"fake":true}`), nil
}

func (w *fakeWire) HTTPRequest(ctx context.Context, _ ChatRequest, body []byte, stream bool) (*http.Request, error) {
	return buildJSONPostRequest(ctx, w.target, body, stream, "", nil)
}

func (w *fakeWire) DecodeResponse(resp *http.Response) (ChatResponse, error) {
	if w.decodeResponse != nil {
		return w.decodeResponse(resp)
	}
	return ChatResponse{Message: Message{Role: MessageRoleAssistant, Content: "ok"}}, nil
}

func (w *fakeWire) DecodeStream(ctx context.Context, body io.Reader, emit func(ChatChunk) error) error {
	if w.decodeStream != nil {
		return w.decodeStream(ctx, body, emit)
	}
	return emit(ChatChunk{Done: true})
}

// refiningWire is a fakeWire that also implements retryRefiner.
type refiningWire struct {
	*fakeWire
	refine func(error, retryDecision) retryDecision
}

func (w *refiningWire) RefineRetry(err error, decision retryDecision) retryDecision {
	return w.refine(err, decision)
}

// newFakeWireClient builds an engine wired to wire, with deterministic sleep and
// jitter so retry delays are observable.
func newFakeWireClient(t *testing.T, wire Wire, retry RetryConfig) (*Client, *[]time.Duration) {
	t.Helper()
	client, err := NewOpenAICompat(ClientConfig{
		BaseURL:   "http://fake.invalid/v1",
		Model:     "test-model",
		Scheduler: mustTestScheduler(t, 1),
		Retry:     retry,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompat() error = %v", err)
	}
	client.wire = wire

	var slept []time.Duration
	client.sleep = func(_ context.Context, delay time.Duration) error {
		slept = append(slept, delay)
		return nil
	}
	client.jitter = func(cap time.Duration) time.Duration { return cap }
	return client, &slept
}

func TestClientChatCompletionRetryPolicy(t *testing.T) {
	retry := RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: 20 * time.Millisecond,
		MaxBackoff:     time.Second,
		RetryAfterMax:  30 * time.Second,
	}

	tests := []struct {
		name         string
		statuses     []int
		retryAfter   string
		wantAttempts int32
		wantSlept    []time.Duration
		wantErr      bool
	}{
		{
			name:         "retries 503 with exponential backoff",
			statuses:     []int{http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK},
			wantAttempts: 3,
			wantSlept:    []time.Duration{20 * time.Millisecond, 40 * time.Millisecond},
		},
		{
			name:         "honours Retry-After over backoff",
			statuses:     []int{http.StatusTooManyRequests, http.StatusOK},
			retryAfter:   "3",
			wantAttempts: 2,
			wantSlept:    []time.Duration{3 * time.Second},
		},
		{
			name:         "caps Retry-After at RetryAfterMax",
			statuses:     []int{http.StatusTooManyRequests, http.StatusOK},
			retryAfter:   "120",
			wantAttempts: 2,
			wantSlept:    []time.Duration{30 * time.Second},
		},
		{
			name:         "does not retry 400",
			statuses:     []int{http.StatusBadRequest},
			wantAttempts: 1,
			wantErr:      true,
		},
		{
			name:         "gives up after MaxAttempts",
			statuses:     []int{http.StatusBadGateway, http.StatusBadGateway, http.StatusBadGateway},
			wantAttempts: 3,
			wantSlept:    []time.Duration{20 * time.Millisecond, 40 * time.Millisecond},
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n := int(attempts.Add(1))
				if n > len(tt.statuses) {
					t.Errorf("unexpected attempt %d", n)
					return
				}
				status := tt.statuses[n-1]
				if tt.retryAfter != "" && status != http.StatusOK {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(status)
			}))
			defer server.Close()

			wire := &fakeWire{target: server.URL}
			client, slept := newFakeWireClient(t, wire, retry)

			_, err := client.ChatCompletion(context.Background(), ChatRequest{
				Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
			})
			if tt.wantErr && err == nil {
				t.Fatal("ChatCompletion() error = nil, want failure")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ChatCompletion() error = %v", err)
			}
			if got := attempts.Load(); got != tt.wantAttempts {
				t.Fatalf("attempts = %d, want %d", got, tt.wantAttempts)
			}
			if got, want := fmt.Sprint(*slept), fmt.Sprint(tt.wantSlept); got != want {
				t.Fatalf("retry delays = %s, want %s", got, want)
			}
			// The payload is built once and reused across attempts.
			if got := wire.payloadCalls.Load(); got != 1 {
				t.Fatalf("Payload() calls = %d, want 1", got)
			}
		})
	}
}

func TestClientRefinesRetryDecisionThroughWire(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	var offered retryDecision
	wire := &refiningWire{
		fakeWire: &fakeWire{target: server.URL},
		refine: func(_ error, decision retryDecision) retryDecision {
			offered = decision
			// The wire alone can tell this 429 is permanent.
			return retryDecision{}
		},
	}
	client, _ := newFakeWireClient(t, wire, RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
	})

	if _, err := client.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
	}); err == nil {
		t.Fatal("ChatCompletion() error = nil, want failure")
	}
	if !offered.retry {
		t.Fatal("engine offered a non-retryable decision for a 429, want retryable")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (wire refused the retry)", got)
	}
}

func TestClientStreamChatCompletionEmitsRetryResetAfterPartialStream(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
	}))
	defer server.Close()

	wire := &fakeWire{target: server.URL}
	wire.decodeStream = func(_ context.Context, _ io.Reader, emit func(ChatChunk) error) error {
		if attempts.Load() == 1 {
			// First attempt delivers visible content, then the stream dies.
			if err := emit(ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: "par"}}); err != nil {
				return err
			}
			return io.ErrUnexpectedEOF
		}
		if err := emit(ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: "tial"}}); err != nil {
			return err
		}
		return emit(ChatChunk{Done: true, FinishReason: "stop"})
	}

	client, _ := newFakeWireClient(t, wire, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
	})

	ch, err := client.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	var (
		content       string
		sawRetryReset bool
		sawDone       bool
	)
	for chunk := range ch {
		if chunk.RetryReset {
			sawRetryReset = true
			if chunk.Severity != "warning" {
				t.Fatalf("retry reset severity = %q, want warning", chunk.Severity)
			}
			continue
		}
		content += chunk.Delta.Content
		if chunk.Done {
			sawDone = true
		}
	}
	if !sawRetryReset {
		t.Fatal("no RetryReset chunk emitted after a partial stream was retried")
	}
	if !sawDone {
		t.Fatal("no terminal chunk emitted")
	}
	if got, want := content, "partial"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if got := wire.streamPayloads.Load(); got != 1 {
		t.Fatalf("stream payload builds = %d, want 1", got)
	}
}

func TestClientStreamChatCompletionSuppressesRetryResetWithoutPartialStream(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()

	wire := &fakeWire{target: server.URL}
	client, _ := newFakeWireClient(t, wire, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
	})

	ch, err := client.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}
	for chunk := range ch {
		if chunk.RetryReset {
			t.Fatal("RetryReset chunk emitted although nothing had been streamed yet")
		}
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestClientStreamChatCompletionReleasesSchedulerSlot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()

	client, _ := newFakeWireClient(t, &fakeWire{target: server.URL}, RetryConfig{})
	scheduler := client.scheduler

	for i := 0; i < 3; i++ {
		ch, err := client.StreamChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
			Stream:   true,
		})
		if err != nil {
			t.Fatalf("StreamChatCompletion() error = %v", err)
		}
		for range ch {
		}
	}

	// A leaked slot would block here: the scheduler has a parallelism of one.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := scheduler.Acquire(ctx); err != nil {
		t.Fatalf("scheduler slot not released after streaming: %v", err)
	}
	scheduler.Release()
}

func TestClientPaceEnforcesMinInterval(t *testing.T) {
	tests := []struct {
		name           string
		minInterval    time.Duration
		requests       int
		wantMinElapsed time.Duration
	}{
		{
			name:           "paces consecutive requests",
			minInterval:    50 * time.Millisecond,
			requests:       3,
			wantMinElapsed: 100 * time.Millisecond,
		},
		{
			name:        "no delay when interval is zero",
			minInterval: 0,
			requests:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewCodexResponses(ClientConfig{
				BaseURL:            "https://chatgpt.com/backend-api/codex",
				Model:              "gpt-5.4-mini",
				Scheduler:          mustTestScheduler(t, 1),
				MinRequestInterval: tt.minInterval,
			})
			if err != nil {
				t.Fatalf("NewCodexResponses() error = %v", err)
			}

			start := time.Now()
			for i := 0; i < tt.requests; i++ {
				if err := client.pace(context.Background()); err != nil {
					t.Fatalf("pace() error = %v", err)
				}
			}
			if elapsed := time.Since(start); elapsed < tt.wantMinElapsed {
				t.Fatalf("elapsed = %v, want >= %v", elapsed, tt.wantMinElapsed)
			}
		})
	}
}

func TestClientPaceHonoursContextCancellation(t *testing.T) {
	client, err := NewCodexResponses(ClientConfig{
		BaseURL:            "https://chatgpt.com/backend-api/codex",
		Model:              "gpt-5.4-mini",
		Scheduler:          mustTestScheduler(t, 1),
		MinRequestInterval: time.Second,
	})
	if err != nil {
		t.Fatalf("NewCodexResponses() error = %v", err)
	}

	// Prime lastRequest so the next call has to wait.
	if err := client.pace(context.Background()); err != nil {
		t.Fatalf("pace() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(1 * time.Millisecond)
		cancel()
	}()

	if err := client.pace(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("pace() error = %v, want context.Canceled", err)
	}
}

func TestClientObservesPromptTokenUsage(t *testing.T) {
	tests := []struct {
		name   string
		stream bool
	}{
		{name: "non-stream response"},
		{name: "stream done chunk", stream: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := &UsageStats{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18}
			counter := &recordingTokenCounter{}
			restore := defaultTokenCounter
			defaultTokenCounter = counter
			defer func() { defaultTokenCounter = restore }()

			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
			defer server.Close()

			wire := &fakeWire{target: server.URL}
			wire.decodeResponse = func(*http.Response) (ChatResponse, error) {
				return ChatResponse{Message: Message{Role: MessageRoleAssistant, Content: "ok"}, Usage: usage}, nil
			}
			wire.decodeStream = func(_ context.Context, _ io.Reader, emit func(ChatChunk) error) error {
				return emit(ChatChunk{Done: true, Usage: usage})
			}
			client, _ := newFakeWireClient(t, wire, RetryConfig{})

			request := ChatRequest{Messages: []Message{{Role: MessageRoleUser, Content: "hi"}}}
			if tt.stream {
				ch, err := client.StreamChatCompletion(context.Background(), request)
				if err != nil {
					t.Fatalf("StreamChatCompletion() error = %v", err)
				}
				for range ch {
				}
			} else if _, err := client.ChatCompletion(context.Background(), request); err != nil {
				t.Fatalf("ChatCompletion() error = %v", err)
			}

			if got := counter.observed.Load(); got != 1 {
				t.Fatalf("usage observations = %d, want 1", got)
			}
		})
	}
}

// TestClientStreamRetryIsLoggedForEveryWire is the regression test for the
// stream-error-logging gap: Anthropic and Codex hand-wrote their own retry
// callbacks and both omitted the StreamErrorLog write, so stream-retry
// diagnostics were silently dropped for those two providers. The engine now owns
// the callback, so a stream retry must be recorded whatever wire is in use.
func TestClientStreamRetryIsLoggedForEveryWire(t *testing.T) {
	tests := []struct {
		name       string
		newClient  func(ClientConfig) (*Client, error)
		streamBody string
	}{
		{
			name:      "openai",
			newClient: NewOpenAICompat,
			streamBody: strings.Join([]string{
				`data: {"choices":[{"delta":{"content":"hello"}}]}`,
				``,
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				``,
				`data: [DONE]`,
				``,
			}, "\n"),
		},
		{
			name:      "anthropic",
			newClient: NewAnthropic,
			streamBody: strings.Join([]string{
				`event: content_block_start`,
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
				``,
				`event: message_delta`,
				`data: {"type":"message_delta","delta":{"type":"message_delta","stop_reason":"end_turn"}}`,
				``,
				`event: message_stop`,
				`data: {"type":"message_stop"}`,
				``,
			}, "\n"),
		},
		{
			name:      "codex responses",
			newClient: NewCodexResponses,
			streamBody: strings.Join([]string{
				`data: {"type":"response.output_text.delta","delta":"hello"}`,
				``,
				`data: {"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
				``,
			}, "\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if attempts.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = fmt.Fprint(w, "busy")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, tt.streamBody)
			}))
			defer server.Close()

			logPath := filepath.Join(t.TempDir(), "stream-errors.log")
			streamErrorLog, err := NewStreamErrorLogger(logPath)
			if err != nil {
				t.Fatalf("NewStreamErrorLogger() error = %v", err)
			}
			defer func() {
				_ = streamErrorLog.Close()
			}()

			client, err := tt.newClient(ClientConfig{
				BaseURL:   server.URL + "/v1",
				Model:     "test-model",
				Scheduler: mustTestScheduler(t, 1),
				Retry: RetryConfig{
					Enabled:        true,
					MaxAttempts:    2,
					InitialBackoff: time.Millisecond,
				},
				StreamErrorLog: streamErrorLog,
			})
			if err != nil {
				t.Fatalf("new client error = %v", err)
			}
			client.sleep = func(context.Context, time.Duration) error { return nil }

			ch, err := client.StreamChatCompletion(context.Background(), ChatRequest{
				Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
				Stream:   true,
			})
			if err != nil {
				t.Fatalf("StreamChatCompletion() error = %v", err)
			}
			for chunk := range ch {
				if chunk.Error != "" {
					t.Fatalf("stream failed: %s", chunk.Error)
				}
			}
			if got := attempts.Load(); got != 2 {
				t.Fatalf("attempts = %d, want 2", got)
			}

			records := readStreamErrorRecords(t, logPath)
			if len(records) != 1 {
				t.Fatalf("stream error log records = %d, want 1", len(records))
			}
			if got, want := records[0].Event, "stream_retry"; got != want {
				t.Fatalf("event = %q, want %q", got, want)
			}
			if got, want := records[0].Attempt, 2; got != want {
				t.Fatalf("attempt = %d, want %d", got, want)
			}
			if records[0].Error == "" {
				t.Fatal("record error = empty, want the 503 reason")
			}
			if records[0].RequestURL != server.URL+"/v1" {
				t.Fatalf("request URL = %q, want %q", records[0].RequestURL, server.URL+"/v1")
			}
			if len(records[0].RequestBody) == 0 {
				t.Fatal("record request body = empty, want the wire payload")
			}
		})
	}
}

// hostRewriteTransport routes every request to target while leaving the request's
// own URL host intact from the wire's point of view, so host-dependent wire
// behaviour (e.g. store: false on OpenAI-operated hosts) still applies.
type hostRewriteTransport struct {
	target *url.URL
}

func (t *hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	routed := req.Clone(req.Context())
	routed.URL.Scheme = t.target.Scheme
	routed.URL.Host = t.target.Host
	routed.Host = ""
	return http.DefaultTransport.RoundTrip(routed)
}

// capturedRequest is what a backend actually received.
type capturedRequest struct {
	path    string
	headers http.Header
	body    map[string]any
}

// TestClientCodexCacheHintsReachTheBackend drives one ChatRequest through the
// engine and asserts every Codex cache hint on the single request the backend
// receives. The hints are empirically worth ~0.68 -> ~0.89 prompt-cache hit rate
// (issue #318) and all derive from the same ChatRequest.PromptCacheKey. The
// per-wire tests each prove one hint in isolation by calling a wire method
// directly, which cannot catch the header key and the body key drifting apart,
// nor the engine failing to hand the ChatRequest to the wire at all.
func TestClientCodexCacheHintsReachTheBackend(t *testing.T) {
	const promptCacheKey = "session-123"

	tests := []struct {
		name       string
		stream     bool
		respond    func(http.ResponseWriter)
		wantAccept string
	}{
		{
			name: "non-stream",
			respond: func(w http.ResponseWriter) {
				_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
			},
		},
		{
			name:   "stream",
			stream: true,
			respond: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, strings.Join([]string{
					`data: {"type":"response.output_text.delta","delta":"ok"}`,
					``,
					`data: {"type":"response.completed","response":{"status":"completed","output":[]}}`,
					``,
				}, "\n"))
			},
			wantAccept: "text/event-stream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured capturedRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured.path = r.URL.Path
				captured.headers = r.Header.Clone()
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
					return
				}
				if err := json.Unmarshal(raw, &captured.body); err != nil {
					t.Errorf("decode request body %q: %v", raw, err)
					return
				}
				tt.respond(w)
			}))
			defer server.Close()

			client, err := NewCodexResponses(ClientConfig{
				// The real Codex backend host: it is what makes the wire ask for
				// store: false, so the request is routed to the test server by
				// transport rather than by base URL.
				BaseURL:    "https://chatgpt.com/backend-api/codex",
				APIKey:     "codex-token",
				Model:      "gpt-5.4-mini",
				Scheduler:  mustTestScheduler(t, 1),
				HTTPClient: &http.Client{Transport: mustHostRewriteTransport(t, server.URL)},
			})
			if err != nil {
				t.Fatalf("NewCodexResponses() error = %v", err)
			}

			request := ChatRequest{
				Messages:       []Message{{Role: MessageRoleUser, Content: "hi"}},
				PromptCacheKey: promptCacheKey,
			}
			if tt.stream {
				ch, err := client.StreamChatCompletion(context.Background(), request)
				if err != nil {
					t.Fatalf("StreamChatCompletion() error = %v", err)
				}
				for chunk := range ch {
					if chunk.Error != "" {
						t.Fatalf("stream failed: %s", chunk.Error)
					}
				}
			} else if _, err := client.ChatCompletion(context.Background(), request); err != nil {
				t.Fatalf("ChatCompletion() error = %v", err)
			}

			if got, want := captured.path, "/backend-api/codex/responses"; got != want {
				t.Fatalf("request path = %q, want %q", got, want)
			}
			if tt.wantAccept != "" {
				if got := captured.headers.Get("Accept"); got != tt.wantAccept {
					t.Fatalf("Accept = %q, want %q", got, tt.wantAccept)
				}
			}

			// Cache-shard affinity headers, all derived from PromptCacheKey.
			for _, header := range []string{"session-id", "thread-id"} {
				if got := captured.headers.Get(header); got != promptCacheKey {
					t.Fatalf("%s = %q, want %q", header, got, promptCacheKey)
				}
			}
			if got, want := captured.headers.Get("originator"), "codex_cli_rs"; got != want {
				t.Fatalf("originator = %q, want %q", got, want)
			}

			// The body key must be the same key, not merely non-empty: a header and
			// a body hint that disagree do not warm the same shard.
			if got := captured.body["prompt_cache_key"]; got != promptCacheKey {
				t.Fatalf("body prompt_cache_key = %v, want %q", got, promptCacheKey)
			}
			if got, want := captured.body["store"], false; got != want {
				t.Fatalf("body store = %v, want %v on an OpenAI-operated host", got, want)
			}
			// The Codex backend rejects prompt_cache_retention with a 400 (issue #318).
			if _, present := captured.body["prompt_cache_retention"]; present {
				t.Fatal("body carries prompt_cache_retention, which the Codex backend rejects")
			}
		})
	}
}

// TestClientOpenAICompatSendsNoCodexAffinityHeaders pins the seam in the other
// direction: the affinity headers are a Codex Responses wire concern and must not
// leak into the OpenAI wire, whose backend does not expect them.
func TestClientOpenAICompatSendsNoCodexAffinityHeaders(t *testing.T) {
	var captured capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.headers = r.Header.Clone()
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	client, err := NewOpenAICompat(ClientConfig{
		BaseURL:   server.URL + "/v1",
		Model:     "gpt-4",
		Scheduler: mustTestScheduler(t, 1),
	})
	if err != nil {
		t.Fatalf("NewOpenAICompat() error = %v", err)
	}

	if _, err := client.ChatCompletion(context.Background(), ChatRequest{
		Messages:       []Message{{Role: MessageRoleUser, Content: "hi"}},
		PromptCacheKey: "session-123",
	}); err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}

	for _, header := range []string{"session-id", "thread-id", "originator"} {
		if got := captured.headers.Get(header); got != "" {
			t.Fatalf("%s = %q, want empty for the OpenAI wire", header, got)
		}
	}
}

func mustHostRewriteTransport(t *testing.T, target string) http.RoundTripper {
	t.Helper()
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	return &hostRewriteTransport{target: parsed}
}

func readStreamErrorRecords(t *testing.T, path string) []streamErrorRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stream error log: %v", err)
	}
	var records []streamErrorRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record streamErrorRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode stream error record %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

type recordingTokenCounter struct {
	observed atomic.Int32
}

func (c *recordingTokenCounter) EstimateChatRequestTokens(context.Context, ChatRequest) (TokenEstimate, error) {
	return TokenEstimate{}, nil
}

func (c *recordingTokenCounter) ObserveUsage(_ context.Context, _ ChatRequest, usage *UsageStats) {
	if usage == nil {
		return
	}
	c.observed.Add(1)
}
