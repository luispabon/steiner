package delegation

import "testing"

func TestParseStructuredDelegateBriefWithObjective(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"objective":        "implement feature X",
		"context":          "background info",
		"deliverable":      "working code",
		"constraints":      []any{"constraint 1", "constraint 2"},
		"success_criteria": []any{"success 1"},
		"checks":           []any{},
	}

	b, ok := ParseStructuredDelegateBrief(args)
	if !ok {
		t.Fatal("ParseStructuredDelegateBrief returned ok=false, want true")
	}
	if b.Objective != "implement feature X" {
		t.Errorf("objective = %q, want %q", b.Objective, "implement feature X")
	}
	if b.Context != "background info" {
		t.Errorf("context = %q, want %q", b.Context, "background info")
	}
	if b.Deliverable != "working code" {
		t.Errorf("deliverable = %q, want %q", b.Deliverable, "working code")
	}
	if len(b.Constraints) != 2 {
		t.Errorf("constraints length = %d, want 2", len(b.Constraints))
	}
	if len(b.SuccessCriteria) != 1 {
		t.Errorf("SuccessCriteria length = %d, want 1", len(b.SuccessCriteria))
	}
	if b.Checks != nil {
		t.Errorf("checks = %v, want nil", b.Checks)
	}
}

func TestParseStructuredDelegateBriefObjectiveBlank(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"objective": "  ",
		"context":   "background",
	}

	_, ok := ParseStructuredDelegateBrief(args)
	if ok {
		t.Error("ParseStructuredDelegateBrief returned ok=true, want false (blank objective)")
	}
}

func TestParseStructuredDelegateBriefNoObjective(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"context": "background",
	}

	_, ok := ParseStructuredDelegateBrief(args)
	if ok {
		t.Error("ParseStructuredDelegateBrief returned ok=true, want false (missing objective)")
	}
}

func TestParseStructuredDelegateBriefFiltersEmptyStrings(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"objective":   "task",
		"constraints": []any{"c1", "  ", "c2", ""},
	}

	b, ok := ParseStructuredDelegateBrief(args)
	if !ok {
		t.Fatal("ParseStructuredDelegateBrief returned ok=false, want true")
	}
	if len(b.Constraints) != 2 {
		t.Errorf("constraints length = %d, want 2 (empty strings filtered)", len(b.Constraints))
	}
}

func TestDelegateArgTextIncludesObjective(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"objective": "primary goal",
		"task":      "secondary",
	}

	result := DelegateArgText(args)
	if result != "primary goal" {
		t.Errorf("DelegateArgText = %q, want %q (objective takes priority)", result, "primary goal")
	}
}

func TestSummarizeGrepArgs(t *testing.T) {
	t.Parallel()
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
			name:     "grep with pattern and path",
			args:     map[string]any{"pattern": "TODO", "path": "internal"},
			expected: "'TODO' in ./internal",
		},
		{
			name:     "grep with pattern only",
			args:     map[string]any{"pattern": "TODO"},
			expected: "'TODO'",
		},
		{
			name:     "grep with no args",
			args:     map[string]any{},
			expected: "grep",
		},
		{
			name:     "grep with empty pattern",
			args:     map[string]any{"pattern": ""},
			expected: "grep",
		},
		{
			name:     "grep with pattern and path with leading ./",
			args:     map[string]any{"pattern": "error", "path": "./cmd"},
			expected: "'error' in ./cmd",
		},
		{
			name:     "grep with pattern and absolute path",
			args:     map[string]any{"pattern": "config", "path": "/etc/config"},
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
			t.Parallel()
			result := summarizeGrepArgs(tt.args)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSummarizeGlobArgs(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			result := summarizeGlobArgs(tt.args)
			if result != tt.expected {
				t.Errorf("summarizeGlobArgs(%v) = %q, want %q", tt.args, result, tt.expected)
			}
		})
	}
}

func TestSummarizeReadArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := summarizeReadArgs(tt.args)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSummarizeLSArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
			name:     "ls with path",
			args:     map[string]any{"path": "./mydir"},
			expected: "./mydir",
		},
		{
			name:     "ls with path + recursive=true",
			args:     map[string]any{"path": "./mydir", "recursive": true},
			expected: "./mydir (recursive)",
		},
		{
			name:     "ls with path + recursive=false",
			args:     map[string]any{"path": "./mydir", "recursive": false},
			expected: "./mydir",
		},
		{
			name:     "ls with no args",
			args:     map[string]any{},
			expected: ".",
		},
		{
			name:     "ls with recursive=true only",
			args:     map[string]any{"recursive": true},
			expected: ". (recursive)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := summarizeLSArgs(tt.args)
			if result != tt.expected {
				t.Errorf("summarizeLSArgs(%v) = %q, want %q", tt.args, result, tt.expected)
			}
		})
	}
}

func TestSummarizeMutateArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := summarizeMutateArgs(tt.args)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSummarizeArgsLegacyDelegateNamesUseGenericFallback(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tool string
		args map[string]any
		want string
	}{
		{
			tool: "explore",
			args: map[string]any{"task": "look into the auth module", "path": "auth"},
			want: "auth",
		},
		{
			tool: "research",
			args: map[string]any{"task": "find all usages of X", "path": "usages"},
			want: "usages",
		},
		{
			tool: "code",
			args: map[string]any{"task": "implement the retry logic", "path": "retry"},
			want: "retry",
		},
		{
			tool: "evaluate",
			args: map[string]any{"task": "plan the migration", "path": "migration"},
			want: "migration",
		},
		{
			tool: "sanity_check",
			args: map[string]any{"task": "run all tests and confirm green", "path": "tests"},
			want: "tests",
		},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			t.Parallel()
			got := SummarizeToolArgs(tt.tool, tt.args)
			if got != tt.want {
				t.Errorf("SummarizeToolArgs(%q, ...) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestSummarizeArgsSubAgentUsesObjective(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"objective": "primary objective",
		"task":      "fallback task",
	}

	if got := SummarizeToolArgs("sub_agent", args); got != "primary objective" {
		t.Errorf("SummarizeToolArgs(%q, ...) = %q, want objective %q", "sub_agent", got, "primary objective")
	}
}

func TestSummarizeToolArgs(t *testing.T) {
	t.Parallel()
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
			name:     "nil args returns empty summary",
			tool:     "bash",
			args:     nil,
			expected: "",
		},
		{
			name:     "empty args returns empty summary",
			tool:     "bash",
			args:     map[string]any{},
			expected: "",
		},
		{
			name:     "mcp tool summarizes non-empty args",
			tool:     "mcp__server__tool",
			args:     map[string]any{"query": "issues"},
			expected: "issues",
		},
		{
			name:     "empty mcp args returns empty summary",
			tool:     "mcp__server__tool",
			args:     map[string]any{},
			expected: "",
		},
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
			name:     "ls tool dispatches to ls handler",
			tool:     "ls",
			args:     map[string]any{"path": "./src", "recursive": true},
			expected: "./src (recursive)",
		},
		{
			name: "mutate tool shows op type",
			tool: "mutate",
			args: map[string]any{
				"operations": []any{
					map[string]any{"type": "replace", "path": "file.go"},
				},
			},
			expected: "replace file.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := SummarizeToolArgs(tt.tool, tt.args)
			if result != tt.expected {
				t.Errorf("SummarizeToolArgs(%q) = %q, want %q", tt.tool, result, tt.expected)
			}
		})
	}
}
