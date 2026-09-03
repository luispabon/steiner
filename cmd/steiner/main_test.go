package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"gopkg.in/yaml.v3"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// restoreEnv restores key to its previous value, unsetting it when it was not
// present before. Setting an empty string is not equivalent: DefaultCacheDir
// treats a set-but-empty XDG_CACHE_HOME differently from an absent one.
func restoreEnv(key, value string, present bool) error {
	if !present {
		return os.Unsetenv(key)
	}
	return os.Setenv(key, value)
}

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "steiner-cmd-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir for cmd tests: %v\n", err)
		os.Exit(1)
	}

	fakeBwrap := filepath.Join(tmp, "bwrap")
	if err := os.WriteFile(fakeBwrap, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write fake bwrap: %v\n", err)
		os.Exit(1)
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", tmp+string(os.PathListSeparator)+oldPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set PATH for cmd tests: %v\n", err)
		os.Exit(1)
	}

	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmp); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set HOME for cmd tests: %v\n", err)
		os.Exit(1)
	}

	// metadata.DefaultCacheDir prefers XDG_CACHE_HOME over HOME, so overriding
	// HOME alone leaves tests reading and writing the developer's real
	// ~/.cache/steiner (and fetching models.dev over the network) on any machine
	// where XDG_CACHE_HOME is set. Redirect both to keep the suite hermetic.
	oldCacheHome, hadCacheHome := os.LookupEnv("XDG_CACHE_HOME")
	if err := os.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, ".cache")); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set XDG_CACHE_HOME for cmd tests: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if err := restoreEnv("XDG_CACHE_HOME", oldCacheHome, hadCacheHome); err != nil {
		fmt.Fprintf(os.Stderr, "failed to restore XDG_CACHE_HOME for cmd tests: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("HOME", oldHome); err != nil {
		fmt.Fprintf(os.Stderr, "failed to restore HOME for cmd tests: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("PATH", oldPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to restore PATH for cmd tests: %v\n", err)
		os.Exit(1)
	}
	if err := os.RemoveAll(tmp); err != nil {
		fmt.Fprintf(os.Stderr, "failed to remove temp dir for cmd tests: %v\n", err)
		os.Exit(1)
	}
	if cliHelperBinaryDir != "" {
		if err := os.RemoveAll(cliHelperBinaryDir); err != nil {
			fmt.Fprintf(os.Stderr, "failed to remove helper binary dir: %v\n", err)
			os.Exit(1)
		}
	}
	if mcpFixtureBinaryDir != "" {
		if err := os.RemoveAll(mcpFixtureBinaryDir); err != nil {
			fmt.Fprintf(os.Stderr, "failed to remove fixture binary dir: %v\n", err)
			os.Exit(1)
		}
	}
	os.Exit(code)
}

