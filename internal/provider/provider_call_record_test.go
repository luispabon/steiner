package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/luispabon/steiner/internal/diagnostics"
)

// TestClientChatCompletionSuccessEmitsOkOutcome pins the denominator: without
// it, only failures would ever be recorded and no rate could be derived
// (issue #707).
func TestClientChatCompletionSuccessEmitsOkOutcome(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logPath := filepath.Join(t.TempDir(), "stream-errors.log")
	streamErrorLog, err := NewStreamErrorLogger(logPath)
	if err != nil {
		t.Fatalf("NewStreamErrorLogger() error = %v", err)
	}
	defer func() { _ = streamErrorLog.Close() }()

	wire := &fakeWire{target: server.URL}
	client, _ := newFakeWireClient(t, wire, RetryConfig{})
	client.streamErrorLog = streamErrorLog

	if _, err := client.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}

	records := readStreamErrorRecords(t, logPath)
	if len(records) != 1 {
		t.Fatalf("stream error log records = %d, want 1", len(records))
	}
	if got, want := records[0].Outcome, "ok"; got != want {
		t.Errorf("outcome = %q, want %q", got, want)
	}
	if got, want := records[0].Attempts, 1; got != want {
		t.Errorf("attempts = %d, want %d", got, want)
	}
	if records[0].Error != "" {
		t.Errorf("error = %q, want empty on success", records[0].Error)
	}
}

// TestClientChatCompletionEmitsOnePerOutcome table-drives the four outcome
// values: exactly one record per call, whatever the outcome, is the invariant
// this whole stage exists to establish (issue #707).
func TestClientChatCompletionEmitsOnePerOutcome(t *testing.T) {
	tests := []struct {
		name         string
		serverStatus int
		cancelled    bool
		wantOutcome  string
		wantAttempts int
	}{
		{
			name:         "exhausted",
			serverStatus: http.StatusServiceUnavailable,
			wantOutcome:  "exhausted",
			wantAttempts: 1,
		},
		{
			name:        "cancelled",
			cancelled:   true,
			wantOutcome: "cancelled",
			// executeRequest never reaches the server for a pre-cancelled
			// context, so the retry loop stops at the first attempt.
			wantAttempts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.serverStatus)
			}))
			defer server.Close()

			logPath := filepath.Join(t.TempDir(), "stream-errors.log")
			streamErrorLog, err := NewStreamErrorLogger(logPath)
			if err != nil {
				t.Fatalf("NewStreamErrorLogger() error = %v", err)
			}
			defer func() { _ = streamErrorLog.Close() }()

			wire := &fakeWire{target: server.URL}
			client, _ := newFakeWireClient(t, wire, RetryConfig{})
			client.streamErrorLog = streamErrorLog

			ctx := context.Background()
			if tt.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			if _, err := client.ChatCompletion(ctx, ChatRequest{
				Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
			}); err == nil {
				t.Fatal("ChatCompletion() error = nil, want failure")
			}

			records := readStreamErrorRecords(t, logPath)
			if len(records) != 1 {
				t.Fatalf("stream error log records = %d, want 1", len(records))
			}
			if got := records[0].Outcome; got != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", got, tt.wantOutcome)
			}
			if got := records[0].Attempts; got != tt.wantAttempts {
				t.Errorf("attempts = %d, want %d", got, tt.wantAttempts)
			}
		})
	}
}

func TestClientEarlyProviderFailuresEmitOneRecord(t *testing.T) {
	tests := []struct {
		name       string
		stream     bool
		payloadErr bool
		paceErr    bool
	}{
		{name: "chat payload", payloadErr: true},
		{name: "stream payload", stream: true, payloadErr: true},
		{name: "chat pacing", paceErr: true},
		{name: "stream pacing", stream: true, paceErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "stream-errors.log")
			streamErrorLog, err := NewStreamErrorLogger(logPath)
			if err != nil {
				t.Fatalf("NewStreamErrorLogger() error = %v", err)
			}
			defer func() { _ = streamErrorLog.Close() }()

			wire := &fakeWire{}
			if tt.payloadErr {
				wire.payloadErr = errors.New("payload construction failed")
			}
			client, _ := newFakeWireClient(t, wire, RetryConfig{})
			client.streamErrorLog = streamErrorLog

			ctx := context.Background()
			if tt.paceErr {
				client.minInterval = time.Hour
				if err := client.pace(ctx); err != nil {
					t.Fatalf("prime pace() error = %v", err)
				}
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			request := ChatRequest{Messages: []Message{{Role: MessageRoleUser, Content: "hi"}}}
			if tt.stream {
				ch, err := client.StreamChatCompletion(ctx, request)
				if tt.paceErr {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("StreamChatCompletion() error = %v, want context.Canceled", err)
					}
				} else {
					if err != nil {
						t.Fatalf("StreamChatCompletion() error = %v", err)
					}
					for range ch {
					}
				}
			} else {
				if _, err := client.ChatCompletion(ctx, request); err == nil {
					t.Fatal("ChatCompletion() error = nil, want failure")
				}
			}

			records := readStreamErrorRecords(t, logPath)
			if len(records) != 1 {
				t.Fatalf("stream error log records = %d, want 1", len(records))
			}
			if got, want := records[0].Attempts, 0; got != want {
				t.Errorf("attempts = %d, want %d before the first attempt", got, want)
			}
			if records[0].Error == "" {
				t.Error("error is empty, want the bounded early failure")
			}
		})
	}
}

