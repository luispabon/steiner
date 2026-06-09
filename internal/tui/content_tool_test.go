package tui

import "testing"

<<<<<<< HEAD
func TestSummarizeMutateArgs(t *testing.T) {
=======
func TestSummarizeReadArgs(t *testing.T) {
>>>>>>> worktree-agent-ad658f1e3457e0017
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
<<<<<<< HEAD
			name: "single replace op",
			args: map[string]any{
				"operations": []any{
					map[string]any{"type": "replace", "path": "file.go"},
				},
			},
			expected: "replace file.go",
		},
		{
			name: "single move op",
			args: map[string]any{
				"operations": []any{
					map[string]any{"type": "move", "from": "a.go", "to": "b.go"},
				},
			},
			expected: "move a.go → b.go",
		},
		{
			name: "single create op",
			args: map[string]any{
				"operations": []any{
					map[string]any{"type": "create", "path": "new.go"},
				},
			},
			expected: "create new.go",
		},
		{
			name: "multi-op shows count",
			args: map[string]any{
				"operations": []any{
					map[string]any{"type": "replace", "path": "file.go"},
					map[string]any{"type": "delete", "path": "old.go"},
					map[string]any{"type": "create", "path": "new.go"},
				},
			},
			expected: "replace file.go (+2 more)",
		},
		{
			name:     "no operations",
			args:     map[string]any{},
			expected: "mutate",
		},
		{
			name: "op with no type",
			args: map[string]any{
				"operations": []any{
					map[string]any{"path": "file.go"},
				},
			},
			expected: "file.go",
=======
			name:     "read with path only (both defaults)",
			args:     map[string]any{"path": "file.go"},
			expected: "file.go:1–200",
		},
		{
			name:     "read with path and offset (limit defaults)",
			args:     map[string]any{"path": "file.go", "offset": 150.0},
			expected: "file.go:150–349",
		},
		{
			name:     "read with path, offset, and limit",
			args:     map[string]any{"path": "file.go", "offset": 150.0, "limit": 50.0},
			expected: "file.go:150–199",
		},
		{
			name:     "read with no path",
			args:     map[string]any{},
			expected: "read",
		},
		{
			name:     "read with file_path fallback",
			args:     map[string]any{"file_path": "main.go"},
			expected: "main.go:1–200",
		},
		{
			name:     "read with both path and file_path (path preferred)",
			args:     map[string]any{"path": "preferred.go", "file_path": "fallback.go"},
			expected: "preferred.go:1–200",
		},
		{
			name:     "read with offset=1 (explicit default)",
			args:     map[string]any{"path": "test.go", "offset": 1.0},
			expected: "test.go:1–200",
		},
		{
			name:     "read with large limit",
			args:     map[string]any{"path": "doc.txt", "limit": 1000.0},
			expected: "doc.txt:1–1000",
>>>>>>> worktree-agent-ad658f1e3457e0017
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
<<<<<<< HEAD
			result := summarizeMutateArgs(tt.args)
=======
			result := summarizeReadArgs(tt.args)
>>>>>>> worktree-agent-ad658f1e3457e0017
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}
<<<<<<< HEAD
=======

func TestSummarizeArgs(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		expected string
	}{
		{
			name:     "read tool dispatches to read handler",
			tool:     "read",
			args:     map[string]any{"path": "main.go"},
			expected: "main.go:1–200",
		},
		{
			name:     "read_file tool dispatches to read handler",
			tool:     "read_file",
			args:     map[string]any{"path": "config.yaml", "offset": 10.0, "limit": 30.0},
			expected: "config.yaml:10–39",
		},
		{
			name:     "read with case-insensitive tool name",
			tool:     "READ",
			args:     map[string]any{"path": "test.txt"},
			expected: "test.txt:1–200",
		},
		{
			name:     "mutate tool uses command key fallback",
			tool:     "mutate",
			args:     map[string]any{"operations": []any{}},
			expected: "mutate",
		},
		{
			name:     "nil args returns tool name",
			tool:     "bash",
			args:     nil,
			expected: "bash",
		},
		{
			name:     "empty args returns tool name",
			tool:     "bash",
			args:     map[string]any{},
			expected: "bash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := summarizeArgs(tt.tool, tt.args)
			if result != tt.expected {
				t.Errorf("summarizeArgs(%q, ...) = %q, want %q", tt.tool, result, tt.expected)
			}
		})
	}
}
>>>>>>> worktree-agent-ad658f1e3457e0017