func TestVersionCommandPrintsVersion(t *testing.T) {
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, version) {
		t.Fatalf("version output = %q, want version %q in output", got, version)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestVersionPanel_IncludesAllKeys(t *testing.T) {
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	for _, key := range []string{"version", "commit", "built", "go", "channel"} {
		if !strings.Contains(got, key) {
			t.Errorf("output missing key %q: %q", key, got)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestConfigCommandPrintsResolvedConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	projectDir := filepath.Join(tempDir, "project")
	globalDir := filepath.Join(homeDir, ".config", "steiner")
	projectConfigDir := filepath.Join(projectDir, ".steiner")

	mustMkdirAll(t, globalDir)
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(globalDir, "config.yaml"), `providers:
  global-provider:
    type: openai_compat
    base_url: http://global.example/v1
models:
  definitions:
    global:
      provider: global-provider
      id: global-backend
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
      advanced:
        limits:
          max_output_tokens: 2048
          context_window: 8192
  profiles:
    default:
      default_model: global
limits:
  max_turns: 25
paths:
  project_root_only: false
`)
	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  project-provider:
    type: openai_compat
    base_url: http://project.example/v1
  cli-provider:
    type: openai_compat
    base_url: http://cli.example/v1
models:
  definitions:
    project:
      provider: project-provider
      id: project-backend
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
      advanced:
        limits:
          max_output_tokens: 4096
          context_window: 32768
    cli:
      provider: cli-provider
      id: cli-backend
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
      advanced:
        limits:
          max_output_tokens: 8192
          context_window: 65536
  profiles:
    default:
      default_model: project
limits:
  max_turns: 10
logging:
  level: warn
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeDir)

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--model", "cli", "--verbose", "config"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var got config.Config
	if err := yaml.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal config output: %v\noutput:\n%s", err, stdout.String())
	}
	if got.Models.Profiles["default"].DefaultModel != "project" {
		t.Fatalf("default_model = %q, want project", got.Models.Profiles["default"].DefaultModel)
	}
	if got.Models.Definitions["cli"].ID != "cli-backend" {
		t.Fatalf("models[cli].ID = %q, want cli-backend", got.Models.Definitions["cli"].ID)
	}
	if got.Limits.MaxTurns != 10 {
		t.Fatalf("limits.max_turns = %d, want 10", got.Limits.MaxTurns)
	}
	if got.Logging.Level != "debug" {
		t.Fatalf("logging.level = %q, want debug", got.Logging.Level)
	}
	if got.Paths.ProjectRootOnly {
		t.Fatalf("paths.project_root_only = %v, want false", got.Paths.ProjectRootOnly)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestBuildActiveRegistryMatchesDelegateRegistry(t *testing.T) {
	base := tool.NewRegistry(
		tool.ToolDef{Name: "bash", Description: "run shell commands"},
		tool.ToolDef{Name: "read", Description: "read files"},
	)

	cfg := config.Config{
		CaveHuman: true,
		Providers: map[string]config.ProviderConfig{
			"testprov": {
				Type:    config.ProviderTypeOpenAICompat,
				BaseURL: "http://localhost:11434/v1",
			},
		},
		Models: config.ModelsConfig{
			Effective: config.EffectiveModelAssignments{Advisor: "advisor-alias"},
			Definitions: map[string]config.ModelConfig{
				"advisor-alias": {
					Provider: "testprov",
					ID:       "advisor-model",
					Advanced: config.AdvancedConfig{
						Limits: config.AdvancedLimitsConfig{
							ContextWindow:   8192,
							MaxOutputTokens: 1024,
						},
					},
				},
			},
		},
		ProjectContext: config.ProjectContextConfig{
			MaxTokens: 2048,
		},
	}
	subAgentCfg := config.SubAgentConfig{
		Enabled:   true,
		MaxTurns:  5,
		MaxTokens: 256,
	}
	advisorCfg := config.AdvisorConfig{
		Enabled:       true,
		MaxUsesPerRun: 2,
	}
	resolvedModel := provider.ResolvedModel{
		ProviderAlias:         "testprov",
		EffectiveProviderType: config.ProviderTypeOpenAICompat,
	}

	want, err := delegation.BuildDelegateRegistry(delegation.DelegateDeps{
		BaseRegistry:  base,
		SubAgentCfg:   subAgentCfg,
		AdvisorCfg:    advisorCfg,
		Provider:      stubProvider{},
		Events:        noopSink{},
		WorkDir:       "/tmp/work",
		ResolvedModel: resolvedModel,
		MaxTokens:     256,
		Config:        cfg,
	})
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}

	got, err := buildActiveRegistry(base, subAgentCfg, advisorCfg, stubProvider{}, noopSink{}, "/tmp/work", "", resolvedModel, 256, false, nil, cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildActiveRegistry() error = %v", err)
	}

	if !reflect.DeepEqual(got.Names(), want.Names()) {
		t.Fatalf("registry names differ:\ncmd/steiner = %v\ninternal/delegation = %v", got.Names(), want.Names())
	}
	if !reflect.DeepEqual(base.Names(), []string{"bash", "read"}) {
		t.Fatalf("base registry mutated: got %v", base.Names())
	}
}

func TestConfigCommandFailsForMissingExplicitConfigPath(t *testing.T) {
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "missing.yaml"), "config"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want missing config path error")
	}
	if !strings.Contains(stderr.String(), "does not exist") {
		t.Fatalf("stderr = %q, want missing-path error", stderr.String())
	}
}

func TestConfigCommandFailsForInvalidConfigContent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	configPath := filepath.Join(tempDir, "broken.yaml")
	writeFile(t, configPath, `provider:
  type: unsupported
`)
	t.Setenv("HOME", homeDir)

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--config", configPath, "config"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid config error")
	}
	if !strings.Contains(stderr.String(), "parse config") {
		t.Fatalf("stderr = %q, want parse error", stderr.String())
	}
	if !strings.Contains(stderr.String(), "provider") {
		t.Fatalf("stderr = %q, want old provider schema validation failure", stderr.String())
	}
}

