package builtin

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

func TestScratchpadToolReturnsAllFields(t *testing.T) {
	t.Parallel()
	def := NewScratchpadTool(Env{})
	result, err := def.Handler(context.Background(), map[string]any{
		"goal":      "fix auth bug",
		"plan":      "read auth.go, find issue, fix",
		"step":      "reading auth.go",
		"decisions": "chose context deadline",
		"files":     "auth.go (read)",
		"open":      "none",
		"next":      "check token validation",
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
	if m["decisions"] != "chose context deadline" {
		t.Fatalf("decisions = %q, want chose context deadline", m["decisions"])
	}
	if m["files"] != "auth.go (read)" {
		t.Fatalf("files = %q, want auth.go (read)", m["files"])
	}
}

func TestScratchpadToolRequiresGoal(t *testing.T) {
	t.Parallel()
	def := NewScratchpadTool(Env{})
	_, err := def.Handler(context.Background(), map[string]any{
		"goal":      "",
		"plan":      "some plan",
		"step":      "none",
		"decisions": "none",
		"files":     "none",
		"open":      "none",
		"next":      "none",
	})
	if err == nil {
		t.Fatal("expected error for missing goal")
	}
}

func TestScratchpadSchemaRequiresAllSevenFields(t *testing.T) {
	t.Parallel()
	schema := ScratchpadSchema()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required field missing or wrong type")
	}
	want := []string{"decisions", "files", "goal", "next", "open", "plan", "step"}
	got := make([]string, len(required))
	copy(got, required)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("required = %v, want %v", got, want)
	}
}

func TestScratchpadSchemaHasSevenProperties(t *testing.T) {
	t.Parallel()
	schema := ScratchpadSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties field missing or wrong type")
	}
	if got, want := len(props), 7; got != want {
		t.Fatalf("property count = %d, want %d", got, want)
	}
	for _, field := range []string{"goal", "plan", "step", "decisions", "files", "open", "next"} {
		if _, exists := props[field]; !exists {
			t.Errorf("missing property %q in schema", field)
		}
	}
}
