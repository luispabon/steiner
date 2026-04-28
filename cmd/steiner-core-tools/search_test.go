package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	steinertool "github.com/luispabon/steiner/internal/tool"
)

func TestRunSearch_MissingQuery(t *testing.T) {
	_, err := runSearch(context.Background(), []byte(`{}`))
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
	if envelopeErr.Message != "query is required" {
		t.Errorf("Message = %q, want %q", envelopeErr.Message, "query is required")
	}
}

func TestRunSearch_Success(t *testing.T) {
	dir := chdirTemp(t)
	files := map[string]string{
		"file1.txt": "hello world\nfoobar\n",
		"file2.txt": "goodbye world\nhello again\n",
		"file3.txt": "nothing here\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := runSearch(context.Background(), []byte(`{"query":"hello"}`))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if g, w := m["query"], "hello"; g != w {
		t.Errorf("query = %v, want %v", g, w)
	}
	matches, ok := m["matches"].([]searchMatch)
	if !ok {
		t.Fatalf("matches type = %T, want []searchMatch", m["matches"])
	}
	if len(matches) != 2 {
		t.Fatalf("len(matches) = %d, want 2", len(matches))
	}
	for _, mm := range matches {
		if mm.Path == "" {
			t.Error("match missing path")
		}
		if mm.Line < 1 {
			t.Error("match missing or invalid line")
		}
		if mm.Text == "" {
			t.Error("match missing text")
		}
	}
}

func TestRunSearch_SkipsBinaryFiles(t *testing.T) {
	dir := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(dir, "text.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "binary.bin"), []byte("hel\x00lo world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := runSearch(context.Background(), []byte(`{"query":"hel"}`))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	matches, ok := m["matches"].([]searchMatch)
	if !ok {
		t.Fatalf("matches type = %T, want []searchMatch", m["matches"])
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1 (binary files should be skipped)", len(matches))
	}
	mm := matches[0]
	if g := mm.Path; g != "text.txt" {
		t.Errorf("match path = %q, want %q", g, "text.txt")
	}
}

func TestRunSearch_RespectsMaxResults(t *testing.T) {
	dir := chdirTemp(t)
	var lines []string
	for i := 0; i < maxSearchResults+5; i++ {
		lines = append(lines, "matchme")
	}
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := runSearch(context.Background(), []byte(`{"query":"matchme"}`))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	matches, ok := m["matches"].([]searchMatch)
	if !ok {
		t.Fatalf("matches type = %T, want []searchMatch", m["matches"])
	}
	if len(matches) > maxSearchResults {
		t.Errorf("len(matches) = %d, want <= %d", len(matches), maxSearchResults)
	}
}
