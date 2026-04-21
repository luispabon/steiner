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

type FileLogSink struct {
	mu   sync.Mutex
	file *os.File
}

func NewFileLogSink(path string) (*FileLogSink, error) {
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
	return &FileLogSink{file: file}, nil
}

func (s *FileLogSink) Emit(event Event) {
	if s == nil || s.file == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, _ = fmt.Fprintf(s.file, "=== %s %s ===\n", event.Timestamp.UTC().Format(time.RFC3339Nano), event.Type)
	switch payload := event.Payload.(type) {
	case UserInputEvent:
		if payload.Mode != "" {
			_, _ = fmt.Fprintf(s.file, "mode: %s\n", payload.Mode)
		}
		_, _ = io.WriteString(s.file, payload.Content)
		_, _ = io.WriteString(s.file, "\n\n")
	default:
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintf(s.file, "%v\n\n", payload)
			return
		}
		_, _ = s.file.Write(data)
		_, _ = io.WriteString(s.file, "\n\n")
	}
}

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

type MultiSink struct {
	sinks []EventSink
}

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

func (s MultiSink) Emit(event Event) {
	for _, sink := range s.sinks {
		sink.Emit(event)
	}
}
