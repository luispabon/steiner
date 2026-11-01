package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestClientChatCompletionRecordsCallDiagnostics proves a successful
// non-streaming call reports the transport attempts it spent.
func TestClientChatCompletionRecordsCallDiagnostics(t *testing.T) {
	tests := []struct {
		name         string
		statuses     []int
		wantAttempts int
	}{
		{name: "single attempt", statuses: []int{http.StatusOK}, wantAttempts: 1},
		{name: "retried", statuses: []int{http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK}, wantAttempts: 3},
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
				w.WriteHeader(tt.statuses[n-1])
			}))
			defer server.Close()

			client, _ := newFakeWireClient(t, &fakeWire{target: server.URL}, RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: time.Millisecond,
			})
			response, err := client.ChatCompletion(context.Background(), ChatRequest{
				Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
			})
			if err != nil {
				t.Fatalf("ChatCompletion() error = %v", err)
			}
			if response.Diagnostics == nil {
				t.Fatal("Diagnostics = nil, want the successful call's attempts")
			}
			if got := response.Diagnostics.Attempts; got != tt.wantAttempts {
				t.Fatalf("Attempts = %d, want %d", got, tt.wantAttempts)
			}
			if response.Diagnostics.CacheAffinityDropped {
				t.Error("CacheAffinityDropped = true, want false without a cache-retention rejection")
			}
		})
	}
}

// TestClientStreamChatCompletionRecordsCallDiagnostics proves the terminal chunk
// of a retried stream carries the attempts that produced it.
func TestClientStreamChatCompletionRecordsCallDiagnostics(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
	}))
	defer server.Close()

	wire := &fakeWire{target: server.URL}
	wire.decodeStream = func(_ context.Context, _ io.Reader, emit func(ChatChunk) error) error {
		if attempts.Load() == 1 {
			if err := emit(ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: "par"}}); err != nil {
				return err
			}
			return io.ErrUnexpectedEOF
		}
		return emit(ChatChunk{Done: true, FinishReason: "stop"})
	}
	client, _ := newFakeWireClient(t, wire, RetryConfig{
		Enabled:        true,
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
	})

	chunks, err := client.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}
	var terminal *CallDiagnostics
	for chunk := range chunks {
		if chunk.Done {
			terminal = chunk.Diagnostics
		}
	}
	if terminal == nil {
		t.Fatal("terminal chunk has no diagnostics")
	}
	if got := terminal.Attempts; got != 2 {
		t.Fatalf("Attempts = %d, want 2 (one failed attempt, one success)", got)
	}
	if terminal.CacheAffinityDropped {
		t.Error("CacheAffinityDropped = true, want false without a cache-retention rejection")
	}
}