func TestDefaultBuildRuntimeResolvesSelectedModel(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  fast-provider:
    type: openai_compat
    base_url: http://fast.example/v1
  slow-provider:
    type: openai_compat
    base_url: http://slow.example/v1
    headers:
      X-Test-Header: slow
    timeout: 45s
models:
  definitions:
    fast:
      provider: fast-provider
      id: fast-backend
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
      advanced:
        limits:
          max_output_tokens: 256
          context_window: 4096
    slow:
      provider: slow-provider
      id: slow-backend
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
      advanced:
        limits:
          max_output_tokens: 512
          context_window: 8192
  profiles:
    default:
      default_model: slow
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("STEINER_MODEL", "slow")

	oldNewOpenAICompat := newOpenAICompat
	t.Cleanup(func() {
		newOpenAICompat = oldNewOpenAICompat
	})

	var gotProviderConfig provider.ClientConfig
	newOpenAICompat = func(cfg provider.ClientConfig) (provider.Provider, error) {
		gotProviderConfig = cfg
		return &fakeProvider{}, nil
	}

	cmd := newRootCommand()
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	rt, err := defaultBuildRuntime(context.Background(), cmd, &cliFlags{})
	if err != nil {
		t.Fatalf("defaultBuildRuntime() error = %v", err)
	}
	rm, err := provider.Resolve(rt.cfg, rt.cfg.Models.Effective.ActiveOrchestratorModel)
	if err != nil {
		t.Fatalf("provider.Resolve() error = %v", err)
	}
	builtProvider, err := rt.providerFactory(rm, "test-session")
	if err != nil {
		t.Fatalf("providerFactory() error = %v", err)
	}
	if builtProvider == nil {
		t.Fatal("providerFactory() = nil, want provider")
	}
	if gotProviderConfig.BaseURL != "http://slow.example/v1" {
		t.Fatalf("provider base_url = %q, want slow alias base_url", gotProviderConfig.BaseURL)
	}
	if gotProviderConfig.Headers["X-Test-Header"] != "slow" {
		t.Fatalf("provider headers = %#v, want X-Test-Header=slow", gotProviderConfig.Headers)
	}
	if gotProviderConfig.Model != "slow-backend" {
		t.Fatalf("provider model = %q, want slow-backend", gotProviderConfig.Model)
	}
	if gotProviderConfig.Timeout != 45*time.Second {
		t.Fatalf("provider timeout = %v, want 45s", gotProviderConfig.Timeout)
	}
	if got := gotProviderConfig.Retry; got != (provider.RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: 250 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		RetryAfterMax:  30 * time.Second,
	}) {
		t.Fatalf("provider retry = %#v, want default retry config", got)
	}
	if gotProviderConfig.HTTPClient == nil {
		t.Fatal("provider HTTP client = nil, want client")
	}
}

func TestRuntimeRegistryIncludesCoreToolsByDefault(t *testing.T) {
	registry := runtimeRegistry(config.Config{
		Limits: config.LimitsConfig{
			ToolTimeoutDefault: config.MustDuration("30s"),
		},
		Tools: map[string]config.ToolConfig{},
	}, t.TempDir())

	got := registry.Names()
	want := []string{"bash", "display_file", "fetch_url", "glob", "grep", "ls", "mutate", "read", "workflow_handoff"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registry names = %v, want %v", got, want)
	}

	bashDef, ok := registry.Get("bash")
	if !ok {
		t.Fatal("Get(bash) = false, want true")
	}
	properties, ok := bashDef.ParameterSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("bash properties type = %T, want map[string]any", bashDef.ParameterSchema["properties"])
	}
	commandSchema, ok := properties["command"].(map[string]any)
	if !ok {
		t.Fatalf("bash command schema type = %T, want map[string]any", properties["command"])
	}
	if _, found := commandSchema["_required"]; found {
		t.Fatalf("bash command schema leaked internal _required field: %#v", commandSchema)
	}
}

