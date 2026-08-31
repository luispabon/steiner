package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// TestSameTurnReadThenMutateObservesRead pins that a same-turn [read A,
// mutate A] batch always resolves the read's observation before mutate
// dispatches. Step 2 pushes agents to batch independent tool calls, and
// step 7's mutate replace guard rejects unobserved edits — this depends on
// the read landing in FileTracker before mutate's invokeTool call, which
// today holds because mutate is not parallel-safe: parallelRunLength breaks
// the batch there, so the read's result (and its ObserveRead call) is fully
// applied before the next run is dispatched. A future reordering of the
// turn loop that broke this would silently defeat step 7 against step 2's
// batching guidance, so pin it here.
func TestSameTurnReadThenMutateObservesRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	readJSON, err := json.Marshal(struct {
		Path       string `json:"path"`
		StartLine  int    `json:"start_line"`
		EndLine    int    `json:"end_line"`
		TotalLines int    `json:"total_lines"`
		Output     string `json:"output"`
	}{Path: path, StartLine: 1, EndLine: 1, TotalLines: 1, Output: "one\n"})
	if err != nil {
		t.Fatalf("marshal read result: %v", err)
	}

	var observedAtMutate bool
	executor := parallelTestExecutor{fn: func(ctx context.Context, name string) (any, error) {
		switch name {
		case "read":
			return string(readJSON), nil
		case "mutate":
			checker := tool.FileObservedCheckerFromContext(ctx)
			observedAtMutate = checker != nil && checker(path)
			return "mutated", nil
		default:
			t.Fatalf("unexpected tool call %q", name)
			return nil, nil
		}
	}}

	req := RunRequest{
		Executor: executor,
		ParallelClassOf: func(name string) ParallelClass {
			if name == "read" {
				return ParallelClassTool
			}
			return ParallelClassNone
		},
		MaxParallelTools: 1,
		ContextManager:   NewContextStateManager(),
		Events:           output.NoopSink{},
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)
	calls := provider.ChatResponse{Message: provider.Message{ToolCalls: []provider.ToolCall{
		{ID: "1", Name: "read"},
		{ID: "2", Name: "mutate"},
	}}}
	p.executeToolCalls(context.Background(), RunState{Lineage: newConversationLineage(nil)}, calls)

	if !observedAtMutate {
		t.Fatal("mutate's FileObservedChecker reported path as unobserved, want observed after a same-turn preceding read")
	}
}

// TestMutateAloneDoesNotObserveUnreadPath is a control for
// TestSameTurnReadThenMutateObservesRead: without a preceding read, the
// checker must report the path as unobserved.
func TestMutateAloneDoesNotObserveUnreadPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")

	var observedAtMutate bool
	var checkerWasNil bool
	executor := parallelTestExecutor{fn: func(ctx context.Context, _ string) (any, error) {
		checker := tool.FileObservedCheckerFromContext(ctx)
		checkerWasNil = checker == nil
		observedAtMutate = checker != nil && checker(path)
		return "mutated", nil
	}}

	req := RunRequest{
		Executor:       executor,
		ContextManager: NewContextStateManager(),
		Events:         output.NoopSink{},
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)
	calls := provider.ChatResponse{Message: provider.Message{ToolCalls: []provider.ToolCall{
		{ID: "1", Name: "mutate"},
	}}}
	p.executeToolCalls(context.Background(), RunState{Lineage: newConversationLineage(nil)}, calls)

	if checkerWasNil {
		t.Fatal("FileObservedChecker was nil, want a checker bound to the run's ContextManager")
	}
	if observedAtMutate {
		t.Fatal("checker reported path as observed with no preceding read, want unobserved")
	}
}
