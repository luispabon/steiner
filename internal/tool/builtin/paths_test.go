package builtin

import (
	"path/filepath"
	"testing"
)

func TestAbsWorkspacePath(t *testing.T) {
	t.Run("relative path resolves against workDir", func(t *testing.T) {
		got, err := absWorkspacePath("/project", "subdir/file.txt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "/project/subdir/file.txt"
		if got != want {
			t.Errorf("absWorkspacePath = %q, want %q", got, want)
		}
	})

	t.Run("absolute path stays absolute", func(t *testing.T) {
		got, err := absWorkspacePath("/project", "/other/file.txt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "/other/file.txt"
		if got != want {
			t.Errorf("absWorkspacePath = %q, want %q", got, want)
		}
	})

	t.Run("absolute path with dots is cleaned", func(t *testing.T) {
		got, err := absWorkspacePath("/project", "/other/../dir/./file.txt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "/dir/file.txt"
		if got != want {
			t.Errorf("absWorkspacePath = %q, want %q", got, want)
		}
	})

	t.Run("empty path defaults to workDir", func(t *testing.T) {
		got, err := absWorkspacePath("/project", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "/project"
		if got != want {
			t.Errorf("absWorkspacePath = %q, want %q", got, want)
		}
	})

	t.Run("whitespace path defaults to workDir", func(t *testing.T) {
		got, err := absWorkspacePath("/project", "  ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "/project"
		if got != want {
			t.Errorf("absWorkspacePath = %q, want %q", got, want)
		}
	})

	t.Run("empty path with empty workDir returns error", func(t *testing.T) {
		_, err := absWorkspacePath("", "")
		if err == nil {
			t.Fatal("expected error for empty path and empty workDir")
		}
	})

	t.Run("relative path with empty workDir returns error", func(t *testing.T) {
		_, err := absWorkspacePath("", "some/file.txt")
		if err == nil {
			t.Fatal("expected error for relative path without workDir")
		}
	})

	t.Run("dot relative path resolves against workDir", func(t *testing.T) {
		got, err := absWorkspacePath("/project", "./file.txt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "/project/file.txt"
		if got != want {
			t.Errorf("absWorkspacePath = %q, want %q", got, want)
		}
	})
}

func TestRelDisplayPath(t *testing.T) {
	t.Run("returns relative path when under workDir", func(t *testing.T) {
		got := relDisplayPath("/project", "/project/subdir/file.txt")
		want := "subdir/file.txt"
		if got != want {
			t.Errorf("relDisplayPath = %q, want %q", got, want)
		}
	})

	t.Run("returns path as-is when not under workDir", func(t *testing.T) {
		got := relDisplayPath("/project", "/other/file.txt")
		want := "../other/file.txt"
		if got != want {
			t.Errorf("relDisplayPath = %q, want %q", got, want)
		}
	})

	t.Run("returns empty when path is empty", func(t *testing.T) {
		got := relDisplayPath("/project", "")
		if got != "" {
			t.Errorf("relDisplayPath = %q, want empty", got)
		}
	})

	t.Run("returns path as-is when workDir is empty", func(t *testing.T) {
		got := relDisplayPath("", "/project/file.txt")
		want := "/project/file.txt"
		if got != want {
			t.Errorf("relDisplayPath = %q, want %q", got, want)
		}
	})

	t.Run("returns dot for exact match", func(t *testing.T) {
		got := relDisplayPath("/project", "/project")
		want := "."
		if got != want {
			t.Errorf("relDisplayPath = %q, want %q", got, want)
		}
	})

	t.Run("platform-specific path separator", func(t *testing.T) {
		sep := string(filepath.Separator)
		got := relDisplayPath("/project"+sep+"src", "/project"+sep+"src"+sep+"main.go")
		want := "main.go"
		if got != want {
			t.Errorf("relDisplayPath = %q, want %q", got, want)
		}
	})
}