// TestClientCaptureBodiesOffOmitsRequestBody covers 3.3: with the gate off,
// no request_body key appears in the emitted record at all.
func TestClientCaptureBodiesOffOmitsRequestBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	diagDir := t.TempDir()
	diag, err := diagnostics.New(diagnostics.Options{
		Dir:           diagDir,
		Streams:       diagnostics.Streams{Provider: true},
		CaptureBodies: false,
	})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = diag.Close() })

	streamErrorLog, err := NewStreamErrorLoggerWithDiagnostics("", diag)
	if err != nil {
		t.Fatalf("NewStreamErrorLoggerWithDiagnostics() error = %v", err)
	}

	wire := &fakeWire{target: server.URL}
	client, _ := newFakeWireClient(t, wire, RetryConfig{})
	client.streamErrorLog = streamErrorLog

	if _, err := client.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(diagDir, "provider.jsonl"))
	if err != nil {
		t.Fatalf("read provider.jsonl: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw["payload"], &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if _, ok := payload["request_body"]; ok {
		t.Error("payload has a request_body key, want none with capture_bodies: false")
	}
	if _, ok := payload["request_headers"]; ok {
		t.Error("payload has a request_headers key, want none with capture_bodies: false")
	}
}

// TestClientStreamTTFTLessThanDuration covers 3.4: on a streamed response
// that takes measurably longer after the first chunk, ttft_ms is populated
// and smaller than duration_ms.
func TestClientStreamTTFTLessThanDuration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()

	logPath := filepath.Join(t.TempDir(), "stream-errors.log")
	streamErrorLog, err := NewStreamErrorLogger(logPath)
	if err != nil {
		t.Fatalf("NewStreamErrorLogger() error = %v", err)
	}
	defer func() { _ = streamErrorLog.Close() }()

	wire := &fakeWire{target: server.URL}
	wire.decodeStream = func(_ context.Context, _ io.Reader, emit func(ChatChunk) error) error {
		time.Sleep(5 * time.Millisecond)
		if err := emit(ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: "first"}}); err != nil {
			return err
		}
		time.Sleep(40 * time.Millisecond)
		return emit(ChatChunk{Done: true, FinishReason: "stop"})
	}

	client, _ := newFakeWireClient(t, wire, RetryConfig{})
	client.streamErrorLog = streamErrorLog

	ch, err := client.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}
	for range ch {
	}

	records := readStreamErrorRecords(t, logPath)
	if len(records) != 1 {
		t.Fatalf("stream error log records = %d, want 1", len(records))
	}
	if records[0].TTFTMillis <= 0 {
		t.Fatalf("ttft_ms = %d, want > 0", records[0].TTFTMillis)
	}
	if records[0].TTFTMillis >= records[0].DurationMillis {
		t.Fatalf("ttft_ms = %d, duration_ms = %d, want ttft < duration", records[0].TTFTMillis, records[0].DurationMillis)
	}
}

// TestCodexWSChatCompletionEmitsProviderCall covers 3.2 for the WebSocket
// transport: one record per call, transport "ws", outcome "ok".
func TestCodexWSChatCompletionEmitsProviderCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
		_ = conn.Write(ctx, websocket.MessageText, mustJSON(t, map[string]any{
			"type":     "response.completed",
			"response": map[string]any{"output": []any{}, "status": "success"},
		}))
	}))
	defer server.Close()

	logPath := filepath.Join(t.TempDir(), "stream-errors.log")
	streamErrorLog, err := NewStreamErrorLogger(logPath)
	if err != nil {
		t.Fatalf("NewStreamErrorLogger() error = %v", err)
	}
	defer func() { _ = streamErrorLog.Close() }()

	cfg := ClientConfig{
		BaseURL:        "http://127.0.0.1:1",
		APIKey:         "test-key",
		Model:          "test-model",
		ProviderType:   "codex",
		HTTPClient:     &http.Client{Timeout: time.Second},
		StreamErrorLog: streamErrorLog,
		Retry: RetryConfig{
			Enabled:        false,
			MaxAttempts:    1,
			InitialBackoff: time.Millisecond,
			MaxBackoff:     time.Millisecond,
		},
	}
	created, err := NewCodexResponsesWS(cfg)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	wsProvider := created.(*codexWSProvider)
	wsProvider.wsURL = "ws" + server.URL[4:]

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := wsProvider.ChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}

	records := readStreamErrorRecords(t, logPath)
	if len(records) != 1 {
		t.Fatalf("stream error log records = %d, want 1", len(records))
	}
	if got, want := records[0].Transport, "ws"; got != want {
		t.Errorf("transport = %q, want %q", got, want)
	}
	if got, want := records[0].Outcome, "ok"; got != want {
		t.Errorf("outcome = %q, want %q", got, want)
	}
	if got, want := records[0].Attempts, 1; got != want {
		t.Errorf("attempts = %d, want %d", got, want)
	}
}