func TestExecModeRunsSinglePromptHeadlessly(t *testing.T) {
	oldBuildRuntime := buildRuntime
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
	})

	var stdout, stderr bytes.Buffer
	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
		cfg := testRuntimeConfig("test-model")
		return cliRuntime{
			cfg: cfg,
			provider: &fakeProvider{
				responses: []provider.ChatResponse{
					{
						Message: provider.Message{
							Role:    provider.MessageRoleAssistant,
							Content: "final answer",
						},
						FinishReason: "stop",
						Usage:        &provider.UsageStats{TotalTokens: 2},
					},
				},
			},
			registry:    tool.NewRegistry(),
			toolNames:   nil,
			skillNames:  nil,
			workDir:     t.TempDir(),
			homeDir:     t.TempDir(),
			human:       output.NewStream(&stdout),
			status:      output.NewStream(&stderr),
			events:      output.NewStream(&stdout),
			sharedInput: bufio.NewReader(strings.NewReader("")),
		}, nil
	}

	cmd := newRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--exec", "fix the bug"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := stdout.String(); !strings.Contains(got, "final answer") {
		t.Fatalf("stdout = %q, want final answer", got)
	}
	if got := stdout.String(); !strings.Contains(got, "status: run complete after 1 turn") {
		t.Fatalf("stdout = %q, want stop reason", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestCLIRunnerReturnsCancelledDiagnosticsWithoutError(t *testing.T) {
	runner := cliRunner{
		runtime: cliRuntime{
			cfg:      testRuntimeConfig("test-model"),
			provider: &fakeProvider{},
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			events:   output.NoopSink{},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := runner.Run(ctx, []agent.Message{{Role: agent.MessageRoleUser, Content: "fix the bug"}}, nil, nil)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("Diagnostics len = %d, want 1", len(result.Diagnostics))
	}
	stop, ok := result.Diagnostics[0].Payload.(output.StopReasonEvent)
	if !ok {
		t.Fatalf("diagnostic payload type = %T, want output.StopReasonEvent", result.Diagnostics[0].Payload)
	}
	if got, want := stop.Reason, "cancelled"; got != want {
		t.Fatalf("stop reason = %q, want %q", got, want)
	}
}

func TestCLIRunnerEmitsRunLifecycleEvents(t *testing.T) {
	var events []output.Event
	runner := cliRunner{
		runtime: cliRuntime{
			cfg: testRuntimeConfig("test-model"),
			provider: &fakeProvider{
				responses: []provider.ChatResponse{
					{
						Message: provider.Message{
							Role:    provider.MessageRoleAssistant,
							Content: "final answer",
						},
						FinishReason: "stop",
						Usage:        &provider.UsageStats{TotalTokens: 3},
					},
				},
			},
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			events: output.SinkFunc(func(event output.Event) {
				events = append(events, event)
			}),
		},
		runMode: "interactive",
	}

	_, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "fix the bug"}}, nil, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(events) < 2 {
		t.Fatalf("events len = %d, want at least 2", len(events))
	}
	start, ok := events[0].Payload.(output.RunStartedEvent)
	if !ok {
		t.Fatalf("first payload type = %T, want output.RunStartedEvent", events[0].Payload)
	}
	if got, want := start.Mode, "interactive"; got != want {
		t.Fatalf("run started mode = %q, want %q", got, want)
	}
	if got, want := start.Prompt, "fix the bug"; got != want {
		t.Fatalf("run started prompt = %q, want %q", got, want)
	}

	last := events[len(events)-1]
	finished, ok := last.Payload.(output.RunFinishedEvent)
	if !ok {
		t.Fatalf("last payload type = %T, want output.RunFinishedEvent", last.Payload)
	}
	if got, want := finished.Reason, "complete"; got != want {
		t.Fatalf("run finished reason = %q, want %q", got, want)
	}
	if got, want := finished.Summary, "final answer"; got != want {
		t.Fatalf("run finished summary = %q, want %q", got, want)
	}
}

func TestCLIRunnerEmitsFallbackWarningOncePerModel(t *testing.T) {
	resetFallbackModelWarnings()
	t.Cleanup(resetFallbackModelWarnings)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	var stderr bytes.Buffer
	cfg := testRuntimeConfig("unknown")
	cfg.Models.Definitions["unknown"] = config.ModelConfig{
		Provider: "local",
		ID:       "custom-unknown-model",
	}

	runner := cliRunner{
		runtime: cliRuntime{
			cfg:      cfg,
			provider: &fakeProvider{},
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			status:   output.NewStream(&stderr),
			events:   output.NoopSink{},
		},
	}

	for i := 0; i < 2; i++ {
		if _, err := runner.prepareRun(nil, nil); err != nil {
			t.Fatalf("prepareRun() error = %v", err)
		}
	}

	const want = "Model metadata warning: unknown/custom-unknown-model has unknown context limits. Using conservative fallback: context_window=32768, max_output_tokens=4096. Set models.unknown.advanced.limits.context_window to remove this warning.\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestPrepareRunReasoningOverrideScope(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	tests := []struct {
		name             string
		override         func() provider.ReasoningOverride
		wantResolved     string
		wantBaseResolved string
	}{
		{
			name: "effort override",
			override: func() provider.ReasoningOverride {
				return provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: "max"}
			},
			wantResolved:     "max",
			wantBaseResolved: "high",
		},
		{
			name:             "no override",
			wantResolved:     "high",
			wantBaseResolved: "high",
		},
		{
			name: "provider default override",
			override: func() provider.ReasoningOverride {
				return provider.ReasoningOverride{Kind: provider.ReasoningOverrideProviderDefault}
			},
			wantResolved:     "",
			wantBaseResolved: "high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testRuntimeConfig("default")
			modelCfg := cfg.Models.Definitions["default"]
			modelCfg.Advanced.Reasoning = config.ReasoningConfig{
				Effort:           "high",
				SupportedEfforts: []string{"low", "medium", "high", "max"},
			}
			cfg.Models.Definitions["default"] = modelCfg
			runner := cliRunner{
				runtime: cliRuntime{
					cfg:      cfg,
					provider: &fakeProvider{},
					workDir:  t.TempDir(),
					homeDir:  t.TempDir(),
					status:   output.NewStream(io.Discard),
					events:   output.NoopSink{},
				},
				currentReasoningOverride: tt.override,
			}

			setup, err := runner.prepareRun(nil, nil)
			if err != nil {
				t.Fatalf("prepareRun() error = %v", err)
			}
			if got := setup.resolvedModel.ReasoningEffectiveEffort; got != tt.wantResolved {
				t.Errorf("resolvedModel.ReasoningEffectiveEffort = %q, want %q", got, tt.wantResolved)
			}
			if got := setup.baseResolvedModel.ReasoningEffectiveEffort; got != tt.wantBaseResolved {
				t.Errorf("baseResolvedModel.ReasoningEffectiveEffort = %q, want %q", got, tt.wantBaseResolved)
			}
		})
	}
}

