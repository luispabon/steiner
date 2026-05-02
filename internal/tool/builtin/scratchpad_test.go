package builtin

import (
	"context"
	"testing"
)

func TestScratchpadToolReturnsFields(t *testing.T) {
	t.Parallel()
	def := NewScratchpadTool(Env{})
	result, err := def.Handler(context.Background(), map[string]any{
		"goal": "fix auth bug",
		"plan": "read auth.go, find issue, fix",
		"step": "reading auth.go",
		"next": "check token validation",
		"open": "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("result type = %T, want map[string]string", result)
	}
	if m["status"] != "ok" {
		t.Fatalf("status = %q, want ok", m["status"])
	}
	if m["goal"] != "fix auth bug" {
		t.Fatalf("goal = %q, want fix auth bug", m["goal"])
	}
}

func TestScratchpadToolRequiresGoal(t *testing.T) {
	t.Parallel()
	def := NewScratchpadTool(Env{})
	_, err := def.Handler(context.Background(), map[string]any{
		"plan": "some plan",
	})
	if err == nil {
		t.Fatal("expected error for missing goal")
	}
}

func TestScratchpadToolAcceptsGoalOnly(t *testing.T) {
	t.Parallel()
	def := NewScratchpadTool(Env{})
	result, err := def.Handler(context.Background(), map[string]any{
		"goal": "investigate crash",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]string)
	if m["goal"] != "investigate crash" {
		t.Fatalf("goal = %q, want investigate crash", m["goal"])
	}
}

func TestScratchpadSchemaRequiresGoal(t *testing.T) {
	t.Parallel()
	schema := ScratchpadSchema()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required field missing or wrong type")
	}
	if len(required) != 1 || required[0] != "goal" {
		t.Fatalf("required = %v, want [goal]", required)
	}
}
