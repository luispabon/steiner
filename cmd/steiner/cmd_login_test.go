package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewLoginCommand(t *testing.T) {
	cmd := newLoginCommand()
	if cmd.Use != "login" {
		t.Errorf("Use = %q, want %q", cmd.Use, "login")
	}
	if cmd.Short != "Authenticate with supported providers" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Authenticate with supported providers")
	}
}

func TestNewLoginCodexCommand(t *testing.T) {
	cmd := newLoginCodexCommand()
	if cmd.Use != "codex" {
		t.Errorf("Use = %q, want %q", cmd.Use, "codex")
	}
	if cmd.Short != "Authenticate with OpenAI Codex via OAuth" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Authenticate with OpenAI Codex via OAuth")
	}

	flag := cmd.Flags().Lookup("timeout")
	if flag == nil {
		t.Fatal("--timeout flag not found")
	}
	if flag.DefValue != "2m0s" {
		t.Errorf("--timeout default = %q, want %q", flag.DefValue, "2m0s")
	}

	var found bool
	for _, sub := range cmd.Commands() {
		if sub.Use == "status" {
			found = true
			break
		}
	}
	if !found {
		t.Error("status subcommand not found on codex command")
	}
}

func TestLoginCodexStatusNoToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cmd := newLoginCodexStatusCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Not authenticated") {
		t.Errorf("output %q does not contain 'Not authenticated'", out)
	}
}

func TestLoginCodexStatusWithToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	tokenDir := filepath.Join(dir, "steiner")
	if err := os.MkdirAll(tokenDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	expiry := time.Now().Add(10 * time.Minute)
	tokenData := map[string]any{
		"access_token": "tok_abc",
		"token_type":   "Bearer",
		"expiry":       expiry.Format(time.RFC3339),
	}
	data, err := json.Marshal(tokenData)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	tokenPath := filepath.Join(tokenDir, "codex_auth.json")
	if err := os.WriteFile(tokenPath, data, 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	cmd := newLoginCodexStatusCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Token expires") {
		t.Errorf("output %q does not contain 'Token expires'", out)
	}
	if !strings.Contains(out, "Status: valid") {
		t.Errorf("output %q does not contain 'Status: valid'", out)
	}
}
