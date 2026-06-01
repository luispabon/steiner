package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/provider"
)

// CompactionLogger writes compaction request/response pairs to a dedicated log file.
type CompactionLogger struct {
	mu   sync.Mutex
	file *os.File
}

// NewCompactionLogger creates a logger at the given path. Creates parent dirs if needed.
func NewCompactionLogger(path string) (*CompactionLogger, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create compaction log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open compaction log file: %w", err)
	}
	return &CompactionLogger{file: f}, nil
}

// LogRequest writes the full compaction API request (model, max tokens, every message verbatim).
func (l *CompactionLogger) LogRequest(req provider.ChatRequest) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "=== Compaction Request ===\n")
	fmt.Fprintf(&b, "Timestamp: %s\n", time.Now().Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "Model: %s\n", req.Model)
	if req.MaxTokens != nil {
		fmt.Fprintf(&b, "MaxTokens: %d\n", *req.MaxTokens)
	} else {
		b.WriteString("MaxTokens: unset\n")
	}
	b.WriteString("Messages:\n")
	for _, msg := range req.Messages {
		fmt.Fprintf(&b, "--- %s ---\n", msg.Role)
		b.WriteString(msg.Content + "\n")
	}
	b.WriteString("\n")

	_, err := l.file.WriteString(b.String())
	return err
}

// LogResponse writes the final response (finish reason, usage, full content text).
func (l *CompactionLogger) LogResponse(resp provider.ChatResponse) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "=== Compaction Response ===\n")
	fmt.Fprintf(&b, "Timestamp: %s\n", time.Now().Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "FinishReason: %s\n", resp.FinishReason)
	if resp.Usage != nil {
		usageJSON, err := json.Marshal(resp.Usage)
		if err != nil {
			return fmt.Errorf("marshal usage stats: %w", err)
		}
		fmt.Fprintf(&b, "Usage: %s\n", string(usageJSON))
	} else {
		b.WriteString("Usage: null\n")
	}
	b.WriteString("Content:\n")
	b.WriteString(resp.Message.Content + "\n")
	b.WriteString("\n")

	_, err := l.file.WriteString(b.String())
	return err
}

// Close closes the underlying file.
func (l *CompactionLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}
