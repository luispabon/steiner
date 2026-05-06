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
		"intent":    "fix auth bug",
		"decisions": "chose context deadline",
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
	if m["intent"] != "fix auth bug" {
		t.Fatalf("intent = %q, want fix auth bug", m["intent"])
	}
	if m["decisions"] != "chose context deadline" {
		t.Fatalf("decisions = %q, want chose context deadline", m["decisions"])
	}
	if m["open"] != "none" {
		t.Fatalf("open = %q, want none", m["open"])
	}
}

func TestScratchpadToolRejectsLegacyOnlyPayloads(t *testing.T) {
	t.Parallel()
	def := NewScratchpadTool(Env{})
	_, err := def.Handler(context.Background(), map[string]any{
		"goal":  "fix auth bug",
		"plan":  "read auth.go, find issue, fix",
		"step":  "reading auth.go",
		"files": "auth.go (read)",
	})
	if err == nil {
		t.Fatal("expected error for legacy-only payload")
	}
}

func TestScratchpadToolRequiresIntent(t *testing.T) {
	t.Parallel()
	def := NewScratchpadTool(Env{})
	_, err := def.Handler(context.Background(), map[string]any{
		"intent":    "",
		"decisions": "none",
		"open":      "none",
		"next":      "none",
	})
	if err == nil {
		t.Fatal("expected error for missing intent")
	}
}

func TestScratchpadSchemaRequiresAllFourFields(t *testing.T) {
	t.Parallel()
	schema := ScratchpadSchema()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required field missing or wrong type")
	}
	want := []string{"decisions", "intent", "next", "open"}
	got := make([]string, len(required))
	copy(got, required)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("required = %v, want %v", got, want)
	}
}

func TestScratchpadSchemaHasFourProperties(t *testing.T) {
	t.Parallel()
	schema := ScratchpadSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties field missing or wrong type")
	}
	if got, want := len(props), 4; got != want {
		t.Fatalf("property count = %d, want %d", got, want)
	}
	for _, field := range []string{"intent", "decisions", "open", "next"} {
		if _, exists := props[field]; !exists {
			t.Errorf("missing property %q in schema", field)
		}
	}
}
