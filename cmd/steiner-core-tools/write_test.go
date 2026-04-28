package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	steinertool "github.com/luispabon/steiner/internal/tool"
)

func TestRunWrite_MissingPath(t *testing.T) {
	_, err := runWrite(context.Background(), []byte(`{"contents":"hello"}`))
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
	if envelopeErr.Message != "path is required" {
		t.Errorf("Message = %q, want %q", envelopeErr.Message, "path is required")
	}
}

func TestRunWrite_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "test.txt")
	content := "hello world"

	result, err := runWrite(context.Background(), []byte(`{"path":"`+path+`","contents":"`+content+`"}`))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if g, w := m["path"], path; g != w {
		t.Errorf("path = %v, want %v", g, w)
	}
	if g, w := m["bytes_written"], len(content); g != w {
		t.Errorf("bytes_written = %v, want %v", g, w)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if g, w := string(data), content; g != w {
		t.Errorf("file contents = %q, want %q", g, w)
	}
}
