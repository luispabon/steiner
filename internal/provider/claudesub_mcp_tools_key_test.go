package provider

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"
)

func TestClaudeSubToolsKeyRejectsUnmarshalableSchema(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
	}{
		{name: "NaN default", schema: map[string]any{"default": math.NaN()}},
		{name: "func default", schema: map[string]any{"default": func() {}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			specs := []ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: tt.schema}}}
			if key, err := claudeSubToolsKey(specs); err == nil {
				t.Fatalf("claudeSubToolsKey() = %q, nil; want marshal error", key)
			}
		})
	}
}

func TestClaudeSubMCPSetToolsKeepsPublishedSetOnMarshalError(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	valid := []ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: claudeSubMCPObjectSchema(nil)}}}
	if err := h.setTools(valid); err != nil {
		t.Fatalf("setTools(read): %v", err)
	}
	h.mu.Lock()
	before := h.toolKey
	h.mu.Unlock()

	bad := []ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: map[string]any{"default": math.NaN()}}}}
	if err := h.setTools(bad); err == nil {
		t.Fatal("setTools(NaN schema) error = nil, want marshal error")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.toolKey != before {
		t.Fatalf("toolKey = %q after failed setTools, want unchanged %q", h.toolKey, before)
	}
}

// A session whose initial tool set cannot be marshalled must fail to start
// before spawning the CLI, instead of silently starting without tools.
func TestClaudeSubAcquireSurfacesInitialToolSyncError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)

	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: map[string]any{"default": math.NaN()}}}}

	_, err := pool.acquire(context.Background(), "parent", spec)
	if err == nil || !strings.Contains(err.Error(), "set initial tools") {
		t.Fatalf("acquire() error = %v, want set initial tools error", err)
	}
	if n := spawner.callCount(); n != 0 {
		t.Errorf("spawned %d CLI processes after failed initial tool sync, want 0", n)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("failed initial tool sync left %d entries in the temp dir, want 0", len(entries))
	}
}
