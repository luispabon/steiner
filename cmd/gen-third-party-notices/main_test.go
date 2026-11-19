package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckFile(t *testing.T) {
	dir := t.TempDir()
	data := []byte("notices\n")
	identical := filepath.Join(dir, "identical.md")
	if err := os.WriteFile(identical, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	different := filepath.Join(dir, "different.md")
	if err := os.WriteFile(different, []byte("other\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}

	tests := []struct {
		name      string
		path      string
		wantStale bool
		wantErr   bool
	}{
		{name: "identical file", path: identical},
		{name: "differing content", path: different, wantStale: true, wantErr: true},
		{name: "absent file", path: filepath.Join(dir, "absent.md"), wantStale: true, wantErr: true},
		{name: "directory path", path: subdir, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkFile(tt.path, data)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("checkFile: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkFile returned no error, want one")
			}
			var stale *staleError
			if got := errors.As(err, &stale); got != tt.wantStale {
				t.Errorf("errors.As(err, *staleError) = %v, want %v (err = %v)", got, tt.wantStale, err)
			}
		})
	}
}
