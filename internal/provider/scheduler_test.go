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

func TestSchedulerBlocksBeyondParallelism(t *testing.T) {
	sched, err := NewScheduler(1)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}

	if err := sched.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer sched.Release()

	acquired := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		err := sched.Acquire(context.Background())
		if err == nil {
			close(acquired)
		}
		errCh <- err
	}()

	select {
	case <-acquired:
		t.Fatal("second acquire succeeded before release")
	case <-time.After(50 * time.Millisecond):
	}

	sched.Release()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Acquire() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second acquire did not complete after release")
	}
}

func TestSchedulerAcquireHonorsContextCancellation(t *testing.T) {
	sched, err := NewScheduler(1)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}

	if err := sched.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer sched.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	err = sched.Acquire(ctx)
	if err == nil {
		t.Fatal("Acquire() error = nil, want cancellation error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire() error = %v, want context cancellation", err)
	}
}

func TestOpenAICompatChatCompletionNormalizesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Method, http.MethodPost; got != want {
			t.Fatalf("method = %s, want %s", got, want)
		}
		if got, want := r.URL.Path, "/v1/chat/completions"; got != want {
			t.Fatalf("path = %s, want %s", got, want)
		}
		if got, want := r.Header.Get("Content-Type"), "application/json"; got != want {
			t.Fatalf("content type = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), ""; got != want {
			t.Fatalf("authorization = %q, want empty", got)
		}
		_, _ = fmt.Fprint(w, `{
			"choices":[
				{
					"message":{
						"role":"assistant",
						"content":"final text",
						"tool_calls":[
							{
								"id":"call_1",
								"type":"function",
								"function":{"name":"lookup","arguments":"{\"query\":\"x\"}"}
							}
						]
					},
					"finish_reason":"stop"
				}
			],
			"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}
		}`)
	}))
	defer server.Close()

	provider := mustTestOpenAICompat(t, server.URL)

	resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got, want := resp.Message.Role, MessageRoleAssistant; got != want {
		t.Fatalf("role = %s, want %s", got, want)
	}
	if got, want := resp.Message.Content, "final text"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if got, want := resp.FinishReason, "stop"; got != want {
		t.Fatalf("finish reason = %q, want %q", got, want)
	}
	if resp.Usage == nil {
		t.Fatal("usage = nil, want usage")
	}
	if got, want := resp.Usage.TotalTokens, 8; got != want {
		t.Fatalf("total tokens = %d, want %d", got, want)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d, want 1", len(resp.Message.ToolCalls))
	}
	if got, want := resp.Message.ToolCalls[0].Name, "lookup"; got != want {
		t.Fatalf("tool call name = %q, want %q", got, want)
	}
	if got, want := resp.Message.ToolCalls[0].Arguments["query"], "x"; got != want {
		t.Fatalf("tool call query = %#v, want %#v", got, want)
	}
}

func TestOpenAICompatStreamChatCompletionNormalizesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v1/chat/completions"; got != want {
			t.Fatalf("path = %s, want %s", got, want)
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not implement Flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"role":"assistant"}}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"hello "}}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"query\":"}}]}}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]},"finish_reason":"tool_calls"}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: %s\n\n", "[DONE]")
		flusher.Flush()
	}))
	defer server.Close()

	provider := mustTestOpenAICompat(t, server.URL)

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	first, ok := <-ch
	if !ok {
		t.Fatal("stream closed before content chunk")
	}
	if got, want := first.Delta.Role, MessageRoleAssistant; got != want {
		t.Fatalf("first chunk role = %s, want %s", got, want)
	}
	if got, want := first.Delta.Content, "hello "; got != want {
		t.Fatalf("first chunk content = %q, want %q", got, want)
	}

	second, ok := <-ch
	if !ok {
		t.Fatal("stream closed before final chunk")
	}
	if !second.Done {
		t.Fatal("final chunk Done = false, want true")
	}
	if got, want := second.FinishReason, "tool_calls"; got != want {
		t.Fatalf("finish reason = %q, want %q", got, want)
	}
	if second.Usage == nil {
		t.Fatal("usage = nil, want usage")
	}
	if got, want := second.Usage.TotalTokens, 6; got != want {
		t.Fatalf("total tokens = %d, want %d", got, want)
	}
	if len(second.Delta.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d, want 1", len(second.Delta.ToolCalls))
	}
	if got, want := second.Delta.ToolCalls[0].Name, "lookup"; got != want {
		t.Fatalf("tool call name = %q, want %q", got, want)
	}
	if got, want := second.Delta.ToolCalls[0].Arguments["query"], "x"; got != want {
		t.Fatalf("tool call query = %#v, want %#v", got, want)
	}
	if _, ok := <-ch; ok {
		t.Fatal("stream produced unexpected extra chunk")
	}
}

