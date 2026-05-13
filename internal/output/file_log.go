package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileLogSink writes events to a log file in a readable append-only format.
type FileLogSink struct {
	mu            sync.Mutex
	file          *os.File
	thinkingChunk bool
}

// NewFileLogSink creates a new file-based event sink at the given path.
// If thinkingChunk is false, ThinkingChunkEvent events are silently dropped.
func NewFileLogSink(path string, thinkingChunk bool) (*FileLogSink, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("log file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log file directory: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}
	return &FileLogSink{file: file, thinkingChunk: thinkingChunk}, nil
}

// Emit appends event to the log file unless it is filtered out.
func (s *FileLogSink) Emit(event Event) {
	if s == nil || s.file == nil {
		return
	}
	if event.Type == EventTypeThinkingChunk && !s.thinkingChunk {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := fmt.Fprintf(s.file, "=== %s %s ===\n", event.Timestamp.UTC().Format(time.RFC3339Nano), event.Type); err != nil {
		return
	}
	switch payload := event.Payload.(type) {
	case UserInputEvent:
		if payload.Mode != "" {
			if _, err := fmt.Fprintf(s.file, "mode: %s\n", payload.Mode); err != nil {
				return
			}
		}
		if _, err := io.WriteString(s.file, payload.Content); err != nil {
			return
		}
		if _, err := io.WriteString(s.file, "\n\n"); err != nil {
			return
		}
	default:
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			if _, writeErr := fmt.Fprintf(s.file, "%v\n\n", payload); writeErr != nil {
				return
			}
			return
		}
		if _, err := s.file.Write(data); err != nil {
			return
		}
		if _, err := io.WriteString(s.file, "\n\n"); err != nil {
			return
		}
	}
}

// Close releases the underlying log file handle.
func (s *FileLogSink) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.file.Close()
	s.file = nil
	return err
}

// MultiSink fans each event out to multiple sinks.
type MultiSink struct {
	sinks []EventSink
}

// NewMultiSink returns a sink that emits to every non-nil sink in sinks.
func NewMultiSink(sinks ...EventSink) EventSink {
	filtered := make([]EventSink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			filtered = append(filtered, sink)
		}
	}
	if len(filtered) == 0 {
		return NoopSink{}
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return MultiSink{sinks: filtered}
}

// Emit forwards event to each configured child sink.
func (s MultiSink) Emit(event Event) {
	for _, sink := range s.sinks {
		sink.Emit(event)
	}
}
