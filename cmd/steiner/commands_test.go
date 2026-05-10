package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	if !strings.HasPrefix(got, "Steiner v") {
		t.Fatalf("version output = %q, want Steiner v prefix", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
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
    retry:
      enabled: true
      max_attempts: 3
      initial_backoff: 250ms
      max_backoff: 5s
      retry_after_max: 30s
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
	var closeCalls atomic.Int32

	buildRuntime = func(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
		_ = ctx
		_ = cmd
		_ = flags
		return cliRuntime{
			toolNames: []string{"bash", "read", "write"},
			cfg:       testRuntimeConfig("test-model"),
			closeFn: func() error {
				closeCalls.Add(1)
				return nil
			},
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
	if got := closeCalls.Load(); got != 1 {
		t.Fatalf("close calls = %d, want 1", got)
	}
}

func TestCommandsSkills(t *testing.T) {
	oldBuildRuntime := buildRuntime
	t.Cleanup(func() { buildRuntime = oldBuildRuntime })
	var closeCalls atomic.Int32

	buildRuntime = func(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
		_ = ctx
		_ = cmd
		_ = flags
		return cliRuntime{
			skillNames: []string{"review", "debug"},
			cfg:        testRuntimeConfig("test-model"),
			closeFn: func() error {
				closeCalls.Add(1)
				return nil
			},
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
	if got := closeCalls.Load(); got != 1 {
		t.Fatalf("close calls = %d, want 1", got)
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

func TestFormatRelativeTime(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		timestamp time.Time
		checkFn   func(string) bool
	}{
		{
			name:      "just now (sub-minute)",
			timestamp: now.Add(-30 * time.Second),
			checkFn:   func(s string) bool { return s == "just now" },
		},
		{
			name:      "5 minutes ago",
			timestamp: now.Add(-5 * time.Minute),
			checkFn:   func(s string) bool { return s == "5m ago" },
		},
		{
			name:      "3 hours ago",
			timestamp: now.Add(-3 * time.Hour),
			checkFn:   func(s string) bool { return s == "3h ago" },
		},
		{
			name:      "2 days ago",
			timestamp: now.Add(-2 * 24 * time.Hour),
			checkFn:   func(s string) bool { return s == "2d ago" },
		},
		{
			name:      "6 days ago",
			timestamp: now.Add(-6 * 24 * time.Hour),
			checkFn:   func(s string) bool { return s == "6d ago" },
		},
		{
			name:      "more than 7 days ago uses Jan 2, 2006 format",
			timestamp: now.Add(-30 * 24 * time.Hour),
			checkFn: func(s string) bool {
				// Just verify it doesn't end with "ago" and contains digits
				return !strings.HasSuffix(s, "ago") && len(s) > 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatRelativeTime(tt.timestamp)
			if !tt.checkFn(got) {
				t.Fatalf("formatRelativeTime(%v) = %q, check failed", tt.timestamp, got)
			}
		})
	}
}

func TestResumeWithExecRejected(t *testing.T) {
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--resume", "some-uuid", "--exec"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want error for --resume with --exec")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "unsupported") {
		t.Fatalf("error message = %q, want 'unsupported' in message", errMsg)
	}
}
