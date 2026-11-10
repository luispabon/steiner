package tool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/diagnostics"
)

// fakeOpsExtractor counts entries in the operations array under one fixed
// label, standing in for a tool's own extractor.
func fakeOpsExtractor(input map[string]any) map[string]int {
	ops, _ := input["operations"].([]any)
	counts := make(map[string]int, 1)
	if len(ops) > 0 {
		counts["create"] = len(ops)
	}
	return counts
}

// executeForOps runs one call and returns the decoded payload plus the raw
// tool.jsonl line, so tests can assert both decoded counts and field absence.
func executeForOps(t *testing.T, executor *Executor, diagDir, toolName string, input map[string]any) (toolRecordPayload, string, error) {
	t.Helper()
	_, err := executor.Execute(context.Background(), toolName, "", input)
	records := readRawToolRecords(t, diagDir)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	payload := decodeToolPayload(t, records[0].Payload)
	raw, readErr := os.ReadFile(filepath.Join(diagDir, "tool.jsonl"))
	if readErr != nil {
		t.Fatalf("read tool.jsonl: %v", readErr)
	}
	return payload, string(raw), err
}

func assertOpsRequested(t *testing.T, got *opsRequested, want map[string]int) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("ops_requested = %v, want the field omitted", *got)
		}
		return
	}
	if got == nil {
		t.Fatalf("ops_requested is absent, want %v", want)
	}
	if len(*got) != len(want) {
		t.Fatalf("ops_requested = %v, want %v", *got, want)
	}
	for label, count := range want {
		if (*got)[label] != count {
			t.Fatalf("ops_requested = %v, want %v", *got, want)
		}
	}
}

