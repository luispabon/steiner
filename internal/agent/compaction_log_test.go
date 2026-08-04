package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestCompactionLogger(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "compaction.log")

	logger, err := NewCompactionLogger(logPath)
	if err != nil {
		t.Fatalf("NewCompactionLogger: %v", err)
	}
	defer func() {
		_ = logger.Close()
	}()

	maxTokens := 4096
	req := provider.ChatRequest{
		Model:     "gpt-4",
		MaxTokens: &maxTokens,
		Messages: []provider.Message{
			{Role: provider.MessageRoleSystem, Content: "You are a helpful assistant."},
			{Role: provider.MessageRoleUser, Content: "Summarize this conversation."},
			{Role: provider.MessageRoleAssistant, Content: "Here is the summary."},
		},
	}

	if err := logger.LogRequest(req); err != nil {
		t.Fatalf("LogRequest: %v", err)
	}

	usage := &provider.UsageStats{PromptTokens: 150, CompletionTokens: 50, TotalTokens: 200}
	resp := provider.ChatResponse{
		FinishReason: "stop",
		Usage:        usage,
		Message:      provider.Message{Content: "This is the compaction response content."},
	}

	if err := logger.LogResponse(resp); err != nil {
		t.Fatalf("LogResponse: %v", err)
	}

	_ = logger.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)

	t.Logf("Log content:\n%s", content)

	// Verify request section
	if !strings.Contains(content, "=== Compaction Request ===") {
		t.Error("missing request header")
	}
	if !strings.Contains(content, "Model: gpt-4") {
		t.Error("missing model")
	}
	if !strings.Contains(content, "MaxTokens: 4096") {
		t.Error("missing max tokens")
	}
	if !strings.Contains(content, "--- system ---") {
		t.Error("missing system message role")
	}
	if !strings.Contains(content, "--- user ---") {
		t.Error("missing user message role")
	}
	if !strings.Contains(content, "--- assistant ---") {
		t.Error("missing assistant message role")
	}

	// Verify response section
	if !strings.Contains(content, "=== Compaction Response ===") {
		t.Error("missing response header")
	}
	if !strings.Contains(content, "FinishReason: stop") {
		t.Error("missing finish reason")
	}
	if !strings.Contains(content, `{"prompt_tokens":150,"completion_tokens":50,"total_tokens":200}`) {
		t.Error("missing or malformed usage stats")
	}
	if !strings.Contains(content, "Content:\nThis is the compaction response content.") {
		t.Error("missing response content")
	}

	// Verify timestamp presence (both request and response)
	timestampCount := strings.Count(content, "Timestamp: ")
	if timestampCount != 2 {
		t.Errorf("expected 2 timestamps, got %d", timestampCount)
	}
}

func TestCompactionLogger_NilMaxTokens(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "compaction.log")

	logger, err := NewCompactionLogger(logPath)
	if err != nil {
		t.Fatalf("NewCompactionLogger: %v", err)
	}
	defer func() {
		_ = logger.Close()
	}()

	req := provider.ChatRequest{
		Model: "gpt-4",
		Messages: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "Hello"},
		},
	}

	if err := logger.LogRequest(req); err != nil {
		t.Fatalf("LogRequest: %v", err)
	}

	_ = logger.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if !strings.Contains(string(data), "MaxTokens: unset") {
		t.Error("expected MaxTokens: unset for nil MaxTokens")
	}
}

func TestCompactionLogger_NilUsage(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "compaction.log")

	logger, err := NewCompactionLogger(logPath)
	if err != nil {
		t.Fatalf("NewCompactionLogger: %v", err)
	}
	defer func() {
		_ = logger.Close()
	}()

	resp := provider.ChatResponse{
		FinishReason: "length",
		Usage:        nil,
		Message:      provider.Message{Content: "Response content."},
	}

	if err := logger.LogResponse(resp); err != nil {
		t.Fatalf("LogResponse: %v", err)
	}

	_ = logger.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if !strings.Contains(string(data), "Usage: null") {
		t.Error("expected Usage: null for nil Usage")
	}
}

func TestCompactionLogger_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sub", "nested", "compaction.log")

	logger, err := NewCompactionLogger(logPath)
	if err != nil {
		t.Fatalf("NewCompactionLogger with nested dirs: %v", err)
	}
	defer func() {
		_ = logger.Close()
	}()

	if err := logger.LogRequest(provider.ChatRequest{Model: "test", Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "hi"}}}); err != nil {
		t.Fatalf("LogRequest: %v", err)
	}

	_ = logger.Close()

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("log file was not created in nested directory")
	}
}

func TestCompactionLogger_ConcurrentSafety(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "compaction.log")

	logger, err := NewCompactionLogger(logPath)
	if err != nil {
		t.Fatalf("NewCompactionLogger: %v", err)
	}
	defer func() {
		_ = logger.Close()
	}()

	done := make(chan struct{})
	const goroutines = 10

	for i := 0; i < goroutines; i++ {
		go func() {
			req := provider.ChatRequest{
				Model:    fmt.Sprintf("model-%d", i),
				Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: fmt.Sprintf("request-body-%d", i)}},
			}
			logger.LogRequest(req) //nolint: errcheck
			resp := provider.ChatResponse{
				FinishReason: fmt.Sprintf("stop-%d", i),
				Message:      provider.Message{Content: fmt.Sprintf("response-body-%d", i)},
			}
			logger.LogResponse(resp) //nolint: errcheck
			done <- struct{}{}
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	_ = logger.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)
	if strings.Count(content, "=== Compaction Request ===") != goroutines {
		t.Errorf("expected %d request headers, got %d", goroutines, strings.Count(content, "=== Compaction Request ==="))
	}
	if strings.Count(content, "=== Compaction Response ===") != goroutines {
		t.Errorf("expected %d response headers, got %d", goroutines, strings.Count(content, "=== Compaction Response ==="))
	}

	// Headers alone cannot detect interleaved writes: assert each goroutine's
	// body survived contiguously and exactly once.
	for i := 0; i < goroutines; i++ {
		reqBody := fmt.Sprintf("Model: model-%d\nMaxTokens: unset\nMessages:\n--- user ---\nrequest-body-%d\n\n", i, i)
		if got := strings.Count(content, reqBody); got != 1 {
			t.Errorf("request body for goroutine %d appears %d times, want 1", i, got)
		}
		respBody := fmt.Sprintf("FinishReason: stop-%d\nUsage: null\nContent:\nresponse-body-%d\n\n", i, i)
		if got := strings.Count(content, respBody); got != 1 {
			t.Errorf("response body for goroutine %d appears %d times, want 1", i, got)
		}
	}
}
