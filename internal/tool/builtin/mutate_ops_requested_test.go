package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/tool"
)

func assertOpCounts(t *testing.T, got, want map[string]int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("counts = %v, want %v", got, want)
	}
	for label, count := range want {
		if got[label] != count {
			t.Fatalf("counts = %v, want %v", got, want)
		}
	}
}

// TestMutateOpsRequested pins the extractor's label set and its handling of
// malformed input: every label comes from validOpTypes or invalid.
func TestMutateOpsRequested(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		want  map[string]int
		nil   bool
	}{
		{name: "nil input", input: nil, nil: true},
		{name: "operations absent", input: map[string]any{}, nil: true},
		{name: "operations not an array", input: map[string]any{"operations": "[{\"type\":\"create\"}]"}, nil: true},
		{name: "operations null", input: map[string]any{"operations": nil}, nil: true},
		{name: "empty operations", input: map[string]any{"operations": []any{}}, want: map[string]int{}},
		{
			name: "valid types accumulate",
			input: map[string]any{"operations": []any{
				map[string]any{"type": "create"},
				map[string]any{"type": "create"},
				map[string]any{"type": "move"},
			}},
			want: map[string]int{"create": 2, "move": 1},
		},
		{
			name: "types are trimmed like the planner",
			input: map[string]any{"operations": []any{
				map[string]any{"type": "  replace\t"},
				map[string]any{"type": "delete_file "},
			}},
			want: map[string]int{"replace": 1, "delete_file": 1},
		},
		{
			name: "unknown, non-string, and missing types are invalid",
			input: map[string]any{"operations": []any{
				map[string]any{"type": "rename"},
				map[string]any{"type": ""},
				map[string]any{"type": 42},
				map[string]any{"path": "a.txt"},
				"not-an-object",
				nil,
				float64(1),
			}},
			want: map[string]int{"invalid": 7},
		},
		{
			name: "field values never become labels",
			input: map[string]any{"operations": []any{
				map[string]any{"type": "create", "path": "secret.go", "content": "package secret"},
				map[string]any{"type": "move", "from": "/etc/passwd", "to": "out.txt"},
			}},
			want: map[string]int{"create": 1, "move": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mutateOpsRequested(tt.input)
			if tt.nil {
				if got != nil {
					t.Fatalf("mutateOpsRequested() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("mutateOpsRequested() = nil, want %v", tt.want)
			}
			assertOpCounts(t, got, tt.want)
			for label := range got {
				if label == invalidOpLabel {
					continue
				}
				if _, ok := allowedFields[label]; !ok {
					t.Errorf("label %q is not one of the tool's fixed operation types", label)
				}
			}
		})
	}
}

func TestNewMutateToolSetsOpsRequested(t *testing.T) {
	def := NewMutateTool(Env{})
	if def.OpsRequested == nil {
		t.Fatal("NewMutateTool() has no OpsRequested extractor")
	}
	got := def.OpsRequested(map[string]any{"operations": []any{map[string]any{"type": "create"}}})
	assertOpCounts(t, got, map[string]int{"create": 1})
}

// mutateStreamExecutor wires the real mutate tool through an executor with the
// tool diagnostics stream on, mirroring production wiring.
func mutateStreamExecutor(t *testing.T, root string) (*tool.Executor, string) {
	t.Helper()
	diagDir := t.TempDir()
	writer, err := diagnostics.New(diagnostics.Options{Dir: diagDir, Streams: diagnostics.Streams{Tool: true}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	policy := tool.NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true})
	def := NewMutateTool(Env{WorkDir: root, PathPolicy: &policy, FileObserved: func(string) bool { return true }})
	executor := tool.NewExecutor(tool.NewRegistry(def), config.Config{Paths: config.PathsConfig{ProjectRootOnly: true}}, nil, root, "", tool.Unsandboxed{})
	executor.WithDiagnostics(writer)
	return executor, diagDir
}

// readMutateRecord returns the single tool payload from dir and the raw line.
func readMutateRecord(t *testing.T, dir string) (map[string]any, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "tool.jsonl"))
	if err != nil {
		t.Fatalf("read tool.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("tool.jsonl has %d lines, want 1:\n%s", len(lines), raw)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	payload, ok := rec["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %v, want an object", rec["payload"])
	}
	return payload, lines[0]
}

