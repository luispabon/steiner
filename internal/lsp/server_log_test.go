package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLSPServerLogPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty input", input: "", want: ""},
		{name: "whitespace only", input: "   ", want: ""},
		{name: "with .log extension", input: "foo.log", want: "foo-lsp.log"},
		{name: "absolute path with .log extension", input: "/tmp/test.log", want: "/tmp/test-lsp.log"},
		{name: "path with directory", input: "/var/log/session.log", want: "/var/log/session-lsp.log"},
		{name: "no extension", input: "myfile", want: "myfile-lsp.log"},
		{name: "other extension", input: "session.jsonl", want: "session-lsp.jsonl"},
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

func TestNewLSPServerLogWriter(t *testing.T) {
	t.Run("no-op for empty path", func(t *testing.T) {
		w, err := NewServerLogWriter("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := w.(*noOpWriteCloser); !ok {
			t.Errorf("expected *noOpWriteCloser, got %T", w)
		}
	})

	t.Run("file created on disk for valid path", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "server.log")
		w, err := NewServerLogWriter(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
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

	t.Run("exact permissions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nested", "server.log")
		w, err := NewServerLogWriter(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer w.Close() //nolint:errcheck

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat file: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("file mode = 0o%o, want 0o600", info.Mode().Perm())
		}

		dirInfo, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if dirInfo.Mode().Perm() != 0o700 {
			t.Errorf("dir mode = 0o%o, want 0o700", dirInfo.Mode().Perm())
		}
	})
}
