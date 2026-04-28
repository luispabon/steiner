package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func TestCommandsVersion(t *testing.T) {
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	if !strings.HasPrefix(got, "steiner ") {
		t.Fatalf("version output = %q, want steiner prefix", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestCommandsConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	writeFile(t, configPath, `model: test
models:
  test:
    type: openai_compat
    base_url: http://example/v1
    model: test-model
    max_completion_tokens: 64
    context_size: 4096
    compaction:
      safety_margin_tokens: 16
      summary_max_tokens: 32
`)
	t.Setenv("HOME", filepath.Join(tempDir, "home"))

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--config", configPath, "config"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var got config.Config
	if err := yaml.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal config output: %v\noutput:\n%s", err, stdout.String())
	}
	if got.Model.Model != "test-model" {
		t.Fatalf("model.Model = %q, want test-model", got.Model.Model)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestCommandsTools(t *testing.T) {
	oldBuildRuntime := buildRuntime
	t.Cleanup(func() { buildRuntime = oldBuildRuntime })

	buildRuntime = func(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
		_ = ctx
		_ = cmd
		_ = flags
		return cliRuntime{
			toolNames: []string{"bash", "read", "write"},
			cfg:       testRuntimeConfig("test-model"),
		}, nil
	}

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"tools"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	for _, name := range []string{"tools:", "  bash", "  read", "  write"} {
		if !strings.Contains(got, name) {
			t.Fatalf("stdout = %q, want %q", got, name)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestCommandsSkills(t *testing.T) {
	oldBuildRuntime := buildRuntime
	t.Cleanup(func() { buildRuntime = oldBuildRuntime })

	buildRuntime = func(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
		_ = ctx
		_ = cmd
		_ = flags
		return cliRuntime{
			skillNames: []string{"review", "debug"},
			cfg:        testRuntimeConfig("test-model"),
		}, nil
	}

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"skills"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	for _, name := range []string{"skills:", "  review", "  debug"} {
		if !strings.Contains(got, name) {
			t.Fatalf("stdout = %q, want %q", got, name)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRenderNames(t *testing.T) {
	t.Run("empty names", func(t *testing.T) {
		var buf bytes.Buffer
		stream := output.NewStream(&buf)
		renderNames(stream, "tools", nil)
		got := buf.String()
		if !strings.Contains(got, "no tools configured") {
			t.Fatalf("output = %q, want no tools configured", got)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		var buf bytes.Buffer
		stream := output.NewStream(&buf)
		renderNames(stream, "skills", []string{})
		got := buf.String()
		if !strings.Contains(got, "no skills configured") {
			t.Fatalf("output = %q, want no skills configured", got)
		}
	})

	t.Run("non-empty names", func(t *testing.T) {
		var buf bytes.Buffer
		stream := output.NewStream(&buf)
		renderNames(stream, "tools", []string{"read", "write"})
		got := buf.String()
		for _, want := range []string{"tools:", "  read", "  write"} {
			if !strings.Contains(got, want) {
				t.Fatalf("output = %q, want %q", got, want)
			}
		}
	})

	t.Run("nil stream", func(t *testing.T) {
		renderNames(nil, "tools", []string{"bash"})
	})
}

func TestRenderNamesUsesProvidedHeading(t *testing.T) {
	var buf bytes.Buffer
	stream := output.NewStream(&buf)
	renderNames(stream, "custom-heading", []string{"alpha"})
	got := buf.String()
	if !strings.Contains(got, "custom-heading:") {
		t.Fatalf("output = %q, want custom-heading:", got)
	}
	if !strings.Contains(got, "  alpha") {
		t.Fatalf("output = %q, want alpha", got)
	}
}
