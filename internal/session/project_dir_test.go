package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// requireSymlink creates link -> target, skipping the test where symlinks are
// unavailable (Windows without the privilege).
func requireSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("create symlink: %v", err)
	}
}

func TestEnsureSteinerProjectDir(t *testing.T) {
	tests := []struct {
		name string
		prep func(t *testing.T, dir string)
		want string
	}{
		{
			name: "creates directory and gitignore from scratch",
			prep: func(_ *testing.T, _ string) {},
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

			if err := EnsureSteinerProjectDir(tempDir); err != nil {
				t.Fatalf("EnsureSteinerProjectDir: %v", err)
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
}

func TestEnsureSteinerProjectDirRejectsUnsafePaths(t *testing.T) {
	tests := []struct {
		name    string
		prep    func(t *testing.T, dir, outside string)
		wantErr string
		check   func(t *testing.T, outside string)
	}{
		{
			name: "steiner directory is a symlink",
			prep: func(t *testing.T, dir, outside string) {
				requireSymlink(t, outside, filepath.Join(dir, ".steiner"))
			},
			wantErr: "is a symlink",
			check: func(t *testing.T, outside string) {
				if _, err := os.Stat(filepath.Join(outside, ".gitignore")); !os.IsNotExist(err) {
					t.Fatalf(".gitignore created outside the project: %v", err)
				}
			},
		},
		{
			name: "steiner directory is a dangling symlink",
			prep: func(t *testing.T, dir, outside string) {
				requireSymlink(t, filepath.Join(outside, "missing"), filepath.Join(dir, ".steiner"))
			},
			wantErr: "is a symlink",
			check: func(t *testing.T, outside string) {
				if _, err := os.Stat(filepath.Join(outside, "missing", ".gitignore")); !os.IsNotExist(err) {
					t.Fatalf(".gitignore created outside the project: %v", err)
				}
			},
		},
		{
			name: "steiner path is a regular file",
			prep: func(t *testing.T, dir, _ string) {
				if err := os.WriteFile(filepath.Join(dir, ".steiner"), []byte("not a dir"), 0o644); err != nil {
					t.Fatalf("write file: %v", err)
				}
			},
			wantErr: "is not a directory",
			check:   func(*testing.T, string) {},
		},
		{
			name: "gitignore is a dangling symlink",
			prep: func(t *testing.T, dir, outside string) {
				steinerDir := filepath.Join(dir, ".steiner")
				if err := os.Mkdir(steinerDir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				target := filepath.Join(outside, "escaped")
				requireSymlink(t, target, filepath.Join(steinerDir, ".gitignore"))
			},
			wantErr: "is a symlink",
			check: func(t *testing.T, outside string) {
				if _, err := os.Stat(filepath.Join(outside, "escaped")); !os.IsNotExist(err) {
					t.Fatalf("gitignore symlink target created outside the project: %v", err)
				}
			},
		},
		{
			name: "gitignore is a symlink to a regular file",
			prep: func(t *testing.T, dir, outside string) {
				steinerDir := filepath.Join(dir, ".steiner")
				if err := os.Mkdir(steinerDir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				target := filepath.Join(outside, "kept")
				if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
					t.Fatalf("write target: %v", err)
				}
				requireSymlink(t, target, filepath.Join(steinerDir, ".gitignore"))
			},
			wantErr: "is a symlink",
			check: func(t *testing.T, outside string) {
				got, err := os.ReadFile(filepath.Join(outside, "kept"))
				if err != nil {
					t.Fatalf("read outside file: %v", err)
				}
				if string(got) != "keep\n" {
					t.Fatalf("outside file content = %q, want %q", got, "keep\n")
				}
			},
		},
		{
			name: "gitignore is a directory",
			prep: func(t *testing.T, dir, _ string) {
				steinerDir := filepath.Join(dir, ".steiner")
				if err := os.MkdirAll(filepath.Join(steinerDir, ".gitignore"), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
			},
			wantErr: "is not a regular file",
			check:   func(*testing.T, string) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			outside := t.TempDir()
			tt.prep(t, tempDir, outside)

			err := EnsureSteinerProjectDir(tempDir)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want it to mention %q", err, tt.wantErr)
			}
			tt.check(t, outside)
		})
	}
}

func TestEnsureSteinerProjectDirFollowsSymlinkedWorkDir(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "project")
	requireSymlink(t, real, link)

	if err := EnsureSteinerProjectDir(link); err != nil {
		t.Fatalf("EnsureSteinerProjectDir: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(real, ".steiner", ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if string(got) != "*\n" {
		t.Fatalf(".gitignore content = %q, want %q", got, "*\n")
	}
}

func TestEnsureSteinerProjectDirConcurrent(t *testing.T) {
	tempDir := t.TempDir()

	const callers = 8
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- EnsureSteinerProjectDir(tempDir)
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent EnsureSteinerProjectDir: %v", err)
		}
	}

	got, err := os.ReadFile(filepath.Join(tempDir, ".steiner", ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if string(got) != "*\n" {
		t.Fatalf(".gitignore content = %q, want %q", got, "*\n")
	}
}

func TestEnsureSteinerProjectDirReadOnlyProject(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}

	projectDir := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(projectDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(projectDir, 0o755)
	})

	if err := EnsureSteinerProjectDir(projectDir); err == nil {
		t.Fatalf("expected error, got nil")
	}
}
