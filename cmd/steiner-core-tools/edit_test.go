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

func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunEdit_MissingPath(t *testing.T) {
	_, err := runEdit(context.Background(), []byte(`{"old":"foo","new":"bar"}`))
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

func TestRunEdit_MissingOld(t *testing.T) {
	_, err := runEdit(context.Background(), []byte(`{"path":"test.txt"}`))
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
	if envelopeErr.Message != "old is required" {
		t.Errorf("Message = %q, want %q", envelopeErr.Message, "old is required")
	}
}

func TestRunEdit_FileNotFound(t *testing.T) {
	chdirTemp(t)

	_, err := runEdit(context.Background(), []byte(`{"path":"nonexistent.txt","old":"foo","new":"bar"}`))
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	var envelopeErr *steinertool.JSONEnvelopeError
	if !errors.As(err, &envelopeErr) {
		t.Fatalf("err type = %T, want *steinertool.JSONEnvelopeError", err)
	}
	if envelopeErr.Kind != "edit_error" {
		t.Errorf("Kind = %q, want %q", envelopeErr.Kind, "edit_error")
	}
}

func TestRunEdit_OldNotFound(t *testing.T) {
	chdirTemp(t)
	if err := os.WriteFile("test.txt", []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runEdit(context.Background(), []byte(`{"path":"test.txt","old":"goodbye","new":"friend"}`))
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	var envelopeErr *steinertool.JSONEnvelopeError
	if !errors.As(err, &envelopeErr) {
		t.Fatalf("err type = %T, want *steinertool.JSONEnvelopeError", err)
	}
	if envelopeErr.Kind != "edit_error" {
		t.Errorf("Kind = %q, want %q", envelopeErr.Kind, "edit_error")
	}
	details, ok := envelopeErr.Details.(map[string]any)
	if !ok {
		t.Fatalf("Details type = %T, want map[string]any", envelopeErr.Details)
	}
	if g, w := details["occurrences"], 0; g != w {
		t.Errorf("occurrences = %v, want %v", g, w)
	}
}

func TestRunEdit_MultipleOccurrences(t *testing.T) {
	chdirTemp(t)
	if err := os.WriteFile("test.txt", []byte("foo foo bar"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runEdit(context.Background(), []byte(`{"path":"test.txt","old":"foo","new":"baz"}`))
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	var envelopeErr *steinertool.JSONEnvelopeError
	if !errors.As(err, &envelopeErr) {
		t.Fatalf("err type = %T, want *steinertool.JSONEnvelopeError", err)
	}
	if envelopeErr.Kind != "edit_error" {
		t.Errorf("Kind = %q, want %q", envelopeErr.Kind, "edit_error")
	}
	details, ok := envelopeErr.Details.(map[string]any)
	if !ok {
		t.Fatalf("Details type = %T, want map[string]any", envelopeErr.Details)
	}
	occ, ok := details["occurrences"].(int)
	if !ok {
		t.Fatalf("occurrences type = %T, want int", details["occurrences"])
	}
	if occ <= 1 {
		t.Errorf("occurrences = %d, want > 1", occ)
	}
}

func TestRunEdit_Success(t *testing.T) {
	dir := chdirTemp(t)
	if err := os.WriteFile("test.txt", []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := runEdit(context.Background(), []byte(`{"path":"test.txt","old":"hello","new":"goodbye"}`))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	wantPath := filepath.Join(dir, "test.txt")
	if g, w := m["path"], wantPath; g != w {
		t.Errorf("path = %v, want %v", g, w)
	}
	if g, w := m["replacements"], 1; g != w {
		t.Errorf("replacements = %v, want %v", g, w)
	}

	contents, err := os.ReadFile("test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if g, w := string(contents), "goodbye world"; g != w {
		t.Errorf("file contents = %q, want %q", g, w)
	}
}

func TestResolveEditablePath_RejectsParentDir(t *testing.T) {
	_, err := resolveEditablePath("../etc/passwd")
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Errorf("err = %q, want to contain 'outside'", err.Error())
	}
}

func TestResolveEditablePath_RejectsDotDot(t *testing.T) {
	_, err := resolveEditablePath("..")
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Errorf("err = %q, want to contain 'outside'", err.Error())
	}
}

func TestResolveEditablePath_ResolvesRelative(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveEditablePath("subdir/file.txt")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	want := filepath.Clean(filepath.Join(wd, "subdir/file.txt"))
	if got != want {
		t.Errorf("resolveEditablePath(%q) = %q, want %q", "subdir/file.txt", got, want)
	}
}

func TestResolveEditablePath_AllowsValidAbsolute(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	validPath := filepath.Join(wd, "somefile")
	got, err := resolveEditablePath(validPath)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != filepath.Clean(validPath) {
		t.Errorf("resolveEditablePath(%q) = %q, want %q", validPath, got, filepath.Clean(validPath))
	}
}

func TestResolveEditablePath_AllowsCurrentDir(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveEditablePath(".")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != wd {
		t.Errorf("resolveEditablePath(\".\") = %q, want %q", got, wd)
	}
}

func TestResolveEditablePath_Empty(t *testing.T) {
	_, err := resolveEditablePath("")
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if !errors.Is(err, os.ErrInvalid) {
		t.Errorf("err = %v, want os.ErrInvalid", err)
	}
}