func TestExecModeWritesFullLogFile(t *testing.T) {
	oldBuildRuntime := buildRuntime
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
	})

	logPath := filepath.Join(t.TempDir(), "session.log")
	buildRuntime = func(_ context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
		fileSink, err := output.NewFileLogSink(flags.logFile, true)
		if err != nil {
			return cliRuntime{}, err
		}
		events := output.NewMultiSink(output.NewStream(cmd.ErrOrStderr()), fileSink)
		cfg := testRuntimeConfig("test-model")
		return cliRuntime{
			cfg: cfg,
			provider: loggingProvider{
				inner: &fakeProvider{
					responses: []provider.ChatResponse{
						{
							Message: provider.Message{
								Role:    provider.MessageRoleAssistant,
								Content: "logged answer",
							},
							FinishReason: "stop",
							Usage:        &provider.UsageStats{TotalTokens: 3},
						},
					},
				},
				sink: events,
			},
			registry:    tool.NewRegistry(),
			workDir:     t.TempDir(),
			homeDir:     t.TempDir(),
			human:       output.NewStream(cmd.OutOrStdout()),
			status:      output.NewStream(cmd.ErrOrStderr()),
			events:      events,
			sharedInput: bufio.NewReader(strings.NewReader("")),
			closeFn:     fileSink.Close,
		}, nil
	}

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--exec", "--log-file", logPath, "fix the bug"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", logPath, err)
	}
	logText := string(data)
	for _, want := range []string{
		"user_input",
		"fix the bug",
		"api_request",
		`"role": "user"`,
		"api_response",
		`"content": "logged answer"`,
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log file missing %q\nlog:\n%s", want, logText)
		}
	}
}

func TestExecModeMaxTurnsFlagOverridesConfig(t *testing.T) {
	oldBuildRuntime := buildRuntime
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
	})

	var stdout, stderr bytes.Buffer
	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
		cfg := testRuntimeConfig("test-model")
		cfg.Limits.MaxTurns = 4
		cfg.Limits.MaxTokens = 256
		return cliRuntime{
			cfg: cfg,
			provider: &fakeProvider{
				responses: []provider.ChatResponse{
					{
						Message: provider.Message{
							Role: provider.MessageRoleAssistant,
							ToolCalls: []provider.ToolCall{
								{ID: "call_1", Name: "noop", Arguments: map[string]any{}},
							},
						},
						FinishReason: "tool_calls",
					},
					{
						Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "done"},
						FinishReason: "stop",
					},
				},
			},
			registry: tool.NewRegistry(tool.ToolDef{
				Name:            "noop",
				Description:     "No-op tool",
				ParameterSchema: map[string]any{"type": "object"},
				Handler: func(_ context.Context, _ map[string]any) (any, error) {
					return map[string]any{"ok": true}, nil
				},
			}),
			workDir:     t.TempDir(),
			homeDir:     t.TempDir(),
			human:       output.NewStream(&stdout),
			status:      output.NewStream(&stderr),
			events:      output.NewStream(&stdout),
			sharedInput: bufio.NewReader(strings.NewReader("")),
		}, nil
	}

	cmd := newRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--exec", "--max-turns", "1", "run noop"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "status: stopped after 1 turn: reached the max turn limit") {
		t.Fatalf("stdout = %q, want max turn stop from flag override", got)
	}
}

func TestCLIRunnerPassesRegistryToolsToProvider(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "final answer",
				},
				FinishReason: "stop",
			},
		},
	}

	runner := cliRunner{
		runtime: cliRuntime{
			cfg:      testRuntimeConfig("test-model"),
			provider: providerStub,
			registry: tool.NewRegistry(
				tool.ToolDef{
					Name:        "glob",
					Description: "Find files",
					ParameterSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"pattern": map[string]any{"type": "string"},
						},
					},
				},
			),
			workDir: t.TempDir(),
			homeDir: t.TempDir(),
			events:  output.NoopSink{},
		},
	}

	_, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "list files"}}, nil, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	if got, want := len(providerStub.requests[0].Tools), 1; got != want {
		t.Fatalf("request tools = %d, want %d", got, want)
	}
	if got, want := providerStub.requests[0].Tools[0].Function.Name, "glob"; got != want {
		t.Fatalf("tool name = %q, want %q", got, want)
	}
}

