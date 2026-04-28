package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	steinertool "github.com/luispabon/steiner/internal/tool"
)

func TestRunGlob_MissingPattern(t *testing.T) {
	_, err := runGlob(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	var envelopeErr *steinertool.JSONEnvelopeError
	if !errors.As(err, &envelopeErr) {
		t.Fatalf("err type = %T, want *steinertool.JSONEnvelopeError", err)
	}
	if envelopeErr.Kind != "invalid_input" {
		t.Errorf("Kind = %q, want %q", envelopeErr.Kind, "invalid_input")
	}
	if envelopeErr.Message != "pattern is required" {
		t.Errorf("Message = %q, want %q", envelopeErr.Message, "pattern is required")
	}
}

func TestRunGlob_Success(t *testing.T) {
	_ = chdirTemp(t)
	files := []string{"a.txt", "b.txt", "c.go", "d.go"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(".", f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := runGlob(context.Background(), []byte(`{"pattern":"*.txt"}`))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if g, w := m["pattern"], "*.txt"; g != w {
		t.Errorf("pattern = %v, want %v", g, w)
	}
	matches, ok := m["matches"].([]string)
	if !ok {
		t.Fatalf("matches type = %T, want []any", m["matches"])
	}
	if len(matches) != 2 {
		t.Fatalf("len(matches) = %d, want 2", len(matches))
	}
	sort.Strings(matches)
	if g, w := matches[0], "a.txt"; g != w {
		t.Errorf("matches[0] = %v, want %v", g, w)
	}
	if g, w := matches[1], "b.txt"; g != w {
		t.Errorf("matches[1] = %v, want %v", g, w)
	}
}
