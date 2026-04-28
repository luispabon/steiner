package tool

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestPolicy_NormalizePolicyPath(t *testing.T) {
	tests := []struct {
		name string
		root string
		raw  string
		want string
	}{
		{
			name: "empty raw returns empty",
			root: "",
			raw:  "",
			want: "",
		},
		{
			name: "whitespace raw returns empty",
			root: "",
			raw:  "  ",
			want: "",
		},
		{
			name: "relative path with root",
			root: "/project",
			raw:  "subdir/file.txt",
			want: "/project/subdir/file.txt",
		},
		{
			name: "relative path without root",
			root: "",
			raw:  "subdir/../file.txt",
			want: "file.txt",
		},
		{
			name: "absolute path is cleaned",
			root: "/project",
			raw:  "/other/../dir//file.txt",
			want: "/dir/file.txt",
		},
		{
			name: "already clean absolute path",
			root: "/project",
			raw:  "/project/file.txt",
			want: "/project/file.txt",
		},
		{
			name: "dot prefix relative path",
			root: "/project",
			raw:  "./file.txt",
			want: "/project/file.txt",
		},
		{
			name: "trailing dotdot resolves above root",
			root: "/project/subdir",
			raw:  "../file.txt",
			want: "/project/file.txt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizePolicyPath(tc.root, tc.raw)
			if got != tc.want {
				t.Fatalf("normalizePolicyPath(%q, %q) = %q, want %q", tc.root, tc.raw, got, tc.want)
			}
		})
	}
}

func TestPolicy_PathWithinRoot(t *testing.T) {
	tests := []struct {
		name string
		root string
		path string
		want bool
	}{
		{
			name: "empty root returns false",
			root: "",
			path: "/project/file.txt",
			want: false,
		},
		{
			name: "empty path returns false",
			root: "/project",
			path: "",
			want: false,
		},
		{
			name: "exact root match",
			root: "/project",
			path: "/project",
			want: true,
		},
		{
			name: "subdirectory within root",
			root: "/project",
			path: "/project/subdir/file.txt",
			want: true,
		},
		{
			name: "sibling directory outside root",
			root: "/project",
			path: "/other/file.txt",
			want: false,
		},
		{
			name: "parent directory escape",
			root: "/project/subdir",
			path: "/project",
			want: false,
		},
		{
			name: "deep parent escape",
			root: "/project/subdir/deep",
			path: "/project",
			want: false,
		},
		{
			name: "unrelated absolute path",
			root: "/project",
			path: "/tmp/file.txt",
			want: false,
		},
		{
			name: "root is prefix but not parent",
			root: "/project",
			path: "/project-other/file.txt",
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pathWithinRoot(tc.root, tc.path)
			if got != tc.want {
				t.Fatalf("pathWithinRoot(%q, %q) = %v, want %v", tc.root, tc.path, got, tc.want)
			}
		})
	}
}

func TestPolicy_ResolvePath_BlocksOutsideRoot(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{ProjectRootOnly: true})

	tests := []struct {
		name string
		path string
	}{
		{
			name: "absolute path outside root",
			path: "/etc/passwd",
		},
		{
			name: "parent directory escape",
			path: "../other",
		},
		{
			name: "double parent escape above root",
			path: "../../etc",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := policy.ResolvePath(tc.path, false)
			if err == nil {
				t.Fatalf("ResolvePath(%q) = nil, want error", tc.path)
			}
		})
	}
}

func TestPolicy_ResolvePath_AllowsWithinRoot(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{ProjectRootOnly: true})

	got, err := policy.ResolvePath("subdir/file.txt", false)
	if err != nil {
		t.Fatalf("ResolvePath(subdir/file.txt) error = %v", err)
	}
	if want := "/project/subdir/file.txt"; got != want {
		t.Fatalf("ResolvePath() = %q, want %q", got, want)
	}
}

func TestPolicy_ResolvePath_BlockedPaths(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{
		ProjectRootOnly: true,
		BlockedPaths:    []string{"secrets"},
	})

	tests := []struct {
		name string
		path string
	}{
		{
			name: "path inside blocked directory",
			path: "secrets/key.txt",
		},
		{
			name: "exact blocked path",
			path: "secrets",
		},
		{
			name: "nested blocked path",
			path: "secrets/subdir/secret.txt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := policy.ResolvePath(tc.path, false)
			if err == nil {
				t.Fatalf("ResolvePath(%q) = nil, want error", tc.path)
			}
		})
	}
}

func TestPolicy_ResolvePath_AllowsNonBlockedPaths(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{
		ProjectRootOnly: true,
		BlockedPaths:    []string{"secrets"},
	})

	got, err := policy.ResolvePath("src/main.go", false)
	if err != nil {
		t.Fatalf("ResolvePath(src/main.go) error = %v", err)
	}
	if want := "/project/src/main.go"; got != want {
		t.Fatalf("ResolvePath() = %q, want %q", got, want)
	}
}