// TestExecuteOpsRequested covers the outcomes a call can end in: counts are
// derived from raw input, so they survive denials and failures, and ops_total
// keeps its existing meaning.
func TestExecuteOpsRequested(t *testing.T) {
	tests := []struct {
		name         string
		tool         string
		handler      func(context.Context, map[string]any) (any, error)
		extract      OpsRequestedFunc
		register     bool
		input        map[string]any
		wantCounts   map[string]int
		wantOutcome  string
		wantOpsTotal int
	}{
		{
			name:         "success",
			tool:         "mutate",
			handler:      okHandler,
			extract:      fakeOpsExtractor,
			register:     true,
			input:        map[string]any{"operations": []any{map[string]any{"type": "create"}, map[string]any{"type": "write"}}},
			wantCounts:   map[string]int{"create": 2},
			wantOutcome:  "ok",
			wantOpsTotal: 2,
		},
		{
			name:         "handler failure keeps counts",
			tool:         "mutate",
			handler:      func(context.Context, map[string]any) (any, error) { return nil, errors.New("boom") },
			extract:      fakeOpsExtractor,
			register:     true,
			input:        map[string]any{"operations": []any{map[string]any{"type": "delete_file"}}},
			wantCounts:   map[string]int{"create": 1},
			wantOutcome:  "error",
			wantOpsTotal: 1,
		},
		{
			name:         "policy denial keeps counts",
			tool:         "mutate",
			handler:      okHandler,
			extract:      fakeOpsExtractor,
			register:     true,
			input:        map[string]any{"operations": []any{map[string]any{"type": "write", "path": "/etc/passwd", "content": "x"}}},
			wantCounts:   map[string]int{"create": 1},
			wantOutcome:  "denied",
			wantOpsTotal: 1,
		},
		{
			name:         "empty operations array emits {}",
			tool:         "mutate",
			handler:      okHandler,
			extract:      fakeOpsExtractor,
			register:     true,
			input:        map[string]any{"operations": []any{}},
			wantCounts:   map[string]int{},
			wantOutcome:  "ok",
			wantOpsTotal: 0,
		},
		{
			name:         "nil extractor result omits the field",
			tool:         "mutate",
			handler:      okHandler,
			extract:      func(map[string]any) map[string]int { return nil },
			register:     true,
			input:        map[string]any{"operations": "not-an-array"},
			wantCounts:   nil,
			wantOutcome:  "ok",
			wantOpsTotal: 1,
		},
		{
			name:         "tool without an extractor",
			tool:         "read",
			handler:      okHandler,
			register:     true,
			input:        map[string]any{"operations": []any{map[string]any{"type": "create"}}},
			wantCounts:   nil,
			wantOutcome:  "ok",
			wantOpsTotal: 1,
		},
		{
			name:         "unregistered tool",
			tool:         "mutate",
			register:     false,
			input:        map[string]any{"operations": []any{map[string]any{"type": "create"}}},
			wantCounts:   nil,
			wantOutcome:  "error",
			wantOpsTotal: 1,
		},
		{
			name:         "oversized label set",
			tool:         "mutate",
			handler:      okHandler,
			extract:      manyLabelsExtractor,
			register:     true,
			input:        map[string]any{"operations": []any{map[string]any{"type": "create"}}},
			wantCounts:   nil,
			wantOutcome:  "ok",
			wantOpsTotal: 1,
		},
		{
			name:    "labels that are not fixed identifiers",
			tool:    "mutate",
			handler: okHandler,
			extract: func(map[string]any) map[string]int {
				return map[string]int{"/etc/passwd": 1, "secret content": 2, "UPPER": 3}
			},
			register:     true,
			input:        map[string]any{"operations": []any{map[string]any{"type": "create"}}},
			wantCounts:   nil,
			wantOutcome:  "ok",
			wantOpsTotal: 1,
		},
		{
			name:         "negative count",
			tool:         "mutate",
			handler:      okHandler,
			extract:      func(map[string]any) map[string]int { return map[string]int{"create": -1} },
			register:     true,
			input:        map[string]any{"operations": []any{map[string]any{"type": "create"}}},
			wantCounts:   nil,
			wantOutcome:  "ok",
			wantOpsTotal: 1,
		},
		{
			name:         "panicking extractor cannot break the call",
			tool:         "mutate",
			handler:      okHandler,
			extract:      func(map[string]any) map[string]int { panic("extractor bug") },
			register:     true,
			input:        map[string]any{"operations": []any{map[string]any{"type": "create"}}},
			wantCounts:   nil,
			wantOutcome:  "ok",
			wantOpsTotal: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			var defs []ToolDef
			if tt.register {
				defs = append(defs, ToolDef{Name: tt.tool, Handler: tt.handler, OpsRequested: tt.extract})
			}
			executor, diagDir := newToolStreamExecutor(t, NewRegistry(defs...), root)

			payload, raw, err := executeForOps(t, executor, diagDir, tt.tool, tt.input)
			if tt.wantOutcome == "ok" && err != nil {
				t.Fatalf("Execute() error = %v, want success", err)
			}
			if tt.wantOutcome != "ok" && err == nil {
				t.Fatalf("Execute() error = nil, want %q", tt.wantOutcome)
			}
			if payload.Outcome != tt.wantOutcome {
				t.Errorf("Outcome = %q, want %q", payload.Outcome, tt.wantOutcome)
			}
			if payload.OpsTotal != tt.wantOpsTotal {
				t.Errorf("OpsTotal = %d, want %d", payload.OpsTotal, tt.wantOpsTotal)
			}
			assertOpsRequested(t, payload.OpsRequested, tt.wantCounts)
			if tt.wantCounts == nil && strings.Contains(raw, `"ops_requested"`) {
				t.Errorf("tool.jsonl carries ops_requested, want it omitted:\n%s", raw)
			}
			if tt.wantCounts != nil && len(tt.wantCounts) == 0 && !strings.Contains(raw, `"ops_requested":{}`) {
				t.Errorf("tool.jsonl omits the empty ops_requested map:\n%s", raw)
			}
		})
	}
}

func okHandler(context.Context, map[string]any) (any, error) { return "ok", nil }

// manyLabelsExtractor returns more labels than the payload may carry.
func manyLabelsExtractor(map[string]any) map[string]int {
	counts := make(map[string]int, maxRequestedOpLabels+1)
	for i := 0; i <= maxRequestedOpLabels; i++ {
		counts["op"+string(rune('a'+i))] = 1
	}
	return counts
}