// TestMutateOpsRequestedToolStream checks recorded counts across success,
// denial, failure, and malformed input, and that no path or content reaches the
// stream through ops_requested.
func TestMutateOpsRequestedToolStream(t *testing.T) {
	tests := []struct {
		name         string
		operations   any
		wantOutcome  string
		wantErr      bool
		wantOpsTotal float64
		wantCounts   map[string]any
		wantAbsent   bool
		secrets      []string
	}{
		{
			name: "success",
			operations: []any{
				map[string]any{"type": "create", "path": "a.txt", "content": "hello-a"},
				map[string]any{"type": "create", "path": "b.txt", "content": "hello-b"},
			},
			wantOutcome:  "ok",
			wantOpsTotal: 2,
			wantCounts:   map[string]any{"create": float64(2)},
			secrets:      []string{"a.txt", "b.txt", "hello-a", "hello-b"},
		},
		{
			name: "partial failure",
			operations: []any{
				map[string]any{"type": "create", "path": "a.txt", "content": "hello"},
				map[string]any{"type": "delete_file", "path": "missing.txt"},
			},
			wantOutcome:  "error",
			wantOpsTotal: 2,
			wantCounts:   map[string]any{"create": float64(1), "delete_file": float64(1)},
			secrets:      []string{"a.txt", "missing.txt", "hello"},
		},
		{
			name:         "policy denial",
			operations:   []any{map[string]any{"type": "write", "path": "/etc/steiner-ops-requested", "content": "secret-body"}},
			wantOutcome:  "denied",
			wantErr:      true,
			wantOpsTotal: 1,
			wantCounts:   map[string]any{"write": float64(1)},
			secrets:      []string{"/etc/steiner-ops-requested", "secret-body"},
		},
		{
			name: "mixed valid and invalid types",
			operations: []any{
				map[string]any{"type": "create", "path": "a.txt", "content": "x"},
				map[string]any{"type": "rename"},
				map[string]any{"path": "b.txt"},
				map[string]any{"type": 42},
				map[string]any{"type": "  move  ", "from": "a.txt", "to": "c.txt"},
				map[string]any{"type": "delete_file", "path": "d.txt"},
			},
			wantOutcome:  "error",
			wantOpsTotal: 6,
			wantCounts:   map[string]any{"create": float64(1), "move": float64(1), "delete_file": float64(1), "invalid": float64(3)},
			secrets:      []string{"a.txt", "b.txt", "c.txt", "d.txt"},
		},
		{
			name:         "double-encoded operations",
			operations:   `[{"type":"create","path":"a.txt","content":"x"}]`,
			wantOutcome:  "error",
			wantErr:      true,
			wantOpsTotal: 1,
			wantAbsent:   true,
			secrets:      []string{"a.txt"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			executor, diagDir := mutateStreamExecutor(t, root)

			_, err := executor.Execute(context.Background(), "mutate", "call-1", map[string]any{"operations": tt.operations})
			if tt.wantErr && err == nil {
				t.Fatalf("Execute() error = nil, want an error for outcome %q", tt.wantOutcome)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Execute() error = %v, want no error", err)
			}

			payload, line := readMutateRecord(t, diagDir)
			if got := payload["outcome"]; got != tt.wantOutcome {
				t.Errorf("payload[outcome] = %v, want %q", got, tt.wantOutcome)
			}
			if got := payload["ops_total"]; got != tt.wantOpsTotal {
				t.Errorf("payload[ops_total] = %v, want %v", got, tt.wantOpsTotal)
			}

			rawCounts, present := payload["ops_requested"]
			if tt.wantAbsent {
				if present {
					t.Fatalf("ops_requested = %v, want the field omitted", rawCounts)
				}
			} else {
				counts, ok := rawCounts.(map[string]any)
				if !ok {
					t.Fatalf("ops_requested = %v, want an object", rawCounts)
				}
				if len(counts) != len(tt.wantCounts) {
					t.Fatalf("ops_requested = %v, want %v", counts, tt.wantCounts)
				}
				for label, want := range tt.wantCounts {
					if counts[label] != want {
						t.Errorf("ops_requested = %v, want %v", counts, tt.wantCounts)
					}
				}
			}

			for _, secret := range append(tt.secrets, root) {
				if strings.Contains(line, secret) {
					t.Errorf("tool record contains %q:\n%s", secret, line)
				}
			}
		})
	}
}
