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

// TestInvokeToolInjectsFileReadLookup pins that a mutate call dispatched through
// invokeTool sees a FileReadLookup bound to the run's ContextManager, marked
// Known, and evaluated at the current turn.
func TestInvokeToolInjectsFileReadLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	readJSON, err := json.Marshal(readResult{Path: path, StartLine: 1, EndLine: 2, TotalLines: 2, Output: "one\ntwo\n"})
	if err != nil {
		t.Fatalf("marshal read result: %v", err)
	}

	manager := NewContextStateManager()
	manager.ObserveToolResult(1, "read", nil, string(readJSON))

	var got tool.FileReadState
	var sawLookup bool
	executor := parallelTestExecutor{fn: func(ctx context.Context, _ string) (any, error) {
		lookup := tool.FileReadLookupFromContext(ctx)
		if lookup == nil {
			return "mutated", nil
		}
		sawLookup = true
		got = lookup(path)
		return "mutated", nil
	}}

	req := RunRequest{
		Executor:       executor,
		ContextManager: manager,
		Events:         output.NoopSink{},
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)
	calls := provider.ChatResponse{Message: provider.Message{ToolCalls: []provider.ToolCall{
		{ID: "1", Name: "mutate"},
	}}}
	p.executeToolCalls(context.Background(), RunState{TurnCount: 4, Lineage: newConversationLineage(nil)}, calls)

	if !sawLookup {
		t.Fatal("FileReadLookupFromContext() = nil, want a lookup bound to the run's ContextManager")
	}
	want := tool.FileReadState{Known: true, Observed: true, StartLine: 1, EndLine: 2, TotalLines: 2, TurnsSinceRead: 3}
	if got != want {
		t.Errorf("lookup state = %+v, want %+v", got, want)
	}
}

// TestInvokeToolOmitsFileReadLookupWithoutContextManager is a control for
// TestInvokeToolInjectsFileReadLookup: with no ContextManager the guard stays
// off and no lookup is injected.
func TestInvokeToolOmitsFileReadLookupWithoutContextManager(t *testing.T) {
	var sawLookup bool
	executor := parallelTestExecutor{fn: func(ctx context.Context, _ string) (any, error) {
		sawLookup = tool.FileReadLookupFromContext(ctx) != nil
		return "mutated", nil
	}}

	req := RunRequest{Executor: executor, Events: output.NoopSink{}}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)
	calls := provider.ChatResponse{Message: provider.Message{ToolCalls: []provider.ToolCall{
		{ID: "1", Name: "mutate"},
	}}}
	p.executeToolCalls(context.Background(), RunState{Lineage: newConversationLineage(nil)}, calls)

	if sawLookup {
		t.Fatal("FileReadLookupFromContext() was non-nil with no ContextManager, want nil")
	}
}
