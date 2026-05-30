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

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	want := "Warning: dev build cannot self-update"
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want substring %q", stderr.String(), want)
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
	updateFunc = func(_ context.Context, _, _, _, _ string) (string, error) {
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

	if !strings.Contains(stdout.String(), "steiner updated successfully") {
		t.Errorf("stdout = %q, want success message", stdout.String())
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
	updateFunc = func(_ context.Context, _, _, _, _ string) (string, error) {
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

	if !strings.Contains(stdout.String(), "steiner is already up to date") {
		t.Errorf("stdout = %q, want up-to-date message", stdout.String())
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
	updateFunc = func(_ context.Context, _, _, _, _ string) (string, error) {
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

	if !strings.Contains(stdout.String(), "steiner updated successfully") {
		t.Errorf("stdout = %q, want success message", stdout.String())
	}
	if !strings.Contains(stdout.String(), "v0.2.0") {
		t.Errorf("stdout = %q, want version v0.2.0", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}