func TestPolicy_ResolvePath_WritablePaths(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{
		ProjectRootOnly: true,
		WritablePaths:   []string{"output"},
	})

	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{
			name:    "path inside writable allowlist",
			path:    "output/result.json",
			want:    "/project/output/result.json",
			wantErr: false,
		},
		{
			name:    "exact writable path",
			path:    "output",
			want:    "/project/output",
			wantErr: false,
		},
		{
			name:    "nested path in writable allowlist",
			path:    "output/subdir/file.txt",
			want:    "/project/output/subdir/file.txt",
			wantErr: false,
		},
		{
			name:    "path not in writable allowlist",
			path:    "src/main.go",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := policy.ResolvePath(tc.path, true)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolvePath(%q) = nil, want error", tc.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolvePath(%q) error = %v", tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("ResolvePath() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPolicy_ResolvePath_ReadIgnoredWritableCheck(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{
		ProjectRootOnly: true,
		WritablePaths:   []string{"output"},
	})

	got, err := policy.ResolvePath("src/main.go", false)
	if err != nil {
		t.Fatalf("ResolvePath(src/main.go) with writable=false error = %v", err)
	}
	if want := "/project/src/main.go"; got != want {
		t.Fatalf("ResolvePath() = %q, want %q", got, want)
	}
}

func TestPolicy_EnsureAllowed_OutsideRoot(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{ProjectRootOnly: true})

	err := policy.ensureAllowed("/other/file.txt", false)
	if err == nil {
		t.Fatal("ensureAllowed(/other/file.txt) = nil, want error")
	}
}

func TestPolicy_EnsureAllowed_EmptyPath(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{})

	err := policy.ensureAllowed("", false)
	if err == nil {
		t.Fatal("ensureAllowed(\"\") = nil, want error")
	}
}

func TestPolicy_PreviewToolInput_Read(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{ProjectRootOnly: true})

	preview, err := policy.previewToolInput("read", map[string]any{
		"path": "subdir/file.txt",
	})
	if err != nil {
		t.Fatalf("previewToolInput(read) error = %v", err)
	}
	if got, want := preview.Tool, "read"; got != want {
		t.Fatalf("Tool = %q, want %q", got, want)
	}
	if len(preview.Fields) != 1 {
		t.Fatalf("Fields len = %d, want 1", len(preview.Fields))
	}
	if got, want := preview.Fields[0].Name, "path"; got != want {
		t.Fatalf("Fields[0].Name = %q, want %q", got, want)
	}
	if got, want := preview.Fields[0].Value, "/project/subdir/file.txt"; got != want {
		t.Fatalf("Fields[0].Value = %q, want %q", got, want)
	}
}

func TestPolicy_PreviewToolInput_Write(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{
		ProjectRootOnly: true,
		WritablePaths:   []string{"output"},
	})

	preview, err := policy.previewToolInput("write", map[string]any{
		"path": "output/result.json",
	})
	if err != nil {
		t.Fatalf("previewToolInput(write) error = %v", err)
	}
	if got, want := preview.Tool, "write"; got != want {
		t.Fatalf("Tool = %q, want %q", got, want)
	}
	if len(preview.Fields) == 0 {
		t.Fatal("previewToolInput(write) returned 0 fields, expected at least 1")
	}
	if got, want := preview.Fields[0].Name, "path"; got != want {
		t.Fatalf("Fields[0].Name = %q, want %q", got, want)
	}
	if got, want := preview.Fields[0].Value, "/project/output/result.json"; got != want {
		t.Fatalf("Fields[0].Value = %q, want %q", got, want)
	}
}

func TestPolicy_PreviewToolInput_Bash(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{ProjectRootOnly: true})

	preview, err := policy.previewToolInput("bash", map[string]any{
		"command": "echo hello",
		"cwd":     "subdir",
	})
	if err != nil {
		t.Fatalf("previewToolInput(bash) error = %v", err)
	}
	if got, want := preview.Tool, "bash"; got != want {
		t.Fatalf("Tool = %q, want %q", got, want)
	}
	if len(preview.Fields) != 2 {
		t.Fatalf("Fields len = %d, want 2", len(preview.Fields))
	}
	if got, want := preview.Fields[0].Name, "cwd"; got != want {
		t.Fatalf("Fields[0].Name = %q, want %q", got, want)
	}
	if got, want := preview.Fields[0].Value, "/project/subdir"; got != want {
		t.Fatalf("Fields[0].Value = %q, want %q", got, want)
	}
	if got, want := preview.Fields[1].Name, "command"; got != want {
		t.Fatalf("Fields[1].Name = %q, want %q", got, want)
	}
	if got, want := preview.Fields[1].Value, "echo hello"; got != want {
		t.Fatalf("Fields[1].Value = %q, want %q", got, want)
	}
}

func TestPolicy_PreviewToolInput_BlocksOutsideRoot(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{ProjectRootOnly: true})

	_, err := policy.previewToolInput("read", map[string]any{
		"path": "/etc/passwd",
	})
	if err == nil {
		t.Fatal("previewToolInput(read, /etc/passwd) = nil, want error")
	}
}

func TestPolicy_PreviewToolInput_BlocksNonWritable(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{
		ProjectRootOnly: true,
		WritablePaths:   []string{"output"},
	})

	_, err := policy.previewToolInput("write", map[string]any{
		"path": "src/main.go",
	})
	if err == nil {
		t.Fatal("previewToolInput(write, src/main.go) = nil, want error")
	}
}