func TestCLIRunnerUsesSelectedSkillSubset(t *testing.T) {
	skillsRoot := filepath.Join(t.TempDir(), ".config", "steiner", "skills")
	mustMkdirAll(t, filepath.Join(skillsRoot, "review"))
	mustMkdirAll(t, filepath.Join(skillsRoot, "debug"))
	writeFile(t, filepath.Join(skillsRoot, "review", "SKILL.md"), "review skill instructions")
	writeFile(t, filepath.Join(skillsRoot, "debug", "SKILL.md"), "debug skill instructions")

	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "final answer",
				},
				FinishReason: "stop",
			},
		},
	}

	runner := cliRunner{
		runtime: cliRuntime{
			cfg:      testRuntimeConfig("test-model"),
			provider: providerStub,
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  filepath.Dir(filepath.Dir(filepath.Dir(skillsRoot))),
			events:   output.NoopSink{},
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]agent.Message{{Role: agent.MessageRoleUser, Content: "fix the bug"}},
		[]string{"review"},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}

	var contents []string
	for _, message := range providerStub.requests[0].Messages {
		contents = append(contents, message.Content)
	}
	joined := strings.Join(contents, "\n")
	if !strings.Contains(joined, "review skill instructions") {
		t.Fatalf("request messages missing enabled skill content:\n%s", joined)
	}
	if strings.Contains(joined, "debug skill instructions") {
		t.Fatalf("request messages included disabled skill content:\n%s", joined)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func testRuntimeConfig(alias string) config.Config {
	modelCfg := config.ModelConfig{
		Provider: "local",
		ID:       alias,
		Advanced: config.AdvancedConfig{
			Limits: config.AdvancedLimitsConfig{
				MaxOutputTokens: 64,
				ContextWindow:   4096,
			},
		},
	}
	return config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{
			Effective: config.EffectiveModelAssignments{
				DefaultModel:            alias,
				ActiveOrchestratorModel: alias,
			},
			Definitions: map[string]config.ModelConfig{alias: modelCfg},
		},
		Limits: config.LimitsConfig{
			MaxTurns:           4,
			MaxTokens:          64,
			ToolTimeoutDefault: config.MustDuration("30s"),
		},
		ProjectContext: config.ProjectContextConfig{
			MaxTokens: 128,
		},
	}
}

func TestInteractiveSkillsSnapshotTracksEnabledSubset(t *testing.T) {
	skills := interactive.NewSkills([]string{"review", "debug", "test"})
	if got := skills.Snapshot(); len(got) != 0 {
		t.Fatalf("initial Snapshot() = %v, want none enabled", got)
	}

	skills.Set("review", true)
	skills.Set("debug", false)
	skills.Set("test", false)
	if got, want := skills.Snapshot(), []string{"review"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered Snapshot() = %v, want %v", got, want)
	}
}

func TestCLIRunnerReturnsContextDiagnostics(t *testing.T) {
	helper := mustBuildCLIHelperBinary(t)
	workDir := t.TempDir()
	writeFile(t, filepath.Join(workDir, "README.md"), strings.Repeat("project context line\n", 64))

	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "bash", Arguments: map[string]any{"command": "pwd"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_2", Name: "bash", Arguments: map[string]any{"command": "pwd"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
			},
		},
	}

	runner := cliRunner{
		runtime: cliRuntime{
			cfg: func() config.Config {
				cfg := testRuntimeConfig("test-model")
				cfg.Limits.MaxTurns = 6
				cfg.Limits.MaxTokens = 100
				cfg.ProjectContext.MaxBytes = 64
				return cfg
			}(),
			provider: providerStub,
			registry: tool.NewRegistry(tool.ToolDef{
				Name:       "bash",
				ExecPath:   helper,
				Subcommand: "bash",
			}),
			workDir:     workDir,
			homeDir:     t.TempDir(),
			events:      output.NoopSink{},
			sharedInput: bufio.NewReader(strings.NewReader("")),
		},
	}

	result, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "run bash"}}, nil, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(result.Diagnostics) == 0 {
		t.Fatal("Diagnostics = empty, want retained diagnostics")
	}
	var kinds []string
	foundStopReason := false
	for _, event := range result.Diagnostics {
		switch event.Type {
		case output.EventTypeContextDiagnostics:
			kinds = append(kinds, output.ContextDiagnosticKind(event.Payload))
		case output.EventTypeStopReason:
			payload, ok := event.Payload.(output.StopReasonEvent)
			if !ok {
				t.Fatalf("stop payload type = %T, want output.StopReasonEvent", event.Payload)
			}
			if payload.Summary == "" {
				t.Fatalf("stop reason summary = empty, want actionable summary")
			}
			foundStopReason = true
		}
	}
	if !containsString(kinds, "budget") {
		t.Fatalf("diagnostic kinds = %v, want budget event", kinds)
	}
	if !foundStopReason {
		t.Fatalf("result diagnostics = %#v, want stop reason event", result.Diagnostics)
	}
}

func TestCLIRunnerPropagatesSelectedModelBudgetToLiveRunRequest(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "should not be reached",
				},
				FinishReason: "stop",
			},
		},
	}

	modelCfg := config.ModelConfig{
		Provider: "local",
		ID:       "test-model",
		Advanced: config.AdvancedConfig{
			Limits: config.AdvancedLimitsConfig{
				MaxOutputTokens: 64,
				ContextWindow:   1,
			},
		},
	}
	cfg := testRuntimeConfig("test-model")
	cfg.Models.Definitions["test-model"] = modelCfg

	runner := cliRunner{
		runtime: cliRuntime{
			cfg:      cfg,
			provider: providerStub,
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			events:   output.NoopSink{},
		},
	}

	_, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "fix the bug"}}, nil, nil)
	if err == nil {
		t.Fatal("Run() error = nil, want irreducible compaction failure")
	}
	if !strings.Contains(err.Error(), "compaction cannot solve this request") {
		t.Fatalf("Run() error = %v, want irreducible compaction failure", err)
	}
	if got, want := len(providerStub.requests), 0; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
}

