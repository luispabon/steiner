package patchdoc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidatePatchPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "dir"))
	mustWriteFile(t, filepath.Join(root, "dir", "file.txt"), "content")

	outside := t.TempDir()
	mustMkdir(t, filepath.Join(outside, "nested"))
	mustWriteFile(t, filepath.Join(outside, "nested", "outside.txt"), "content")

	symlinkSupported := canCreateSymlink(t)
	if symlinkSupported {
		mustSymlink(t, filepath.Join(outside, "nested"), filepath.Join(root, "linked-outside"))
	}

	tests := []struct {
		name    string
		path    string
		wantAbs string
		wantRel string
		wantErr string
	}{
		{
			name:    "empty path",
			path:    "",
			wantErr: "path must not be empty",
		},
		{
			name:    "absolute path",
			path:    string(filepath.Separator) + "tmp" + string(filepath.Separator) + "x",
			wantErr: "path must be relative",
		},
		{
			name:    "nul byte",
			path:    "bad\x00path",
			wantErr: "path must not contain NUL bytes",
		},
		{
			name:    "current directory",
			path:    ".",
			wantErr: "path must not resolve to current directory",
		},
		{
			name:    "parent directory",
			path:    "..",
			wantErr: "path must not escape workdir",
		},
		{
			name:    "parent prefix",
			path:    "../escape.txt",
			wantErr: "path must not escape workdir",
		},
		{
			name:    "cleaned path",
			path:    "dir/./../file.txt",
			wantAbs: filepath.Join(root, "file.txt"),
			wantRel: "file.txt",
		},
		{
			name:    "existing target",
			path:    "dir/file.txt",
			wantAbs: filepath.Join(root, "dir", "file.txt"),
			wantRel: "dir/file.txt",
		},
		{
			name:    "missing child under existing directory",
			path:    "dir/missing/new.txt",
			wantAbs: filepath.Join(root, "dir", "missing", "new.txt"),
			wantRel: "dir/missing/new.txt",
		},
	}

	if symlinkSupported {
		tests = append(tests, struct {
			name    string
			path    string
			wantAbs string
			wantRel string
			wantErr string
		}{
			name:    "symlinked ancestor outside workdir",
			path:    "linked-outside/file.txt",
			wantErr: "path escapes workdir",
		})
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotAbs, gotRel, err := ValidatePatchPath(root, tt.path)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ValidatePatchPath() error = nil, want %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ValidatePatchPath() error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidatePatchPath() error = %v", err)
			}
			if gotAbs != tt.wantAbs {
				t.Fatalf("ValidatePatchPath() abs = %q, want %q", gotAbs, tt.wantAbs)
			}
			if gotRel != tt.wantRel {
				t.Fatalf("ValidatePatchPath() rel = %q, want %q", gotRel, tt.wantRel)
			}
			if filepath.IsAbs(gotRel) {
				t.Fatalf("ValidatePatchPath() rel = %q, want slash-normalized relative path", gotRel)
			}
		})
	}
}

func TestValidateDuplicateTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hunks   []Hunk
		wantErr string
	}{
		{
			name: "unique targets",
			hunks: []Hunk{
				AddFile{PathValue: "new.txt"},
				DeleteFile{PathValue: "old.txt"},
				UpdateFile{PathValue: "src.txt", MovePath: "dst.txt"},
			},
		},
		{
			name: "duplicate source path",
			hunks: []Hunk{
				DeleteFile{PathValue: "same.txt"},
				UpdateFile{PathValue: "same.txt"},
			},
			wantErr: "duplicate source path",
		},
		{
			name: "duplicate affected path",
			hunks: []Hunk{
				AddFile{PathValue: "same.txt"},
				UpdateFile{PathValue: "src.txt", MovePath: "same.txt"},
			},
			wantErr: "duplicate affected path",
		},
		{
			name: "move destination equals source",
			hunks: []Hunk{
				UpdateFile{PathValue: "same.txt", MovePath: "same.txt"},
			},
			wantErr: "move destination equals source",
		},
		{
			name: "move destination touched by another op",
			hunks: []Hunk{
				AddFile{PathValue: "dst.txt"},
				UpdateFile{PathValue: "src.txt", MovePath: "dst.txt"},
			},
			wantErr: "duplicate affected path",
		},
		{
			name: "move destination collides with another source",
			hunks: []Hunk{
				UpdateFile{PathValue: "dst.txt", MovePath: "other.txt"},
				UpdateFile{PathValue: "src.txt", MovePath: "dst.txt"},
			},
			wantErr: "collides with source path",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateDuplicateTargets(tt.hunks)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateDuplicateTargets() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateDuplicateTargets() error = nil, want %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateDuplicateTargets() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func canCreateSymlink(t *testing.T) bool {
	t.Helper()

	if runtime.GOOS == "windows" {
		return false
	}

	target := filepath.Join(t.TempDir(), "target")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("prepare symlink target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		return false
	}
	return true
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string, contents string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func mustSymlink(t *testing.T, oldname string, newname string) {
	t.Helper()

	if err := os.Symlink(oldname, newname); err != nil {
		t.Fatalf("symlink %s -> %s: %v", newname, oldname, err)
	}
}
