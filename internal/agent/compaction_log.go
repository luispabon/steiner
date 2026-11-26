package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/output"
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

// LogRequest writes the full compaction API request (attempt, model, max tokens, every message verbatim).
func (l *CompactionLogger) LogRequest(req provider.ChatRequest, attempt int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "=== Compaction Request ===\n")
	fmt.Fprintf(&b, "Timestamp: %s\n", time.Now().Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "Attempt: %d\n", attempt)
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

// LogSizes writes one line with the token sizes of a compaction stage. It holds
// counts only, never prompt or summary text.
func (l *CompactionLogger) LogSizes(stage output.ContextCompactionStage) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	_, err := fmt.Fprintf(l.file, "Stage Sizes: stage=%s retained_units=%d source_tokens=%d summary_tokens=%d retained_tokens=%d protected_tokens=%d\n",
		stage.Stage, stage.RetainedUnits, stage.SourceTokens, stage.SummaryTokens, stage.RetainedTokens, stage.ProtectedTokens)
	return err
}

// LogSegmentDecision writes one line with the append, rebase or plain fallback
// decision of a segmented compaction. It holds counts only, never prompt or
// summary text.
func (l *CompactionLogger) LogSegmentDecision(d segmentDecision) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	action := "append"
	switch {
	case d.Plain:
		action = "plain"
	case d.Rebase:
		action = "rebase"
	}
	_, err := fmt.Fprintf(l.file, "segment_decision action=%s reason=%s sequence=%d chain_tokens=%d projected_post_tokens=%d budget=%d\n",
		action, d.Reason, d.Sequence, d.ChainTokens, d.ProjectedPostTokens, d.Budget)
	return err
}

// withCompactionLogger opens the compaction log at path and runs fn with it. An
// empty path skips fn. Open, write and close failures warn and are otherwise
// ignored, because the log is best-effort: label names the write in the warning.
func withCompactionLogger(path, label string, fn func(*CompactionLogger) error) {
	if path == "" {
		return
	}
	logger, err := NewCompactionLogger(path)
	if err != nil {
		slog.Warn("compaction logger unavailable", "path", path, "error", err)
		return
	}
	defer func() {
		if err := logger.Close(); err != nil {
			slog.Warn("compaction logger close failed", "path", path, "error", err)
		}
	}()
	if err := fn(logger); err != nil {
		slog.Warn("compaction "+label+" log failed", "path", path, "error", err)
	}
}

// logSegmentDecisionFile appends one adopted segmented compaction decision to
// the compaction log at path. An empty path or no decision logs nothing.
func logSegmentDecisionFile(path string, decision *segmentDecision) {
	if decision == nil {
		return
	}
	withCompactionLogger(path, "segment decision", func(logger *CompactionLogger) error {
		return logger.LogSegmentDecision(*decision)
	})
}

// logCompactionStageSizes appends the sizes of an applied stage to the compaction
// log at path. An empty path logs nothing.
func logCompactionStageSizes(path string, stage output.ContextCompactionStage) {
	withCompactionLogger(path, "sizes", func(logger *CompactionLogger) error {
		return logger.LogSizes(stage)
	})
}

// Close closes the underlying file.
func (l *CompactionLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}
