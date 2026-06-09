package tui

import "testing"

func TestSummarizeGrepArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
			name: "grep with pattern, path, and glob",
			args: map[string]any{
				"pattern": "func.*Schema",
				"path":    "internal",
				"glob":    "*.go",
			},
			expected: "'func.*Schema' in ./internal/*.go",
		},
		{
			name: "grep with pattern and path",
			args: map[string]any{
				"pattern": "TODO",
				"path":    "internal",
			},
			expected: "'TODO' in ./internal",
		},
		{
			name: "grep with pattern only",
			args: map[string]any{
				"pattern": "TODO",
			},
			expected: "'TODO'",
		},
		{
			name:     "grep with no args",
			args:     map[string]any{},
			expected: "grep",
		},
		{
			name: "grep with empty pattern",
			args: map[string]any{
				"pattern": "",
			},
			expected: "grep",
		},
		{
			name: "grep with pattern and path with leading ./",
			args: map[string]any{
				"pattern": "error",
				"path":    "./cmd",
			},
			expected: "'error' in ./cmd",
		},
		{
			name: "grep with pattern and absolute path",
			args: map[string]any{
				"pattern": "config",
				"path":    "/etc/config",
			},
			expected: "'config' in /etc/config",
		},
		{
			name: "grep with pattern, path ending with slash, and glob",
			args: map[string]any{
				"pattern": "test",
				"path":    "testdata/",
				"glob":    "*.json",
			},
			expected: "'test' in ./testdata/*.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := summarizeGrepArgs(tt.args)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSummarizeArgs(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		expected string
	}{
		{
			name: "grep tool dispatches to grep handler",
			tool: "grep",
			args: map[string]any{
				"pattern": "func.*Schema",
				"path":    "internal",
				"glob":    "*.go",
			},
			expected: "'func.*Schema' in ./internal/*.go",
		},
		{
			name: "bash tool uses generic path",
			tool: "bash",
			args: map[string]any{
				"command": "ls -la",
			},
			expected: "ls -la",
		},
		{
			name: "grep with case-insensitive tool name",
			tool: "GREP",
			args: map[string]any{
				"pattern": "TODO",
			},
			expected: "'TODO'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := summarizeArgs(tt.tool, tt.args)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}