func TestRequestSnapshotStoreStartsEmpty(t *testing.T) {
	store := &interactive.SnapshotStore{}
	if _, ok := store.Snapshot(); ok {
		t.Fatal("Snapshot() ok = true, want false")
	}
}

func TestCLIRunnerUpdatesSnapshotBudgetWhenModelChanges(t *testing.T) {
	store := &interactive.SnapshotStore{}
	sink := output.SinkFunc(func(event output.Event) {
		payload, ok := event.Payload.(output.APIRequestEvent)
		if !ok {
			return
		}
		store.Store(interactive.RequestContextSnapshot{
			Model:       payload.Model,
			Messages:    payload.Messages,
			Tools:       payload.Tools,
			MaxTokens:   payload.MaxTokens,
			Blocks:      payload.Blocks,
			ModelBudget: payload.ModelBudget,
		})
	})

	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "one"},
				FinishReason: "stop",
			},
			{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "two"},
				FinishReason: "stop",
			},
		},
	}

	models := map[string]config.ModelConfig{
		"small": {
			Provider: "local",
			ID:       "gpt-4o-mini",
			Advanced: config.AdvancedConfig{
				Limits: config.AdvancedLimitsConfig{
					MaxOutputTokens: 32,
					ContextWindow:   4096,
				},
			},
		},
		"large": {
			Provider: "local",
			ID:       "gpt-4o",
			Advanced: config.AdvancedConfig{
				Limits: config.AdvancedLimitsConfig{
					MaxOutputTokens: 96,
					ContextWindow:   8192,
				},
			},
		},
	}
	cfg := testRuntimeConfig("small")
	cfg.Models.Definitions = models

	runner := cliRunner{
		runtime: cliRuntime{
			cfg:      cfg,
			provider: providerStub,
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			events:   sink,
		},
	}

	if _, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "first"}}, nil, nil); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	first, ok := store.Snapshot()
	if !ok {
		t.Fatal("first Snapshot() ok = false, want true")
	}
	if got, want := first.ModelBudget.ContextSize, 4096; got != want {
		t.Fatalf("first context size = %d, want %d", got, want)
	}
	if got, want := first.ModelBudget.MaxCompletionTokens, 32; got != want {
		t.Fatalf("first max completion tokens = %d, want %d", got, want)
	}

	runner.runtime.cfg.Models.Effective.ActiveOrchestratorModel = "large"
	if _, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "second"}}, nil, nil); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	second, ok := store.Snapshot()
	if !ok {
		t.Fatal("second Snapshot() ok = false, want true")
	}
	if got, want := second.ModelBudget.ContextSize, 8192; got != want {
		t.Fatalf("second context size = %d, want %d", got, want)
	}
	if got, want := second.ModelBudget.MaxCompletionTokens, 96; got != want {
		t.Fatalf("second max completion tokens = %d, want %d", got, want)
	}
}

func TestCLIRunnerUsesCurrentModelCallback(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "first"},
				FinishReason: "stop",
			},
			{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "second"},
				FinishReason: "stop",
			},
		},
	}

	models := map[string]config.ModelConfig{
		"small": {
			Provider: "local",
			ID:       "gpt-4o-mini",
			Advanced: config.AdvancedConfig{
				Limits: config.AdvancedLimitsConfig{
					MaxOutputTokens: 32,
					ContextWindow:   4096,
				},
			},
		},
		"large": {
			Provider: "local",
			ID:       "gpt-4o",
			Advanced: config.AdvancedConfig{
				Limits: config.AdvancedLimitsConfig{
					MaxOutputTokens: 96,
					ContextWindow:   8192,
				},
			},
		},
	}

	callIndex := 0
	runner := cliRunner{
		runtime: cliRuntime{
			cfg: func() config.Config {
				cfg := testRuntimeConfig("small")
				cfg.Models.Definitions = models
				return cfg
			}(),
			provider: providerStub,
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			events:   output.NoopSink{},
		},
		currentModel: func() config.ModelConfig {
			defer func() { callIndex++ }()
			if callIndex == 0 {
				return models["small"]
			}
			return models["large"]
		},
	}

	_, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "first"}}, nil, nil)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	firstReq := providerStub.requests[0]
	if firstReq.Model != "gpt-4o-mini" {
		t.Fatalf("first request model = %q, want %q", firstReq.Model, "gpt-4o-mini")
	}

	_, err = runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "second"}}, nil, nil)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	secondReq := providerStub.requests[1]
	if secondReq.Model != "gpt-4o" {
		t.Fatalf("second request model = %q, want %q", secondReq.Model, "gpt-4o")
	}
}