// TestOpsRequestedExtractorRunsOnlyWithToolStream asserts the extractor is
// gated exactly like the record it feeds: no writer or another stream only
// means it never runs.
func TestOpsRequestedExtractorRunsOnlyWithToolStream(t *testing.T) {
	tests := []struct {
		name    string
		streams diagnostics.Streams
		writer  bool
		wantRun int
	}{
		{name: "no writer", writer: false, wantRun: 0},
		{name: "tool stream disabled", writer: true, streams: diagnostics.Streams{TUI: true}, wantRun: 0},
		{name: "tool stream enabled", writer: true, streams: diagnostics.Streams{Tool: true}, wantRun: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			runs := 0
			reg := NewRegistry(ToolDef{
				Name:    "mutate",
				Handler: okHandler,
				OpsRequested: func(map[string]any) map[string]int {
					runs++
					return map[string]int{"create": 1}
				},
			})
			executor := NewExecutor(reg, rootOnlyConfig(), nil, root, "", Unsandboxed{})
			if tt.writer {
				writer, err := diagnostics.New(diagnostics.Options{Dir: t.TempDir(), Streams: tt.streams})
				if err != nil {
					t.Fatalf("diagnostics.New() error = %v", err)
				}
				t.Cleanup(func() { _ = writer.Close() })
				executor.WithDiagnostics(writer)
			}
			if _, err := executor.Execute(context.Background(), "mutate", "", map[string]any{
				"operations": []any{map[string]any{"type": "create"}},
			}); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if runs != tt.wantRun {
				t.Errorf("extractor ran %d times, want %d", runs, tt.wantRun)
			}
		})
	}
}

// TestOpsRequestedChildScope asserts a sub-agent executor records counts
// alongside the child's attribution.
func TestOpsRequestedChildScope(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(ToolDef{Name: "mutate", Handler: okHandler, OpsRequested: fakeOpsExtractor})
	executor, diagDir := newToolStreamExecutor(t, reg, root)
	executor.WithDiagnosticsScope(diagnostics.SourceSubAgent, "agent-3", "code")

	payload, _, err := executeForOps(t, executor, diagDir, "mutate", map[string]any{
		"operations": []any{map[string]any{"type": "create"}, map[string]any{"type": "move"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	assertOpsRequested(t, payload.OpsRequested, map[string]int{"create": 2})

	records := readRawToolRecords(t, diagDir)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	got := records[0]
	if got.Source != diagnostics.SourceSubAgent || got.AgentID != "agent-3" || got.AgentType != "code" {
		t.Errorf("envelope = source=%q agent_id=%q agent_type=%q, want sub_agent/agent-3/code", got.Source, got.AgentID, got.AgentType)
	}
}

// TestSanitizeRequestedOps pins the bounds applied to any extractor result.
func TestSanitizeRequestedOps(t *testing.T) {
	longLabel := strings.Repeat("a", maxOpLabelLen)
	tests := []struct {
		name string
		raw  map[string]int
		want *opsRequested
	}{
		{name: "nil", raw: nil, want: nil},
		{name: "empty map", raw: map[string]int{}, want: &opsRequested{}},
		{
			name: "fixed identifiers survive",
			raw:  map[string]int{"create": 2, "delete_file": 1, longLabel: 1, "op2": 1},
			want: &opsRequested{"create": 2, "delete_file": 1, longLabel: 1, "op2": 1},
		},
		{
			name: "non-identifier labels dropped",
			raw:  map[string]int{"create": 1, "/etc/passwd": 1, "with space": 1, "UPPER": 1, "": 1, "1st": 1, "_x": 1, strings.Repeat("a", maxOpLabelLen+1): 1},
			want: &opsRequested{"create": 1},
		},
		{
			name: "only invalid labels omits the field",
			raw:  map[string]int{"/etc/passwd": 1},
			want: nil,
		},
		{
			name: "negative counts dropped",
			raw:  map[string]int{"create": -2},
			want: nil,
		},
		{
			name: "oversized label set omits the field",
			raw:  manyLabelsExtractor(nil),
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeRequestedOps(tt.raw)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("sanitizeRequestedOps() = %v, want nil", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("sanitizeRequestedOps() = nil, want %v", *tt.want)
			}
			if len(*got) != len(*tt.want) {
				t.Fatalf("sanitizeRequestedOps() = %v, want %v", *got, *tt.want)
			}
			for label, count := range *tt.want {
				if (*got)[label] != count {
					t.Fatalf("sanitizeRequestedOps() = %v, want %v", *got, *tt.want)
				}
			}
		})
	}
}
