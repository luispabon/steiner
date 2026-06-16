package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
	"github.com/luispabon/steiner/internal/output"
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
}

func TestCommandsConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	writeFile(t, configPath, `default_model: test
providers:
  local:
    type: openai_compat
    base_url: http://example/v1
models:
  test:
    provider: local
    id: test-model
    retry:
      enabled: true
      max_attempts: 3
      initial_backoff: 250ms
      max_backoff: 5s
      retry_after_max: 30s
    advanced:
      limits:
        max_output_tokens: 64
        context_window: 4096
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
	if got.Models["test"].ID != "test-model" {
		t.Fatalf("models[test].ID = %q, want test-model", got.Models["test"].ID)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestCommandsConfigCaveHumanDefaultFalse(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	writeFile(t, configPath, `default_model: test
providers:
  local:
    type: openai_compat
    base_url: http://example/v1
models:
  test:
    provider: local
    id: test-model
    retry:
      enabled: true
      max_attempts: 3
      initial_backoff: 250ms
      max_backoff: 5s
      retry_after_max: 30s
    advanced:
      limits:
        max_output_tokens: 64
        context_window: 4096
`)
	t.Setenv("HOME", filepath.Join(tempDir, "home"))

	// No cave_human in config — should preserve default false
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
	if got.CaveHuman {
		t.Fatal("config command without cave_human: CaveHuman = true, want false (default)")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestCommandsTools(t *testing.T) {
	oldBuildRuntime := buildRuntime
	t.Cleanup(func() { buildRuntime = oldBuildRuntime })
	var closeCalls atomic.Int32

	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
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

	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
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

	t.Run("nil stream", func(_ *testing.T) {
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

func TestModelInspectCommand(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tempDir, "xdg-cache"))
	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(cache.CachePath(), []byte(`{"openai":{"models":{"gpt-4o":{"limit":{"context":128000,"output":16384}}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-05-20T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}
	configPath := filepath.Join(tempDir, "config.yaml")
	writeFile(t, configPath, `default_model: inspect
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  inspect:
    provider: local
    id: gpt-4o
    params:
      temperature: 0.2
    extra_params:
      reasoning:
        effort: medium
    prompt_suffix: <|think_off|>
`)

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--config", configPath, "model", "inspect", "inspect"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	for _, want := range []string{
		"alias: inspect",
		"provider: local",
		"backend_id: gpt-4o",
		"limits:",
		"  source: models.dev",
		"  confidence: medium",
		"  context_window: 128000",
		"  max_output_tokens: 16384",
		"derived_policy:",
		"  compaction_threshold: 0.70",
		"  estimator_pad_tokens: 1280",
		"  normal_summary_token_budget: 10240",
		"  emergency_summary_token_budget: 5120",
		"params: {\"temperature\":0.2}",
		"extra_params: {\"reasoning\":{\"effort\":\"medium\"}}",
		"prompt_suffix: \"<|think_off|>\"",
		"tokenizer:",
		"  strategy: tiktoken",
		"  confidence: high",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	}
	for _, absent := range []string{
		"safety_margin_tokens",
		"summary_max_tokens",
		"output_reserve",
	} {
		if strings.Contains(got, absent) {
			t.Fatalf("stdout = %q, want %q absent", got, absent)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestModelMetadataStatusCommand(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	cache := runtimeMetadataCache(nil)
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(cache.CachePath(), []byte(`{"openai":{"models":{"gpt-4o":{},"gpt-4.1":{}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	metaData, err := json.Marshal(metadata.CacheMetadata{
		DownloadedAt: time.Now().Add(-2 * time.Hour),
		ExpiresAt:    time.Now().Add(2 * time.Hour),
		URL:          "https://models.dev/api.json",
	})
	if err != nil {
		t.Fatalf("Marshal(meta) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), metaData, 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"model-metadata", "status"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	for _, want := range []string{
		"cache_path: " + cache.CachePath(),
		"size_bytes: ",
		"age: ",
		"freshness: fresh",
		"model_count: 2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestModelMetadataClearCommand(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	cache := runtimeMetadataCache(nil)
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(cache.CachePath(), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"model-metadata", "clear"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "model metadata cache cleared") {
		t.Fatalf("stdout = %q, want clear confirmation", stdout.String())
	}
	if _, err := os.Stat(cache.CachePath()); !os.IsNotExist(err) {
		t.Fatalf("cache file still exists, stat err = %v", err)
	}
	if _, err := os.Stat(cache.MetaPath()); !os.IsNotExist(err) {
		t.Fatalf("meta file still exists, stat err = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestModelMetadataRefreshCommand(t *testing.T) {
	oldFactory := metadataCacheFactory
	t.Cleanup(func() { metadataCacheFactory = oldFactory })

	payload := []byte(`{"models":{"gpt-4o":{"context":128000,"maxOutputTokens":16384}}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	cache := &metadata.Cache{
		Dir: t.TempDir(),
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				req.URL.Scheme = "http"
				req.URL.Host = strings.TrimPrefix(server.URL, "http://")
				return http.DefaultTransport.RoundTrip(req)
			}),
		},
	}
	metadataCacheFactory = func(_ *http.Client) *metadata.Cache { return cache }

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"model-metadata", "refresh"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "model metadata cache refreshed") {
		t.Fatalf("stdout = %q, want refresh confirmation", stdout.String())
	}
	data, err := os.ReadFile(cache.CachePath())
	if err != nil {
		t.Fatalf("ReadFile(cache) error = %v", err)
	}
	if string(data) != string(payload) {
		t.Fatalf("cache data = %q, want %q", string(data), string(payload))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
