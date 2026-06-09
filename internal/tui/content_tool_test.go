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

func TestSummarizeGlobArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
			name:     "pattern and path together",
			args:     map[string]any{"pattern": "**/*.go", "path": "internal"},
			expected: "./internal/**/*.go",
		},
		{
			name:     "pattern and path with ./ prefix",
			args:     map[string]any{"pattern": "*.go", "path": "./src"},
			expected: "./src/*.go",
		},
		{
			name:     "pattern only",
			args:     map[string]any{"pattern": "**/*.go"},
			expected: "**/*.go",
		},
		{
			name:     "path only with ./ prefix",
			args:     map[string]any{"path": "./internal"},
			expected: "./internal",
		},
		{
			name:     "path only without ./ prefix",
			args:     map[string]any{"path": "internal"},
			expected: "./internal",
		},
		{
			name:     "empty args map",
			args:     map[string]any{},
			expected: "glob",
		},
		{
			name:     "path with trailing slash",
			args:     map[string]any{"pattern": "**/*.go", "path": "internal/"},
			expected: "./internal/**/*.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := summarizeGlobArgs(tt.args)
			if result != tt.expected {
				t.Errorf("summarizeGlobArgs(%v) = %q, want %q", tt.args, result, tt.expected)
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
			name:     "bash tool uses command key",
			tool:     "bash",
			args:     map[string]any{"command": "ls -la"},
			expected: "ls -la",
		},
		{
			name:     "grep with case-insensitive tool name",
			tool:     "GREP",
			args:     map[string]any{"pattern": "TODO"},
			expected: "'TODO'",
		},
		{
			name:     "glob tool dispatches to glob handler",
			tool:     "glob",
			args:     map[string]any{"pattern": "**/*.go", "path": "internal"},
			expected: "./internal/**/*.go",
		},
		{
			name:     "fetch_url shows url",
			tool:     "fetch_url",
			args:     map[string]any{"url": "https://example.com"},
			expected: "https://example.com",
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
		{
			name:     "url priority over fallback",
			tool:     "fetch_url",
			args:     map[string]any{"url": "https://example.com", "other": "value"},
			expected: "https://example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := summarizeArgs(tt.tool, tt.args)
			if result != tt.expected {
				t.Errorf("summarizeArgs(%q) = %q, want %q", tt.tool, result, tt.expected)
			}
		})
	}
}
