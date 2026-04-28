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

func TestRunBash_EmptyCommand(t *testing.T) {
	_, err := runBash(context.Background(), []byte(`{"command":""}`))
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
	if envelopeErr.Message != "command is required" {
		t.Errorf("Message = %q, want %q", envelopeErr.Message, "command is required")
	}
}

func TestRunBash_CommandSuccess(t *testing.T) {
	result, err := runBash(context.Background(), []byte(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if g, w := m["command"], "echo hello"; g != w {
		t.Errorf("command = %v, want %v", g, w)
	}
	if g, w := m["stdout"], "hello\n"; g != w {
		t.Errorf("stdout = %q, want %q", g, w)
	}
	if g, w := m["stderr"], ""; g != w {
		t.Errorf("stderr = %q, want %q", g, w)
	}
	if g, w := m["exit_code"], 0; g != w {
		t.Errorf("exit_code = %v, want %v", g, w)
	}
}

func TestRunBash_CommandFailure(t *testing.T) {
	_, err := runBash(context.Background(), []byte(`{"command":"false"}`))
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	var envelopeErr *steinertool.JSONEnvelopeError
	if !errors.As(err, &envelopeErr) {
		t.Fatalf("err type = %T, want *steinertool.JSONEnvelopeError", err)
	}
	if envelopeErr.Kind != "command_failed" {
		t.Errorf("Kind = %q, want %q", envelopeErr.Kind, "command_failed")
	}
	if envelopeErr.Details == nil {
		t.Fatal("Details = nil, want non-nil")
	}
	details, ok := envelopeErr.Details.(map[string]any)
	if !ok {
		t.Fatalf("Details type = %T, want map[string]any", envelopeErr.Details)
	}
	if g, w := details["command"], "false"; g != w {
		t.Errorf("Details[command] = %v, want %v", g, w)
	}
	exitCode, ok := details["exit_code"].(int)
	if !ok {
		t.Fatalf("Details[exit_code] type = %T, want int", details["exit_code"])
	}
	if exitCode != 1 {
		t.Errorf("Details[exit_code] = %v, want 1", exitCode)
	}
}

func TestResolveWorkingDir_Empty(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveWorkingDir("")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != wd {
		t.Errorf("resolveWorkingDir(\"\") = %q, want %q", got, wd)
	}
}

func TestResolveWorkingDir_Relative(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveWorkingDir("subdir")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	want := filepath.Clean(filepath.Join(wd, "subdir"))
	if got != want {
		t.Errorf("resolveWorkingDir(\"subdir\") = %q, want %q", got, want)
	}
}

func TestResolveWorkingDir_Absolute(t *testing.T) {
	got, err := resolveWorkingDir("/tmp")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != "/tmp" {
		t.Errorf("resolveWorkingDir(\"/tmp\") = %q, want %q", got, "/tmp")
	}
}

func TestResolveWorkingDir_AbsoluteCleaned(t *testing.T) {
	got, err := resolveWorkingDir("/tmp/../tmp")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != "/tmp" {
		t.Errorf("resolveWorkingDir(\"/tmp/../tmp\") = %q, want %q", got, "/tmp")
	}
}

func TestRunBash_EmptyPayload(t *testing.T) {
	_, err := runBash(context.Background(), []byte{})
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
}

func TestRunBash_CommandSuccessWithStderr(t *testing.T) {
	result, err := runBash(context.Background(), []byte(`{"command":"echo out && echo err >&2"}`))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if g := m["stdout"].(string); !strings.Contains(g, "out") {
		t.Errorf("stdout = %q, want to contain %q", g, "out")
	}
	if g := m["stderr"].(string); !strings.Contains(g, "err") {
		t.Errorf("stderr = %q, want to contain %q", g, "err")
	}
	if g, w := m["exit_code"], 0; g != w {
		t.Errorf("exit_code = %v, want %v", g, w)
	}
}
