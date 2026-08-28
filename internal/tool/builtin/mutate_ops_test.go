package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestMutate_EffectivePolicy_AllowsPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	tmpDir := t.TempDir()
	tmpPath := filepath.Join(tmpDir, "outside.txt")

	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	env := Env{WorkDir: root, PathPolicy: &policy}
	toolDef := NewMutateTool(env)

	effectivePolicy := policy.WithoutRoot()

	ctx := context.WithValue(context.Background(), tool.EffectivePolicyKey{}, &effectivePolicy)
	result, err := toolDef.Handler(ctx, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": tmpPath, "content": "created outside root\n"},
		},
	})
	if err != nil {
		t.Fatalf("mutate Handler() error = %v", err)
	}

	mutateResult, ok := result.(*MutateResult)
	if !ok {
		t.Fatalf("mutate Handler() result = %T, want *MutateResult", result)
	}

	if mutateResult.OperationsFailed != 0 {
		t.Fatalf("OperationsFailed = %d, want 0; output=%q", mutateResult.OperationsFailed, mutateResult.Output)
	}

	if mutateResult.OperationsApplied != 1 {
		t.Fatalf("OperationsApplied = %d, want 1", mutateResult.OperationsApplied)
	}

	content, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("read created file: %v", err)
	}
	if string(content) != "created outside root\n" {
		t.Fatalf("file content = %q, want 'created outside root\\n'", string(content))
	}
}

func TestReplaceDiagnostics_NoMatch_IncludesOperationIndex(t *testing.T) {
	tests := []struct {
		name      string
		opIndex   float64
		initial   string
		oldString string
	}{
		{
			name:      "replace with no match at operation 3",
			opIndex:   float64(3),
			initial:   "line1\nline2\nline3\n",
			oldString: "nonexistent",
		},
		{
			name:      "replace with no match at operation 1",
			opIndex:   float64(1),
			initial:   "hello world",
			oldString: "goodbye",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "test.txt")
			if err := os.WriteFile(path, []byte(tt.initial), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			policy := tool.NewPathPolicy(root, config.PathsConfig{})
			toolDef := NewMutateTool(Env{WorkDir: root, PathPolicy: &policy})

			result, err := toolDef.Handler(context.Background(), map[string]any{
				"operations": []any{
					map[string]any{"type": "replace", "path": "test.txt", "old_string": tt.oldString, "new_string": "replacement"},
				},
			})
			if err != nil {
				t.Fatalf("mutate Handler() error = %v", err)
			}

			got, ok := result.(*MutateResult)
			if !ok {
				t.Fatalf("mutate Handler() result = %T, want *MutateResult", result)
			}

			if got.OperationsFailed != 1 {
				t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
			}

			operationIndexStr := strings.Split(got.Output, ":")[1]
			if !strings.Contains(operationIndexStr, "operation 1") {
				t.Errorf("error output does not contain 'operation 1 replace': %s", got.Output)
			}
		})
	}
}

func TestReplaceDiagnostics_AmbiguousMatch_IncludesOperationIndex(t *testing.T) {
	tests := []struct {
		name      string
		opIndex   float64
		initial   string
		oldString string
	}{
		{
			name:      "replace with ambiguous match at operation 2",
			opIndex:   float64(2),
			initial:   "hello\nworld\nhello\n",
			oldString: "hello",
		},
		{
			name:      "replace with multiple matches at operation 5",
			opIndex:   float64(5),
			initial:   "test test test",
			oldString: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "test.txt")
			if err := os.WriteFile(path, []byte(tt.initial), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			policy := tool.NewPathPolicy(root, config.PathsConfig{})
			toolDef := NewMutateTool(Env{WorkDir: root, PathPolicy: &policy})

			result, err := toolDef.Handler(context.Background(), map[string]any{
				"operations": []any{
					map[string]any{"type": "replace", "path": "test.txt", "old_string": tt.oldString, "new_string": "replacement"},
				},
			})
			if err != nil {
				t.Fatalf("mutate Handler() error = %v", err)
			}

			got, ok := result.(*MutateResult)
			if !ok {
				t.Fatalf("mutate Handler() result = %T, want *MutateResult", result)
			}

			if got.OperationsFailed != 1 {
				t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
			}

			if !strings.Contains(got.Output, "operation 1 replace") {
				t.Errorf("error output does not contain 'operation 1 replace': %s", got.Output)
			}
		})
	}
}
