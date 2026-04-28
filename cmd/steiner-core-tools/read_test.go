package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	steinertool "github.com/luispabon/steiner/internal/tool"
)

func TestRunRead_MissingPath(t *testing.T) {
	_, err := runRead(context.Background(), []byte(`{}`))
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

func TestRunRead_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.txt")
	payload := []byte(`{"path":"` + path + `"}`)

	_, err := runRead(context.Background(), payload)
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	var envelopeErr *steinertool.JSONEnvelopeError
	if !errors.As(err, &envelopeErr) {
		t.Fatalf("err type = %T, want *steinertool.JSONEnvelopeError", err)
	}
	if envelopeErr.Kind != "read_error" {
		t.Errorf("Kind = %q, want %q", envelopeErr.Kind, "read_error")
	}
	if envelopeErr.Details == nil {
		t.Fatal("Details = nil, want non-nil")
	}
	details, ok := envelopeErr.Details.(map[string]any)
	if !ok {
		t.Fatalf("Details type = %T, want map[string]any", envelopeErr.Details)
	}
	if g, w := details["path"], path; g != w {
		t.Errorf("Details[path] = %v, want %v", g, w)
	}
}

func TestRunRead_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "hello world"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := []byte(`{"path":"` + path + `"}`)
	result, err := runRead(context.Background(), payload)
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
	if g, w := m["contents"], content; g != w {
		t.Errorf("contents = %v, want %v", g, w)
	}
}
