package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

// TestRuntimeStreamErrorLoggerReachesProvider covers the composition root's half
// of stream-error logging: internal/provider proves the client writes a
// stream_retry record when it holds a logger, but nothing proved that
// cmd/steiner ever hands it one. This drives a real Anthropic-wire provider,
// built the way the runtime builds it, through a stream retry and asserts the
// record lands on disk at the path derived from the session log.
func TestRuntimeStreamErrorLoggerReachesProvider(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "busy")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, strings.Join([]string{
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"type":"message_delta","stop_reason":"end_turn"}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))
	}))
	defer server.Close()

	sessionLog := filepath.Join(t.TempDir(), "session.log")
	flags := &cliFlags{logFile: sessionLog}
	cfg := config.Config{}

	streamErrorLog, err := buildStreamErrorLogger(cfg, flags)
	if err != nil {
		t.Fatalf("buildStreamErrorLogger() error = %v", err)
	}
	if streamErrorLog == nil {
		t.Fatal("buildStreamErrorLogger() = nil, want a logger when a session log is configured")
	}
	defer func() {
		_ = streamErrorLog.Close()
	}()

	factory := buildRuntimeProviderFactory(cfg, &http.Client{}, streamErrorLog)

	p, err := factory(provider.ResolvedModel{
		Alias:                 "anthropic",
		ProviderConfig:        config.ProviderConfig{Type: config.ProviderTypeAnthropic, BaseURL: server.URL + "/v1", APIKey: "sk-ant-test"},
		BackendModelID:        "claude-3-7-sonnet",
		EffectiveProviderType: config.ProviderTypeAnthropic,
		Retry: config.RetryConfig{
			Enabled:        true,
			MaxAttempts:    2,
			InitialBackoff: config.MustDuration("1ms"),
			MaxBackoff:     config.MustDuration("1ms"),
		},
	}, "test-session")
	if err != nil {
		t.Fatalf("factory() error = %v", err)
	}

	ch, err := p.StreamChatCompletion(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "hi"}},
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

	logPath := provider.StreamErrorLogPath(sessionLog)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read stream error log %q: %v", logPath, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("stream error log lines = %d, want 1", len(lines))
	}

	var record struct {
		Event   string
		Attempt int
		Error   string
	}
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("decode stream error record %q: %v", lines[0], err)
	}
	if got, want := record.Event, "stream_retry"; got != want {
		t.Fatalf("event = %q, want %q", got, want)
	}
	if got, want := record.Attempt, 2; got != want {
		t.Fatalf("attempt = %d, want %d", got, want)
	}
	if record.Error == "" {
		t.Fatal("record error = empty, want the 503 reason")
	}
}
