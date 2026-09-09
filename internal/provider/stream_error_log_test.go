package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStreamErrorLogPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  "",
		},
		{
			name:  "with .log extension",
			input: "foo.log",
			want:  "foo-stream-errors.log",
		},
		{
			name:  "absolute path with .log extension",
			input: "/tmp/test.log",
			want:  "/tmp/test-stream-errors.log",
		},
		{
			name:  "path with directory",
			input: "/var/log/session.log",
			want:  "/var/log/session-stream-errors.log",
		},
		{
			name:  "no extension",
			input: "myfile",
			want:  "myfile-stream-errors.log",
		},
		{
			name:  "other extension",
			input: "session.jsonl",
			want:  "session-stream-errors.jsonl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StreamErrorLogPath(tt.input)
			if got != tt.want {
				t.Errorf("StreamErrorLogPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewStreamErrorLogger(t *testing.T) {
	t.Run("nil for empty path", func(t *testing.T) {
		l, err := NewStreamErrorLogger("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if l != nil {
			t.Error("expected nil logger for empty path")
		}
	})

	t.Run("nil for whitespace path", func(t *testing.T) {
		l, err := NewStreamErrorLogger("   ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if l != nil {
			t.Error("expected nil logger for whitespace path")
		}
	})

	t.Run("non-nil for valid path", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "stream-errors.log")
		l, err := NewStreamErrorLogger(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if l == nil {
			t.Fatal("expected non-nil logger")
		}
		defer l.Close() //nolint:errcheck
	})

	t.Run("file created on disk", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "stream-errors.log")
		l, err := NewStreamErrorLogger(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer l.Close() //nolint:errcheck

		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			t.Error("expected log file to exist on disk")
		}
	})

	t.Run("creates parent directories", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nested", "deep", "stream-errors.log")
		l, err := NewStreamErrorLogger(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer l.Close() //nolint:errcheck

		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			t.Error("expected log file to exist in nested directory")
		}
	})

	t.Run("exact permissions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nested", "stream-errors.log")
		l, err := NewStreamErrorLogger(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer l.Close() //nolint:errcheck

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat file: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %o, want 0o600", info.Mode().Perm())
		}

		dirInfo, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if dirInfo.Mode().Perm() != 0o700 {
			t.Errorf("dir mode = %o, want 0o700", dirInfo.Mode().Perm())
		}
	})
}

func TestStreamErrorLoggerUpgradesExistingFileWithoutChangingParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream-errors.log")
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("create log file: %v", err)
	}

	logger, err := NewStreamErrorLogger(path)
	if err != nil {
		t.Fatalf("NewStreamErrorLogger() error = %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })

	if info, err := os.Stat(path); err != nil {
		t.Fatalf("stat log file: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", info.Mode().Perm())
	}
	if info, err := os.Stat(dir); err != nil {
		t.Fatalf("stat parent: %v", err)
	} else if info.Mode().Perm() != 0o755 {
		t.Errorf("parent mode = %o, want unchanged 755", info.Mode().Perm())
	}
}

func TestStreamErrorLogger_Log(t *testing.T) {
	t.Run("nil receiver no-op", func(_ *testing.T) {
		var l *StreamErrorLogger
		l.Log(streamErrorRecord{Outcome: "retried"})
	})

	t.Run("write and read back valid JSON", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "stream-errors.log")
		l, err := NewStreamErrorLogger(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := streamErrorRecord{
			Timestamp:       time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			Provider:        "openai_compat",
			Model:           "gpt-4",
			Transport:       "sse",
			Outcome:         "retried",
			Attempts:        2,
			TTFTMillis:      120,
			DurationMillis:  1500,
			ErrorClass:      "http_5xx",
			Error:           "EOF",
			Chunks:          10,
			PartialStream:   true,
			RequestURL:      "https://api.example.com/v1/chat",
			RequestHeaders:  map[string]string{"Content-Type": "application/json"},
			RequestBody:     json.RawMessage(`{"model":"gpt-4"}`),
			ResponseHeaders: map[string]string{"X-Request-ID": "abc123"},
		}

		l.Log(want)

		if err := l.Close(); err != nil {
			t.Fatalf("close error: %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read file error: %v", err)
		}

		var got streamErrorRecord
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}

		if got.Outcome != want.Outcome {
			t.Errorf("Outcome: got %q, want %q", got.Outcome, want.Outcome)
		}
		if got.Attempts != want.Attempts {
			t.Errorf("Attempts: got %d, want %d", got.Attempts, want.Attempts)
		}
		if got.Error != want.Error {
			t.Errorf("Error: got %q, want %q", got.Error, want.Error)
		}
		if got.Chunks != want.Chunks {
			t.Errorf("Chunks: got %d, want %d", got.Chunks, want.Chunks)
		}
		if got.PartialStream != want.PartialStream {
			t.Errorf("PartialStream: got %v, want %v", got.PartialStream, want.PartialStream)
		}
		if got.RequestURL != want.RequestURL {
			t.Errorf("RequestURL: got %q, want %q", got.RequestURL, want.RequestURL)
		}
	})

	t.Run("multiple records appended", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "stream-errors.log")
		l, err := NewStreamErrorLogger(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		l.Log(streamErrorRecord{Outcome: "retried", Attempts: 1})
		l.Log(streamErrorRecord{Outcome: "exhausted", Attempts: 3})

		if err := l.Close(); err != nil {
			t.Fatalf("close error: %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read file error: %v", err)
		}

		dec := json.NewDecoder(bytes.NewReader(data))
		var records []streamErrorRecord
		for dec.More() {
			var r streamErrorRecord
			if err := dec.Decode(&r); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			records = append(records, r)
		}

		if len(records) != 2 {
			t.Fatalf("expected 2 records, got %d", len(records))
		}
		if records[0].Outcome != "retried" {
			t.Errorf("records[0].Outcome = %q, want %q", records[0].Outcome, "retried")
		}
		if records[1].Outcome != "exhausted" {
			t.Errorf("records[1].Outcome = %q, want %q", records[1].Outcome, "exhausted")
		}
	})
}

func TestEmitProviderCallOmitsRequestURLWithoutBodyCapture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream-errors.log")
	logger, err := NewStreamErrorLogger(path)
	if err != nil {
		t.Fatalf("NewStreamErrorLogger() error = %v", err)
	}

	emitProviderCall(providerCallInput{
		log:        logger,
		start:      time.Now(),
		attempts:   1,
		ctx:        context.Background(),
		requestURL: "https://api.example.com/v1/chat",
	})
	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if _, ok := record["request_url"]; ok {
		t.Fatalf("record contains request_url with capture_bodies disabled: %s", data)
	}
}

func TestStreamErrorLogger_Close(t *testing.T) {
	t.Run("nil receiver returns nil", func(t *testing.T) {
		var l *StreamErrorLogger
		if err := l.Close(); err != nil {
			t.Errorf("unexpected error from nil Close: %v", err)
		}
	})

	t.Run("double close does not panic", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "stream-errors.log")
		l, err := NewStreamErrorLogger(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if err := l.Close(); err != nil {
			t.Fatalf("first close error: %v", err)
		}
		// Second close returns an error (file already closed) but must not panic.
		_ = l.Close()
	})
}