func TestCLIRunnerUsesSessionCurrentModelAliasCallback(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "first"},
				FinishReason: "stop",
			},
			{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "second"},
				FinishReason: "stop",
			},
		},
	}

	cfg := testRuntimeConfig("small")
	cfg.Models.Definitions = map[string]config.ModelConfig{
		"small": {
			Provider: "local",
			ID:       "gpt-4o-mini",
			Advanced: config.AdvancedConfig{
				Limits: config.AdvancedLimitsConfig{
					MaxOutputTokens: 32,
					ContextWindow:   4096,
				},
			},
		},
		"large": {
			Provider: "local",
			ID:       "gpt-4o",
			Advanced: config.AdvancedConfig{
				Limits: config.AdvancedLimitsConfig{
					MaxOutputTokens: 96,
					ContextWindow:   8192,
				},
			},
		},
	}

	sess, err := interactive.NewSession(interactive.Dependencies{Config: cfg})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	runner := cliRunner{
		runtime: cliRuntime{
			cfg:      cfg,
			provider: providerStub,
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			events:   output.NoopSink{},
		},
		currentAlias: sess.CurrentModelAlias,
	}

	_, err = runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "first"}}, nil, nil)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if got, want := providerStub.requests[0].Model, "gpt-4o-mini"; got != want {
		t.Fatalf("first request model = %q, want %q", got, want)
	}

	if err := sess.Handle(context.Background(), interactive.SwitchModel{Name: "large"}); err != nil {
		t.Fatalf("Handle(SwitchModel) error = %v", err)
	}
	_, err = runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "second"}}, nil, nil)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if got, want := providerStub.requests[1].Model, "gpt-4o"; got != want {
		t.Fatalf("second request model = %q, want %q", got, want)
	}
	if got, want := cfg.Models.Effective.DefaultModel, "small"; got != want {
		t.Fatalf("runtime config default model = %q, want %q", got, want)
	}
}

func TestCLIRunnerPropagatesExtraParamsToProvider(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "done"},
				FinishReason: "stop",
			},
		},
	}

	modelCfg := config.ModelConfig{
		Provider:    "local",
		ID:          "test-model",
		ExtraParams: map[string]any{"temperature": 0.7, "top_p": 0.9},
		Advanced: config.AdvancedConfig{
			Limits: config.AdvancedLimitsConfig{
				MaxOutputTokens: 64,
				ContextWindow:   4096,
			},
		},
	}
	cfg := testRuntimeConfig("test-model")
	cfg.Models.Definitions["test-model"] = modelCfg

	runner := cliRunner{
		runtime: cliRuntime{
			cfg:      cfg,
			provider: providerStub,
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			events:   output.NoopSink{},
		},
		maxTurns: 1,
	}

	if _, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "hello"}}, nil, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	ep := providerStub.requests[0].ExtraParams
	if ep == nil {
		t.Fatal("ExtraParams = nil, want non-nil map")
	}
	if got, want := ep["temperature"], 0.7; got != want {
		t.Fatalf("ExtraParams[temperature] = %v, want %v", got, want)
	}
	if got, want := ep["top_p"], 0.9; got != want {
		t.Fatalf("ExtraParams[top_p] = %v, want %v", got, want)
	}
}

type fakeProvider struct {
	requests  []provider.ChatRequest
	responses []provider.ChatResponse
}

func (p *fakeProvider) ChatCompletion(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if len(p.responses) == 0 {
		return provider.ChatResponse{}, fmt.Errorf("no response configured")
	}
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

func (p *fakeProvider) StreamChatCompletion(_ context.Context, _ provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, fmt.Errorf("stream not used")
}

func (p *fakeProvider) SupportsUsageStats() bool { return true }

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

type builtCLIHelperBinary struct {
	path string
	err  error
}

var buildCLIHelperBinaryOnce = sync.OnceValue(func() builtCLIHelperBinary {
	dir, err := os.MkdirTemp("", "steiner-cmd-helper")
	if err != nil {
		return builtCLIHelperBinary{err: fmt.Errorf("create helper binary dir: %w", err)}
	}
	cliHelperBinaryDir = dir

	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(cliHelperSource), 0o644); err != nil {
		return builtCLIHelperBinary{err: fmt.Errorf("write helper source: %w", err)}
	}
	bin := filepath.Join(dir, "helper")
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", bin, source)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return builtCLIHelperBinary{err: fmt.Errorf("build helper binary: %w: %s", err, strings.TrimSpace(string(output)))}
	}
	return builtCLIHelperBinary{path: bin}
})

var cliHelperBinaryDir string

func mustBuildCLIHelperBinary(t *testing.T) string {
	t.Helper()

	built := buildCLIHelperBinaryOnce()
	if built.err != nil {
		t.Fatalf("%v", built.err)
	}
	return built.path
}

const cliHelperSource = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "bash" {
		fmt.Fprint(os.Stdout, "{\"ok\":true,\"result\":{\"status\":\"ok\"}}")
		return
	}
	fmt.Fprint(os.Stdout, "{\"ok\":true,\"result\":null}")
}
`
