package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureSteinerProjectDir(t *testing.T) {
	tests := []struct {
		name string
		prep func(t *testing.T, dir string)
		want string
	}{
		{
			name: "creates directory and gitignore from scratch",
			prep: func(_ *testing.T, dir string) {},
			want: "*\n",
		},
		{
			name: "skips existing gitignore",
			prep: func(t *testing.T, dir string) {
				steinerDir := filepath.Join(dir, ".steiner")
				if err := os.MkdirAll(steinerDir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(steinerDir, ".gitignore"), []byte("custom\n"), 0o644); err != nil {
					t.Fatalf("write file: %v", err)
				}
			},
			want: "custom\n",
		},
		{
			name: "creates gitignore when directory already exists",
			prep: func(t *testing.T, dir string) {
				steinerDir := filepath.Join(dir, ".steiner")
				if err := os.MkdirAll(steinerDir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
			},
			want: "*\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			tt.prep(t, tempDir)

			if err := ensureSteinerProjectDir(tempDir); err != nil {
				t.Fatalf("ensureSteinerProjectDir: %v", err)
			}

			steinerDir := filepath.Join(tempDir, ".steiner")
			info, err := os.Stat(steinerDir)
			if err != nil {
				t.Fatalf("stat .steiner: %v", err)
			}
			if !info.IsDir() {
				t.Fatalf(".steiner is not a directory")
			}

			got, err := os.ReadFile(filepath.Join(steinerDir, ".gitignore"))
			if err != nil {
				t.Fatalf("read .gitignore: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf(".gitignore content = %q, want %q", got, tt.want)
			}
		})
	}

	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		t.Run("returns error on read-only parent", func(t *testing.T) {
			tempDir := t.TempDir()
			if err := os.Chmod(tempDir, 0o555); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			t.Cleanup(func() {
				_ = os.Chmod(tempDir, 0o755)
			})

			err := ensureSteinerProjectDir(filepath.Join(tempDir, "sub"))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}
