package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/update"
)

func TestUpdateCommand_DevBuild(t *testing.T) {
	oldVersion := version
	version = "dev"
	t.Cleanup(func() { version = oldVersion })

	// No updateFunc mock needed; it should not be called.

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !strings.Contains(stderr.String(), "Warning: dev builds cannot check for stable updates") {
		t.Errorf("stderr = %q, want warning about dev builds", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestUpdateCommand_DevBuildWithDash(t *testing.T) {
	oldVersion := version
	version = "dev-abc1234"
	t.Cleanup(func() { version = oldVersion })

	// No updateFunc mock needed; it should not be called.

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !strings.Contains(stderr.String(), "Warning: dev builds cannot check for stable updates") {
		t.Errorf("stderr = %q, want warning about dev builds", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestUpdateCommand_DevBuildWithDevFlag(t *testing.T) {
	oldVersion := version
	version = "dev"
	t.Cleanup(func() { version = oldVersion })

	oldUpdateFunc := updateFunc
	updateFunc = func(_ context.Context, _, _, _, _, channel string) (string, error) {
		if channel != "dev" {
			t.Errorf("updateFunc channel = %q, want %q", channel, "dev")
		}
		return "dev", nil
	}
	t.Cleanup(func() { updateFunc = oldUpdateFunc })

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update", "--dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "Downloading…") {
		t.Errorf("stdout = %q, want substring %q", got, "Downloading…")
	}
	if !strings.Contains(got, "updated to dev") {
		t.Errorf("stdout = %q, want substring %q", got, "updated to dev")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_RootDevFlag(t *testing.T) {
	oldVersion := version
	version = "v0.1.0"
	t.Cleanup(func() { version = oldVersion })

	oldUpdateFunc := updateFunc
	updateFunc = func(_ context.Context, _, _, _, _, channel string) (string, error) {
		if channel != "dev" {
			t.Errorf("updateFunc channel = %q, want %q", channel, "dev")
		}
		return "dev", nil
	}
	t.Cleanup(func() { updateFunc = oldUpdateFunc })

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--dev", "update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "Downloading…") {
		t.Errorf("stdout = %q, want substring %q", got, "Downloading…")
	}
	if !strings.Contains(got, "updated to dev") {
		t.Errorf("stdout = %q, want substring %q", got, "updated to dev")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_Help(t *testing.T) {
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := "Update steiner to the latest release"
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout = %q, want substring %q", stdout.String(), want)
	}
}

func TestUpdateCommand_UpgradeAlias(t *testing.T) {
	oldVersion := version
	version = "v0.1.0"
	t.Cleanup(func() { version = oldVersion })

	oldUpdateFunc := updateFunc
	updateFunc = func(_ context.Context, _, _, _, _, _ string) (string, error) {
		return "v0.2.0", nil
	}
	t.Cleanup(func() { updateFunc = oldUpdateFunc })

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"upgrade"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "Downloading…") {
		t.Errorf("stdout = %q, want substring %q", got, "Downloading…")
	}
	if !strings.Contains(got, "updated to v0.2.0") {
		t.Errorf("stdout = %q, want substring %q", got, "updated to v0.2.0")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_UpToDate(t *testing.T) {
	oldVersion := version
	version = "v0.1.0"
	t.Cleanup(func() { version = oldVersion })

	oldUpdateFunc := updateFunc
	updateFunc = func(_ context.Context, _, _, _, _, _ string) (string, error) {
		return "v0.1.0", update.ErrUpToDate
	}
	t.Cleanup(func() { updateFunc = oldUpdateFunc })

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "already up to date") {
		t.Errorf("stdout = %q, want up-to-date message", got)
	}
	if !strings.Contains(got, "current") {
		t.Errorf("stdout = %q, want version block label 'current'", got)
	}
	if !strings.Contains(got, "latest") {
		t.Errorf("stdout = %q, want version block label 'latest'", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_Success(t *testing.T) {
	oldVersion := version
	version = "v0.1.0"
	t.Cleanup(func() { version = oldVersion })

	oldUpdateFunc := updateFunc
	updateFunc = func(_ context.Context, _, _, _, _, _ string) (string, error) {
		return "v0.2.0", nil
	}
	t.Cleanup(func() { updateFunc = oldUpdateFunc })

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "Downloading…") {
		t.Errorf("stdout = %q, want substring %q", got, "Downloading…")
	}
	if !strings.Contains(got, "updated to v0.2.0") {
		t.Errorf("stdout = %q, want substring %q", got, "updated to v0.2.0")
	}
	if !strings.Contains(got, "current") {
		t.Errorf("stdout = %q, want version block label 'current'", got)
	}
	if !strings.Contains(got, "latest") {
		t.Errorf("stdout = %q, want version block label 'latest'", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_NoTTY(t *testing.T) {
	oldVersion := version
	version = "v0.1.0"
	t.Cleanup(func() { version = oldVersion })

	oldUpdateFunc := updateFunc
	updateFunc = func(_ context.Context, _, _, _, _, _ string) (string, error) {
		return "v0.2.0", nil
	}
	t.Cleanup(func() { updateFunc = oldUpdateFunc })

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	// Non-TTY (bytes.Buffer): spinner writes static "Downloading…" line,
	// followed by "✔ updated to v0.2.0" line. No \r, no braille glyphs.
	if !strings.Contains(got, "Downloading…") {
		t.Errorf("stdout = %q, want substring %q", got, "Downloading…")
	}
	if strings.Contains(got, "\r") {
		t.Errorf("non-TTY output should not contain \\r: %q", got)
	}
	if strings.Contains(got, "⣾") {
		t.Errorf("non-TTY output should not contain braille: %q", got)
	}
	if !strings.Contains(got, "✔") {
		t.Errorf("stdout = %q, want checkmark", got)
	}
	if !strings.Contains(got, "v0.2.0") {
		t.Errorf("stdout = %q, want version v0.2.0", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestIsDevBuild(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"dev", true},
		{"dev-abc1234", true},
		{"dev-", true},
		{"v1.0.0", false},
		{"1.0.0", false},
		{"", false},
		{"development", false},
	}
	for _, tt := range tests {
		got := isDevBuild(tt.input)
		if got != tt.want {
			t.Errorf("isDevBuild(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