func TestOpenAICompatStreamChatCompletionReportsStreamErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, "bad upstream")
	}))
	defer server.Close()

	provider := mustTestOpenAICompat(t, server.URL)

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v, want channel", err)
	}

	chunk, ok := <-ch
	if !ok {
		t.Fatal("stream closed before error chunk")
	}
	if !chunk.Done {
		t.Fatal("error chunk Done = false, want true")
	}
	if chunk.Error == "" {
		t.Fatal("error chunk Error = empty, want upstream error")
	}
	if _, ok := <-ch; ok {
		t.Fatal("stream produced unexpected extra chunk after error")
	}
}

func TestOpenAICompatSchedulerSerializesRequests(t *testing.T) {
	var started int32
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondEntered := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&started, 1) {
		case 1:
			close(firstStarted)
			select {
			case <-firstRelease:
			case <-r.Context().Done():
				t.Fatalf("first request canceled: %v", r.Context().Err())
			}
		case 2:
			close(secondEntered)
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	provider := mustTestOpenAICompat(t, server.URL)

	firstErr := make(chan error, 1)
	go func() {
		_, err := provider.ChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "one"}},
		})
		firstErr <- err
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first request did not reach server")
	}

	secondErr := make(chan error, 1)
	go func() {
		_, err := provider.ChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "two"}},
		})
		secondErr <- err
	}()

	select {
	case <-secondEntered:
		t.Fatal("second request reached server before first completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(firstRelease)

	if err := <-firstErr; err != nil {
		t.Fatalf("first ChatCompletion() error = %v", err)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("second ChatCompletion() error = %v", err)
	}
}

func mustTestOpenAICompat(t *testing.T, baseURL string) *Client {
	t.Helper()
	provider, err := NewOpenAICompat(ClientConfig{
		BaseURL:   baseURL + "/v1",
		Model:     "test-model",
		Scheduler: mustTestScheduler(t, 1),
	})
	if err != nil {
		t.Fatalf("NewOpenAICompat() error = %v", err)
	}
	return provider
}

func mustTestScheduler(t *testing.T, parallelism int) *Scheduler {
	t.Helper()
	sched, err := NewScheduler(parallelism)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	return sched
}

func TestSchedulerEnforcesConcurrentLimit(t *testing.T) {
	const (
		parallelism = 2
		goroutines  = 4
	)

	scheduler := mustTestScheduler(t, parallelism)

	var (
		mu            sync.Mutex
		concurrent    int
		maxConcurrent int
		wg            sync.WaitGroup
		ctx           = context.Background()
	)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			err := scheduler.Acquire(ctx)
			if err != nil {
				t.Errorf("Acquire failed: %v", err)
				return
			}
			defer scheduler.Release()

			// Track concurrent access
			mu.Lock()
			concurrent++
			if concurrent > maxConcurrent {
				maxConcurrent = concurrent
			}
			mu.Unlock()

			// Hold the critical section briefly
			time.Sleep(10 * time.Millisecond)

			// Decrement on exit
			mu.Lock()
			concurrent--
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Assert that max concurrent never exceeded parallelism
	if maxConcurrent > parallelism {
		t.Errorf("max concurrent (%d) exceeded parallelism limit (%d)", maxConcurrent, parallelism)
	}
}
