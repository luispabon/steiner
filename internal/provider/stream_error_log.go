package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/diagnostics"
)

// StreamErrorLogger writes provider-stream records as JSON lines to a
// dedicated file. Safe for concurrent use.
type StreamErrorLogger struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
	diag *diagnostics.Writer
}

// streamErrorRecord is one provider-stream diagnostics record. One is emitted
// per model call, regardless of outcome (issue #707): a failure-only record
// gives a numerator with no denominator.
type streamErrorRecord struct {
	Timestamp      time.Time `json:"ts"`
	Provider       string    `json:"provider,omitempty"`
	Model          string    `json:"model,omitempty"`
	Transport      string    `json:"transport,omitempty"` // "ws" | "http" | "sse"
	Outcome        string    `json:"outcome"`             // "ok" | "retried" | "exhausted" | "cancelled"
	Attempts       int       `json:"attempts"`
	TTFTMillis     int       `json:"ttft_ms,omitempty"`
	DurationMillis int       `json:"duration_ms"`
	ErrorClass     string    `json:"error_class,omitempty"`
	Error          string    `json:"error,omitempty"`
	PartialStream  bool      `json:"partial_stream,omitempty"`
	Chunks         int       `json:"chunks,omitempty"`

	RequestURL      string            `json:"request_url,omitempty"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"` // auth stripped; capture_bodies-gated
	RequestBody     json.RawMessage   `json:"request_body,omitempty"`    // capture_bodies-gated
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
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
	dir := filepath.Dir(path)
	dirExisted := false
	if _, statErr := os.Stat(dir); statErr == nil {
		dirExisted = true
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("stat stream error log directory: %w", statErr)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create stream error log directory: %w", err)
	}
	if !dirExisted {
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, fmt.Errorf("secure stream error log directory: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open stream error log: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("secure stream error log: %w", err)
	}
	return &StreamErrorLogger{
		file: f,
		enc:  json.NewEncoder(f),
	}, nil
}

// Log appends a provider-stream record to the log. No-op on nil receiver.
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

// captureBodies reports whether records may carry request/response bodies and
// headers. Default false everywhere per issue #707's capture_bodies gate: the
// standalone file predates the gate but must not regress into logging every
// prompt to a 0o600 file on every model call, which is exactly the unbounded
// content the diagnostics plan exists to prevent. False on a nil receiver.
func (l *StreamErrorLogger) captureBodies() bool {
	if l == nil {
		return false
	}
	if l.diag != nil {
		return l.diag.CaptureBodies()
	}
	return false
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

// providerCallInput captures what one model call observed, independent of
// transport (HTTP client or WebSocket provider).
type providerCallInput struct {
	log       *StreamErrorLogger
	provider  string
	model     string
	transport string // "ws" | "http" | "sse"

	start time.Time
	ttft  time.Duration

	attempts      int
	chunks        int
	partialStream bool

	err error
	ctx context.Context

	requestURL      string
	requestHeaders  map[string]string
	requestBody     []byte
	responseHeaders http.Header
}

// emitProviderCall writes exactly one provider-stream diagnostics record for
// one model call, whatever the outcome — the numerator/denominator pairing
// the unified-diagnostics plan depends on (issue #707). No-op when in.log is
// nil (diagnostics disabled and no --log-file derived path configured).
func emitProviderCall(in providerCallInput) {
	if in.log == nil {
		return
	}

	attempts := in.attempts

	outcome := "ok"
	var errClass, errText string
	if in.err != nil {
		errText = boundedError(in.err)
		class := classifyErrorClass(in.ctx, in.err)
		errClass = string(class)
		if class == errorClassContextCancelled {
			outcome = "cancelled"
		} else {
			outcome = "exhausted"
		}
	} else if attempts > 1 {
		outcome = "retried"
	}

	rec := streamErrorRecord{
		Timestamp:      time.Now(),
		Provider:       in.provider,
		Model:          in.model,
		Transport:      in.transport,
		Outcome:        outcome,
		Attempts:       attempts,
		TTFTMillis:     int(in.ttft.Milliseconds()),
		DurationMillis: int(time.Since(in.start).Milliseconds()),
		ErrorClass:     errClass,
		Error:          errText,
		PartialStream:  in.partialStream,
		Chunks:         in.chunks,
		RequestURL:     in.requestURL,
	}
	if in.log.captureBodies() {
		rec.RequestHeaders = in.requestHeaders
		rec.RequestBody = json.RawMessage(append([]byte(nil), in.requestBody...))
		rec.ResponseHeaders = sanitizeHeaders(in.responseHeaders)
	}
	in.log.Log(rec)
}
