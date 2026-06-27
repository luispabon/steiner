package main

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// assertOrder checks that before appears before after in s.
func assertOrder(t *testing.T, s, before, after string) {
	t.Helper()
	i := strings.Index(s, before)
	j := strings.Index(s, after)
	if i == -1 {
		t.Errorf("output missing %q", before)
		return
	}
	if j == -1 {
		t.Errorf("output missing %q", after)
		return
	}
	if i >= j {
		t.Errorf("expected %q before %q in output:\n%s", before, after, s)
	}
}

func stripANSI(s string) string {
	return ansiEscapePattern.ReplaceAllString(s, "")
}

// setVersion is a helper that sets version and channel together and returns a
// cleanup function that restores both.
func setVersion(t *testing.T, v string) {
	t.Helper()
	oldVersion := version
	oldChannel := channel
	version = v
	if v == "dev" || strings.HasPrefix(v, "dev-") {
		channel = "dev"
	} else {
		channel = "stable"
	}
	t.Cleanup(func() {
		version = oldVersion
		channel = oldChannel
	})
}

func TestUpdateCommand_DevToStable(t *testing.T) {
	setVersion(t, "dev")

	oldCheck := checkFunc
	oldApply := applyFunc
	checkFunc = func(_ context.Context, _, _, _, _, channel, targetVersion string) (string, bool, error) {
		if channel != "stable" {
			t.Errorf("checkFunc channel = %q, want %q", channel, "stable")
		}
		if targetVersion != "" {
			t.Errorf("checkFunc targetVersion = %q, want empty", targetVersion)
		}
		return "v1.2.3", true, nil
	}
	applyFunc = func(_ context.Context, _, _, _, _, channel, _ string) (string, error) {
		if channel != "stable" {
			t.Errorf("applyFunc channel = %q, want %q", channel, "stable")
		}
		return "v1.2.3", nil
	}
	t.Cleanup(func() {
		checkFunc = oldCheck
		applyFunc = oldApply
	})

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stripANSI(stdout.String())
	if !strings.Contains(got, "checking version") {
		t.Errorf("stdout = %q, want substring %q", got, "checking version")
	}
	if !strings.Contains(got, "Current:") {
		t.Errorf("stdout = %q, want substring %q", got, "Current:")
	}
	if !strings.Contains(got, "Latest:") {
		t.Errorf("stdout = %q, want substring %q", got, "Latest:")
	}
	if !strings.Contains(got, "updating...") {
		t.Errorf("stdout = %q, want substring %q", got, "updating...")
	}
	if !strings.Contains(got, "\u2714") {
		t.Errorf("stdout = %q, want checkmark", got)
	}
	assertOrder(t, got, "\u26a0 notice", "checking version")
	assertOrder(t, got, "checking version", "Current:")
	assertOrder(t, got, "Current:", "Latest:")
	assertOrder(t, got, "Latest:", "updating...")
	assertOrder(t, got, "updating...", "\u2714")
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_DevBuildWithDevFlag(t *testing.T) {
	setVersion(t, "dev")

	oldCheck := checkFunc
	oldApply := applyFunc
	checkFunc = func(_ context.Context, _, _, _, _, channel, _ string) (string, bool, error) {
		if channel != "dev" {
			t.Errorf("checkFunc channel = %q, want %q", channel, "dev")
		}
		return "dev", true, nil
	}
	applyFunc = func(_ context.Context, _, _, _, _, channel, _ string) (string, error) {
		if channel != "dev" {
			t.Errorf("applyFunc channel = %q, want %q", channel, "dev")
		}
		return "dev", nil
	}
	t.Cleanup(func() {
		checkFunc = oldCheck
		applyFunc = oldApply
	})

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update", "--dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stripANSI(stdout.String())
	// No channel switch warning when already on dev channel.
	if strings.Contains(got, "\u26a0 notice") {
		t.Errorf("stdout = %q, should not contain channel switch warning", got)
	}
	if !strings.Contains(got, "Latest:") {
		t.Errorf("stdout = %q, want Latest label", got)
	}
	if !strings.Contains(got, "updating...") {
		t.Errorf("stdout = %q, want substring %q", got, "updating...")
	}
	if !strings.Contains(got, "\u2714") {
		t.Errorf("stdout = %q, want checkmark", got)
	}
	assertOrder(t, got, "checking version", "Current:")
	assertOrder(t, got, "updating...", "\u2714")
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_StableToDev(t *testing.T) {
	setVersion(t, "v1.2.3")

	oldCheck := checkFunc
	oldApply := applyFunc
	checkFunc = func(_ context.Context, _, _, _, _, channel, _ string) (string, bool, error) {
		if channel != "dev" {
			t.Errorf("checkFunc channel = %q, want %q", channel, "dev")
		}
		return "dev", true, nil
	}
	applyFunc = func(_ context.Context, _, _, _, _, channel, _ string) (string, error) {
		if channel != "dev" {
			t.Errorf("applyFunc channel = %q, want %q", channel, "dev")
		}
		return "dev", nil
	}
	t.Cleanup(func() {
		checkFunc = oldCheck
		applyFunc = oldApply
	})

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update", "--dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stripANSI(stdout.String())
	if !strings.Contains(got, "switching from stable to dev") {
		t.Errorf("stdout = %q, want channel switch warning", got)
	}
	assertOrder(t, got, "\u26a0 notice", "checking version")
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_StableUpToDate(t *testing.T) {
	setVersion(t, "v1.2.3")

	oldCheck := checkFunc
	oldApply := applyFunc
	calledApply := false
	checkFunc = func(_ context.Context, _, _, _, _, channel, _ string) (string, bool, error) {
		if channel != "stable" {
			t.Errorf("checkFunc channel = %q, want %q", channel, "stable")
		}
		return "v1.2.3", false, nil
	}
	applyFunc = func(_ context.Context, _, _, _, _, _, _ string) (string, error) {
		calledApply = true
		return "", nil
	}
	t.Cleanup(func() {
		checkFunc = oldCheck
		applyFunc = oldApply
	})

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stripANSI(stdout.String())
	if !strings.Contains(got, "Up to date") {
		t.Errorf("stdout = %q, want 'Up to date'", got)
	}
	if !strings.Contains(got, "\u2714 Up to date") {
		t.Errorf("stdout = %q, want checkmark with 'Up to date'", got)
	}
	if calledApply {
		t.Errorf("applyFunc should not have been called when up to date")
	}
	assertOrder(t, got, "Latest:", "Up to date")
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_StableUpdate(t *testing.T) {
	setVersion(t, "v1.2.0")

	oldCheck := checkFunc
	oldApply := applyFunc
	checkFunc = func(_ context.Context, _, _, _, _, channel, _ string) (string, bool, error) {
		if channel != "stable" {
			t.Errorf("checkFunc channel = %q, want %q", channel, "stable")
		}
		return "v1.2.3", true, nil
	}
	applyFunc = func(_ context.Context, _, _, _, _, channel, _ string) (string, error) {
		if channel != "stable" {
			t.Errorf("applyFunc channel = %q, want %q", channel, "stable")
		}
		return "v1.2.3", nil
	}
	t.Cleanup(func() {
		checkFunc = oldCheck
		applyFunc = oldApply
	})

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stripANSI(stdout.String())
	if !strings.Contains(got, "\u2714 updated") {
		t.Errorf("stdout = %q, want 'updated'", got)
	}
	if !strings.Contains(got, "updating...") {
		t.Errorf("stdout = %q, want %q", got, "updating...")
	}
	assertOrder(t, got, "Latest:", "updating...")
	assertOrder(t, got, "updating...", "\u2714 updated")
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_SpecificVersion(t *testing.T) {
	setVersion(t, "v1.2.3")

	oldCheck := checkFunc
	oldApply := applyFunc
	checkFunc = func(_ context.Context, _, _, _, _, channel, targetVersion string) (string, bool, error) {
		if channel != "stable" {
			t.Errorf("checkFunc channel = %q, want %q", channel, "stable")
		}
		if targetVersion != "v1.1.0" {
			t.Errorf("checkFunc targetVersion = %q, want %q", targetVersion, "v1.1.0")
		}
		return "v1.1.0", true, nil
	}
	applyFunc = func(_ context.Context, _, _, _, _, channel, targetVersion string) (string, error) {
		if channel != "stable" {
			t.Errorf("applyFunc channel = %q, want %q", channel, "stable")
		}
		if targetVersion != "v1.1.0" {
			t.Errorf("applyFunc targetVersion = %q, want %q", targetVersion, "v1.1.0")
		}
		return "v1.1.0", nil
	}
	t.Cleanup(func() {
		checkFunc = oldCheck
		applyFunc = oldApply
	})

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update", "v1.1.0"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stripANSI(stdout.String())
	if !strings.Contains(got, "v1.1.0") {
		t.Errorf("stdout = %q, want version v1.1.0", got)
	}
	if !strings.Contains(got, "\u2714 updated") {
		t.Errorf("stdout = %q, want 'updated'", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_SpecificVersionWithoutV(t *testing.T) {
	setVersion(t, "v1.2.3")

	oldCheck := checkFunc
	oldApply := applyFunc
	checkFunc = func(_ context.Context, _, _, _, _, _, targetVersion string) (string, bool, error) {
		if targetVersion != "1.1.0" {
			t.Errorf("checkFunc targetVersion = %q, want %q", targetVersion, "1.1.0")
		}
		return "v1.1.0", true, nil
	}
	applyFunc = func(_ context.Context, _, _, _, _, _, targetVersion string) (string, error) {
		if targetVersion != "1.1.0" {
			t.Errorf("applyFunc targetVersion = %q, want %q", targetVersion, "1.1.0")
		}
		return "v1.1.0", nil
	}
	t.Cleanup(func() {
		checkFunc = oldCheck
		applyFunc = oldApply
	})

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update", "1.1.0"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stripANSI(stdout.String())
	if !strings.Contains(got, "v1.1.0") {
		t.Errorf("stdout = %q, want version v1.1.0", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_DevWithVersionError(t *testing.T) {
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update", "--dev", "v1.2.3"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --dev with version, got nil")
	}
	if !strings.Contains(err.Error(), "--dev and a target version cannot be used together") {
		t.Errorf("error = %v, want version/dev conflict error", err)
	}
}

func TestUpdateCommand_RootDevFlag(t *testing.T) {
	setVersion(t, "v1.0.0")

	oldCheck := checkFunc
	oldApply := applyFunc
	checkFunc = func(_ context.Context, _, _, _, _, channel, _ string) (string, bool, error) {
		if channel != "dev" {
			t.Errorf("checkFunc channel = %q, want %q", channel, "dev")
		}
		return "dev", true, nil
	}
	applyFunc = func(_ context.Context, _, _, _, _, channel, _ string) (string, error) {
		if channel != "dev" {
			t.Errorf("applyFunc channel = %q, want %q", channel, "dev")
		}
		return "dev", nil
	}
	t.Cleanup(func() {
		checkFunc = oldCheck
		applyFunc = oldApply
	})

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--dev", "update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stripANSI(stdout.String())
	if !strings.Contains(got, "switching from stable to dev") {
		t.Errorf("stdout = %q, want channel switch warning", got)
	}
	if !strings.Contains(got, "updating...") {
		t.Errorf("stdout = %q, want %q", got, "updating...")
	}
	if !strings.Contains(got, "\u2714") {
		t.Errorf("stdout = %q, want checkmark", got)
	}
	assertOrder(t, got, "Latest:", "updating...")
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

	want := "Update steiner to the latest release, or to a specific version"
	if !strings.Contains(stripANSI(stdout.String()), want) {
		t.Errorf("stdout = %q, want substring %q", stripANSI(stdout.String()), want)
	}
}

func TestUpdateCommand_UpgradeAlias(t *testing.T) {
	setVersion(t, "v1.0.0")

	oldCheck := checkFunc
	oldApply := applyFunc
	checkFunc = func(_ context.Context, _, _, _, _, channel, _ string) (string, bool, error) {
		if channel != "stable" {
			t.Errorf("checkFunc channel = %q, want %q", channel, "stable")
		}
		return "v1.2.0", true, nil
	}
	applyFunc = func(_ context.Context, _, _, _, _, _, _ string) (string, error) {
		return "v1.2.0", nil
	}
	t.Cleanup(func() {
		checkFunc = oldCheck
		applyFunc = oldApply
	})

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"upgrade"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stripANSI(stdout.String())
	if !strings.Contains(got, "checking version") {
		t.Errorf("stdout = %q, want substring %q", got, "checking version")
	}
	if !strings.Contains(got, "updating...") {
		t.Errorf("stdout = %q, want substring %q", got, "updating...")
	}
	if !strings.Contains(got, "\u2714 updated") {
		t.Errorf("stdout = %q, want 'updated'", got)
	}
	if !strings.Contains(got, "v1.2.0") {
		t.Errorf("stdout = %q, want version v1.2.0", got)
	}
	assertOrder(t, got, "checking version", "Current:")
	assertOrder(t, got, "Current:", "Latest:")
	assertOrder(t, got, "Latest:", "updating...")
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_NoTTY(t *testing.T) {
	setVersion(t, "v1.0.0")

	oldCheck := checkFunc
	oldApply := applyFunc
	checkFunc = func(_ context.Context, _, _, _, _, _, _ string) (string, bool, error) {
		return "v1.2.0", true, nil
	}
	applyFunc = func(_ context.Context, _, _, _, _, _, _ string) (string, error) {
		return "v1.2.0", nil
	}
	t.Cleanup(func() {
		checkFunc = oldCheck
		applyFunc = oldApply
	})

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stripANSI(stdout.String())
	// Non-TTY (bytes.Buffer): no \r, no braille glyphs.
	if strings.Contains(got, "\r") {
		t.Errorf("non-TTY output should not contain \\r: %q", got)
	}
	if strings.Contains(got, "\u28be") {
		t.Errorf("non-TTY output should not contain braille: %q", got)
	}
	if !strings.Contains(got, "\u2714") {
		t.Errorf("stdout = %q, want checkmark", got)
	}
	if !strings.Contains(got, "v1.2.0") {
		t.Errorf("stdout = %q, want version v1.2.0", got)
	}
	assertOrder(t, got, "checking version", "Current:")
	assertOrder(t, got, "Current:", "Latest:")
	assertOrder(t, got, "Latest:", "updating...")
	assertOrder(t, got, "updating...", "\u2714")
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUpdateCommand_CheckError(t *testing.T) {
	setVersion(t, "v1.0.0")

	oldCheck := checkFunc
	oldApply := applyFunc
	calledApply := false
	checkFunc = func(_ context.Context, _, _, _, _, _, _ string) (string, bool, error) {
		return "", false, fmt.Errorf("network error")
	}
	applyFunc = func(_ context.Context, _, _, _, _, _, _ string) (string, error) {
		calledApply = true
		return "", nil
	}
	t.Cleanup(func() {
		checkFunc = oldCheck
		applyFunc = oldApply
	})

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(stderr.String(), "\u2717") {
		t.Errorf("stderr = %q, want crossMark", stderr.String())
	}
	if !strings.Contains(stderr.String(), "network error") {
		t.Errorf("stderr = %q, want error message", stderr.String())
	}
	if calledApply {
		t.Errorf("applyFunc should not have been called after check error")
	}
}

func TestUpdateCommand_ApplyError(t *testing.T) {
	setVersion(t, "v1.0.0")

	oldCheck := checkFunc
	oldApply := applyFunc
	checkFunc = func(_ context.Context, _, _, _, _, _, _ string) (string, bool, error) {
		return "v1.2.0", true, nil
	}
	applyFunc = func(_ context.Context, _, _, _, _, _, _ string) (string, error) {
		return "", fmt.Errorf("download failed")
	}
	t.Cleanup(func() {
		checkFunc = oldCheck
		applyFunc = oldApply
	})

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"update"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(stderr.String(), "download failed") {
		t.Errorf("stderr = %q, want error message", stderr.String())
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

func TestDisplayVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"dev", "dev"},
		{"dev-abc1234", "dev-abc1234"},
		{"v1.2.3", "v1.2.3"},
		{"1.2.3", "v1.2.3"},
		{"", "v"},
	}
	for _, tt := range tests {
		got := displayVersion(tt.input)
		if got != tt.want {
			t.Errorf("displayVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
