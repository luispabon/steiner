package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRoot(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) (file, workspace, wantRoot string)
		markers []string
	}{
		{
			name: "finds nearest marker",
			setup: func(t *testing.T) (string, string, string) {
				tmpdir := t.TempDir()
				rootMarker := filepath.Join(tmpdir, "go.mod")
				if err := writeFile(rootMarker); err != nil {
					t.Fatal(err)
				}
				subdir := filepath.Join(tmpdir, "pkg", "a")
				if err := mkdirAll(subdir); err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(subdir, "file.go")
				return file, tmpdir, tmpdir
			},
			markers: []string{"go.mod"},
		},
		{
			name: "prefers nested marker over distant one",
			setup: func(t *testing.T) (string, string, string) {
				tmpdir := t.TempDir()
				outerMarker := filepath.Join(tmpdir, "go.mod")
				if err := writeFile(outerMarker); err != nil {
					t.Fatal(err)
				}

				subdir := filepath.Join(tmpdir, "pkg")
				innerMarker := filepath.Join(subdir, "go.mod")
				if err := writeFile(innerMarker); err != nil {
					t.Fatal(err)
				}

				file := filepath.Join(subdir, "file.go")
				return file, tmpdir, subdir
			},
			markers: []string{"go.mod"},
		},
		{
			name: "returns workspace when no marker found",
			setup: func(t *testing.T) (string, string, string) {
				tmpdir := t.TempDir()
				subdir := filepath.Join(tmpdir, "pkg", "a")
				if err := mkdirAll(subdir); err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(subdir, "file.go")
				return file, tmpdir, tmpdir
			},
			markers: []string{"go.mod"},
		},
		{
			name: "checks multiple markers in order",
			setup: func(t *testing.T) (string, string, string) {
				tmpdir := t.TempDir()
				marker := filepath.Join(tmpdir, "pyproject.toml")
				if err := writeFile(marker); err != nil {
					t.Fatal(err)
				}
				subdir := filepath.Join(tmpdir, "pkg", "a")
				if err := mkdirAll(subdir); err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(subdir, "file.py")
				return file, tmpdir, tmpdir
			},
			markers: []string{"go.mod", "pyproject.toml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, workspace, wantRoot := tt.setup(t)
			got := resolveRoot(file, workspace, tt.markers)

			if got != wantRoot {
				t.Errorf("resolveRoot = %q, want %q", got, wantRoot)
			}
		})
	}
}

func writeFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(""), 0o644)
}

func mkdirAll(path string) error {
	return os.MkdirAll(path, 0o755)
}
