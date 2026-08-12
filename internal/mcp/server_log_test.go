package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServerLogPath(t *testing.T) {
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
			want:  "foo-mcp.log",
		},
		{
			name:  "absolute path with .log extension",
			input: "/tmp/test.log",
			want:  "/tmp/test-mcp.log",
		},
		{
			name:  "path with directory",
			input: "/var/log/session.log",
			want:  "/var/log/session-mcp.log",
		},
		{
			name:  "no extension",
			input: "myfile",
			want:  "myfile-mcp.log",
		},
		{
			name:  "other extension",
			input: "session.jsonl",
			want:  "session-mcp.jsonl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServerLogPath(tt.input)
			if got != tt.want {
				t.Errorf("ServerLogPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewServerLogWriter(t *testing.T) {
	t.Run("no-op for empty path", func(t *testing.T) {
		w, err := NewServerLogWriter("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if w == nil {
			t.Fatal("expected non-nil writer")
		}
		// Verify it's the no-op type
		if _, ok := w.(*noOpWriteCloser); !ok {
			t.Errorf("expected *noOpWriteCloser, got %T", w)
		}
	})

	t.Run("no-op for whitespace path", func(t *testing.T) {
		w, err := NewServerLogWriter("   ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if w == nil {
			t.Fatal("expected non-nil writer")
		}
		if _, ok := w.(*noOpWriteCloser); !ok {
			t.Errorf("expected *noOpWriteCloser, got %T", w)
		}
	})

	t.Run("no-op writer discards output", func(t *testing.T) {
		w, err := NewServerLogWriter("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		n, err := w.Write([]byte("test data"))
		if err != nil {
			t.Errorf("Write error: %v", err)
		}
		if n != 9 {
			t.Errorf("Write returned %d, want 9", n)
		}
		if err := w.Close(); err != nil {
			t.Errorf("Close error: %v", err)
		}
	})

	t.Run("file created on disk for valid path", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "server.log")
		w, err := NewServerLogWriter(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if w == nil {
			t.Fatal("expected non-nil writer")
		}
		defer w.Close() //nolint:errcheck

		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			t.Error("expected log file to exist on disk")
		}
	})

	t.Run("creates parent directories", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nested", "deep", "server.log")
		w, err := NewServerLogWriter(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer w.Close() //nolint:errcheck

		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			t.Error("expected log file to exist in nested directory")
		}
	})

	t.Run("file mode is 0o600", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "server.log")
		w, err := NewServerLogWriter(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		w.Close() //nolint:errcheck

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat error: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("file mode = 0o%o, want 0o600", info.Mode().Perm())
		}
	})

	t.Run("writes and appends to file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "server.log")
		w, err := NewServerLogWriter(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		n, err := w.Write([]byte("first line\n"))
		if err != nil {
			t.Fatalf("Write error: %v", err)
		}
		if n != 11 {
			t.Errorf("Write returned %d, want 11", n)
		}

		if err := w.Close(); err != nil {
			t.Fatalf("Close error: %v", err)
		}

		// Reopen and append
		w2, err := NewServerLogWriter(path)
		if err != nil {
			t.Fatalf("unexpected error on reopen: %v", err)
		}
		defer w2.Close() //nolint:errcheck

		n, err = w2.Write([]byte("second line\n"))
		if err != nil {
			t.Fatalf("Write error: %v", err)
		}
		if n != 12 {
			t.Errorf("Write returned %d, want 12", n)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile error: %v", err)
		}

		expected := "first line\nsecond line\n"
		if string(data) != expected {
			t.Errorf("file content = %q, want %q", string(data), expected)
		}
	})
}
