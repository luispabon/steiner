package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/diagnostics"
)

// StreamErrorLogger writes stream error records as JSON lines to a dedicated file.
// Safe for concurrent use.
type StreamErrorLogger struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
	diag *diagnostics.Writer
}

type streamErrorRecord struct {
	Timestamp       time.Time
	Event           string // "stream_retry" | "stream_exhausted"
	Attempt         int
	Max             int
	Error           string
	StreamAlive     string // duration as string
	ChunksReceived  int
	ContentBytes    int
	PartialStream   bool
	RetryDelay      string
	RequestURL      string
	RequestHeaders  map[string]string // auth stripped
	RequestBody     json.RawMessage
	ResponseHeaders map[string]string
}

// NewStreamErrorLogger creates a StreamErrorLogger writing to path. Parent directories
// are created if needed. Returns nil without error when path is empty.
func NewStreamErrorLogger(path string) (*StreamErrorLogger, error) {
	return NewStreamErrorLoggerWithDiagnostics(path, nil)
}

// NewStreamErrorLoggerWithDiagnostics creates a StreamErrorLogger that writes
// through diag when one is supplied, and falls back to its own file at path
// when it is not. The diagnostics writer subsumes the file: no file is opened
// when diag is non-nil, so the records land in the provider stream instead of
// a log path derived from the sensitive --log-file. Returns nil without error
// when there is neither a writer nor a path.
func NewStreamErrorLoggerWithDiagnostics(path string, diag *diagnostics.Writer) (*StreamErrorLogger, error) {
	if diag != nil {
		return &StreamErrorLogger{diag: diag}, nil
	}
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create stream error log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open stream error log: %w", err)
	}
	return &StreamErrorLogger{
		file: f,
		enc:  json.NewEncoder(f),
	}, nil
}

// Log appends a stream error record to the log. No-op on nil receiver.
func (l *StreamErrorLogger) Log(r streamErrorRecord) {
	if l == nil {
		return
	}
	if l.diag != nil {
		l.diag.Write(diagnostics.Record{Kind: diagnostics.KindProvider, Payload: r})
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// Best-effort; stream error logging must not break execution.
	_ = l.enc.Encode(r)
}

// Close closes the underlying file. No-op on nil receiver.
func (l *StreamErrorLogger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// StreamErrorLogPath derives the stream error log path from the main session log path.
// Returns empty string when sessionLogPath is empty.
func StreamErrorLogPath(sessionLogPath string) string {
	sessionLogPath = strings.TrimSpace(sessionLogPath)
	if sessionLogPath == "" {
		return ""
	}
	ext := filepath.Ext(sessionLogPath)
	base := strings.TrimSuffix(sessionLogPath, ext)
	if ext == "" {
		ext = ".log"
	}
	return base + "-stream-errors" + ext
}
