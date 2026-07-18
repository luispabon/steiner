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
	if s == nil {
		return
	}
	if event.Type == EventTypeThinkingChunk && !s.thinkingChunk {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file == nil {
		return
	}

	if !s.writef("=== %s %s ===\n", event.Timestamp.UTC().Format(time.RFC3339Nano), event.Type) {
		return
	}
	switch payload := event.Payload.(type) {
	case UserInputEvent:
		if payload.Mode != "" {
			if !s.writef("mode: %s\n", payload.Mode) {
				return
			}
		}
		if !s.writeString(payload.Content) {
			return
		}
		if !s.writeString("\n\n") {
			return
		}
	default:
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			s.writef("%v\n\n", payload)
			return
		}
		if !s.writeBytes(data) {
			return
		}
		if !s.writeString("\n\n") {
			return
		}
	}
}

func (s *FileLogSink) writeString(str string) bool {
	_, err := io.WriteString(s.file, str)
	return err == nil
}

func (s *FileLogSink) writef(format string, args ...any) bool {
	_, err := fmt.Fprintf(s.file, format, args...)
	return err == nil
}

func (s *FileLogSink) writeBytes(data []byte) bool {
	_, err := s.file.Write(data)
	return err == nil
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
