package mcp

import (
	"context"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBuildCommand_Basic(t *testing.T) {
	spec := ServerSpec{
		Name:    "fixture",
		Command: "/usr/bin/env",
		Args:    []string{"-i"},
		Env:     map[string]string{"FOO": "bar", "BAZ": "qux"},
	}
	stderr := io.Discard

	cmd := buildCommand(context.Background(), spec, nil, stderr)

	if cmd.Path != spec.Command {
		t.Errorf("Path = %q, want %q", cmd.Path, spec.Command)
	}
	wantArgs := append([]string{spec.Command}, spec.Args...)
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Errorf("Args = %v, want %v", cmd.Args, wantArgs)
	}
	if cmd.Stdin != nil {
		t.Error("Stdin must stay nil for the SDK's StdinPipe")
	}
	if cmd.Stdout != nil {
		t.Error("Stdout must stay nil for the SDK's StdoutPipe")
	}
	if cmd.Stderr != stderr {
		t.Error("Stderr not set to the provided writer")
	}
	for _, kv := range os.Environ() {
		if !slices.Contains(cmd.Env, kv) {
			t.Errorf("Env missing os.Environ() entry %q", kv)
		}
	}
	for k, v := range spec.Env {
		if !slices.Contains(cmd.Env, k+"="+v) {
			t.Errorf("Env missing spec entry %q", k+"="+v)
		}
	}
	if len(cmd.Env) != len(os.Environ())+len(spec.Env) {
		t.Errorf("Env length = %d, want %d", len(cmd.Env), len(os.Environ())+len(spec.Env))
	}
}

func TestBuildCommand_AppliesProcessGroup(t *testing.T) {
	cmd := buildCommand(context.Background(), ServerSpec{Command: "/bin/true"}, nil, io.Discard)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Error("SysProcAttr.Setpgid not set on the unwrapped command")
	}
	if cmd.Cancel == nil {
		t.Error("Cancel not set")
	}
	if cmd.WaitDelay != 5*time.Second {
		t.Errorf("WaitDelay = %v, want 5s", cmd.WaitDelay)
	}
	if cmd.Stdin != nil || cmd.Stdout != nil {
		t.Error("Stdin/Stdout must stay nil")
	}
}

func TestBuildCommand_SpecEnvSurvivesFilteringWrap(t *testing.T) {
	allowed := map[string]bool{"PATH": true, "HOME": true}
	filterWrap := func(c *exec.Cmd) *exec.Cmd {
		filtered := make([]string, 0, len(c.Env))
		for _, kv := range c.Env {
			key, _, ok := strings.Cut(kv, "=")
			if ok && allowed[key] {
				filtered = append(filtered, kv)
			}
		}
		return &exec.Cmd{
			Path: "/wrapped/bin",
			Args: []string{"/wrapped/bin"},
			Env:  filtered,
		}
	}

	spec := ServerSpec{
		Command: "/bin/true",
		Env:     map[string]string{"MCP_TOKEN": "secret"},
	}

	got := buildCommand(context.Background(), spec, filterWrap, io.Discard)

	if !slices.Contains(got.Env, "MCP_TOKEN=secret") {
		t.Errorf("expected spec.Env to survive the filtering wrap, got %v", got.Env)
	}
	for _, kv := range got.Env {
		key, _, _ := strings.Cut(kv, "=")
		if key != "MCP_TOKEN" && !allowed[key] {
			t.Errorf("expected non-allowlisted, non-spec.Env var to be filtered out, found %q in %v", kv, got.Env)
		}
	}
}

func TestBuildCommand_SpecEnvOrderIsDeterministic(t *testing.T) {
	spec := ServerSpec{
		Command: "/bin/true",
		Env:     map[string]string{"ZETA": "1", "ALPHA": "2", "MID": "3"},
	}

	first := buildCommand(context.Background(), spec, nil, io.Discard)
	second := buildCommand(context.Background(), spec, nil, io.Discard)

	specTail := func(env []string) []string {
		return env[len(env)-3:]
	}
	firstTail := specTail(first.Env)
	secondTail := specTail(second.Env)

	if !slices.Equal(firstTail, secondTail) {
		t.Fatalf("spec.Env ordering not deterministic across calls: %v vs %v", firstTail, secondTail)
	}
	want := []string{"ALPHA=2", "MID=3", "ZETA=1"}
	if !slices.Equal(firstTail, want) {
		t.Fatalf("spec.Env not in sorted-key order: got %v, want %v", firstTail, want)
	}
}

func TestBuildCommand_AppliesProcessGroupAfterWrap(t *testing.T) {
	// Mirror sandbox.WrapCommandMode: the wrapper returns a fresh exec.Cmd that
	// copies only Path/Args/Stdin/Stdout/Stderr/Env and drops SysProcAttr,
	// Cancel, and WaitDelay.
	wrap := func(c *exec.Cmd) *exec.Cmd {
		if c.SysProcAttr != nil {
			t.Error("applyProcessGroup ran before wrap")
		}
		if c.WaitDelay != 0 {
			t.Errorf("WaitDelay = %v before wrap, want 0 (applyProcessGroup has not run)", c.WaitDelay)
		}
		return &exec.Cmd{
			Path:       "/wrapped/bin",
			Args:       []string{"/wrapped/bin", "arg"},
			Stdin:      c.Stdin,
			Stdout:     c.Stdout,
			Stderr:     c.Stderr,
			Env:        append([]string{"FILTERED=1"}, c.Env...),
			ExtraFiles: []*os.File{os.Stdin},
		}
	}

	got := buildCommand(context.Background(), ServerSpec{Command: "/bin/true"}, wrap, io.Discard)

	if got.Path != "/wrapped/bin" {
		t.Errorf("Path = %q, want the wrapped command's path", got.Path)
	}
	if !slices.Contains(got.Env, "FILTERED=1") {
		t.Error("Env not copied from the wrapped command")
	}
	if len(got.ExtraFiles) != 1 || got.ExtraFiles[0] != os.Stdin {
		t.Error("ExtraFiles not copied from the wrapped command")
	}
	if got.SysProcAttr == nil || !got.SysProcAttr.Setpgid {
		t.Error("SysProcAttr.Setpgid not applied to the wrapped command")
	}
	if got.Cancel == nil {
		t.Error("Cancel not applied to the wrapped command")
	}
	if got.WaitDelay != 5*time.Second {
		t.Errorf("WaitDelay = %v, want 5s", got.WaitDelay)
	}
	if got.Stdin != nil || got.Stdout != nil {
		t.Error("Stdin/Stdout must stay nil")
	}
}
