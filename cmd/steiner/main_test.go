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
	"testing"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"gopkg.in/yaml.v3"
)

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
	if !strings.HasPrefix(got, "steiner ") {
		t.Fatalf("version output = %q, want version prefix", got)
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

	writeFile(t, filepath.Join(globalDir, "config.yaml"), `scheduler:
  parallelism: 2
model: global
models:
  global:
    type: openai_compat
    base_url: http://global.example/v1
    model: global-backend
    max_completion_tokens: 2048
    context_size: 8192
    compaction:
      safety_margin_tokens: 256
      summary_max_tokens: 128
limits:
  max_turns: 25
approval:
  default: auto
paths:
  project_root_only: false
`)
	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model: project
models:
  project:
    type: openai_compat
    base_url: http://project.example/v1
    model: project-backend
    max_completion_tokens: 4096
    context_size: 32768
    compaction:
      safety_margin_tokens: 1024
      summary_max_tokens: 512
  cli:
    type: openai_compat
    base_url: http://cli.example/v1
    model: cli-backend
    max_completion_tokens: 8192
    context_size: 65536
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
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
	if got.Model != "cli" {
		t.Fatalf("model = %q, want cli", got.Model)
	}
	if got.Scheduler.Parallelism != 2 {
		t.Fatalf("scheduler.parallelism = %d, want 2", got.Scheduler.Parallelism)
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
	if got.Models["cli"].Model != "cli-backend" {
		t.Fatalf("models[cli].model = %q, want cli-backend", got.Models["cli"].Model)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
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

func TestDefaultBuildRuntimeResolvesSelectedModelAndScheduler(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `scheduler:
  parallelism: 7
model: slow
models:
  fast:
    type: openai_compat
    base_url: http://fast.example/v1
    model: fast-backend
    max_completion_tokens: 256
    context_size: 4096
    compaction:
      safety_margin_tokens: 128
      summary_max_tokens: 64
  slow:
    type: openai_compat
    base_url: http://slow.example/v1
    model: slow-backend
    max_completion_tokens: 512
    context_size: 8192
    compaction:
      safety_margin_tokens: 256
      summary_max_tokens: 128
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

	oldNewScheduler := newScheduler
	oldNewOpenAICompat := newOpenAICompat
	t.Cleanup(func() {
		newScheduler = oldNewScheduler
		newOpenAICompat = oldNewOpenAICompat
	})

	var gotParallelism int
	var gotProviderConfig provider.OpenAICompatConfig
	newScheduler = func(parallelism int) (*provider.Scheduler, error) {
		gotParallelism = parallelism
		return provider.NewScheduler(parallelism)
	}
	newOpenAICompat = func(cfg provider.OpenAICompatConfig) (provider.Provider, error) {
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
	if gotParallelism != 7 {
		t.Fatalf("scheduler parallelism = %d, want 7", gotParallelism)
	}
	builtProvider, err := rt.providerFactory(rt.cfg.Model)
	if err != nil {
		t.Fatalf("providerFactory() error = %v", err)
	}
	if builtProvider == nil {
		t.Fatal("providerFactory() = nil, want provider")
	}
	if gotProviderConfig.BaseURL != "http://slow.example/v1" {
		t.Fatalf("provider base_url = %q, want slow alias base_url", gotProviderConfig.BaseURL)
	}
	if gotProviderConfig.Model != "slow-backend" {
		t.Fatalf("provider model = %q, want slow-backend", gotProviderConfig.Model)
	}
	if gotProviderConfig.Scheduler == nil {
		t.Fatal("provider scheduler = nil, want scheduler")
	}
	if gotProviderConfig.HTTPClient == nil {
		t.Fatal("provider HTTP client = nil, want client")
	}
}

func TestRuntimeRegistryIncludesCoreToolsByDefault(t *testing.T) {
	registry, err := runtimeRegistry(config.Config{
		Limits: config.LimitsConfig{
			ToolTimeoutDefault: config.MustDuration("30s"),
		},
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModePrompt,
			Overrides: map[string]config.ApprovalMode{
				"read":  config.ApprovalModeAuto,
				"glob":  config.ApprovalModeAuto,
				"grep":  config.ApprovalModeAuto,
				"ls":    config.ApprovalModeAuto,
				"write": config.ApprovalModePrompt,
				"edit":  config.ApprovalModePrompt,
				"bash":  config.ApprovalModePrompt,
			},
		},
		Tools: map[string]config.ToolConfig{},
	})
	if err != nil {
		t.Fatalf("runtimeRegistry() error = %v", err)
	}

	got := registry.Names()
	want := []string{"bash", "edit", "glob", "grep", "ls", "read", "write"}
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
	buildRuntime = func(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
		_ = ctx
		_ = flags
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

	result, err := runner.Run(ctx, []agent.Message{{Role: agent.MessageRoleUser, Content: "fix the bug"}}, nil)
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

func TestExecModeWritesFullLogFile(t *testing.T) {
	oldBuildRuntime := buildRuntime
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
	})

	logPath := filepath.Join(t.TempDir(), "session.log")
	buildRuntime = func(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
		_ = ctx
		fileSink, err := output.NewFileLogSink(flags.logFile)
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

func TestExecModePrintsApprovalPromptWithPreviewArgs(t *testing.T) {
	helper := mustBuildCLIHelperBinary(t)
	tempRepo := t.TempDir()
	mustMkdirAll(t, filepath.Join(tempRepo, "subdir"))

	oldBuildRuntime := buildRuntime
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
	})

	var stdout, stderr bytes.Buffer
	buildRuntime = func(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
		_ = ctx
		_ = cmd
		_ = flags
		cfg := testRuntimeConfig("test-model")
		cfg.Limits.MaxTurns = 4
		cfg.Limits.MaxTokens = 64
		cfg.Approval = config.ApprovalConfig{
			Default: config.ApprovalModePrompt,
			Overrides: map[string]config.ApprovalMode{
				"bash": config.ApprovalModePrompt,
			},
		}
		cfg.Paths = config.PathsConfig{
			ProjectRootOnly: true,
		}
		return cliRuntime{
			cfg: cfg,
			provider: &fakeProvider{
				responses: []provider.ChatResponse{
					{
						Message: provider.Message{
							Role: provider.MessageRoleAssistant,
							ToolCalls: []provider.ToolCall{
								{
									ID:   "call_1",
									Name: "bash",
									Arguments: map[string]any{
										"command": "pwd",
										"cwd":     "subdir",
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
					{
						Message: provider.Message{
							Role:    provider.MessageRoleAssistant,
							Content: "final answer",
						},
						FinishReason: "stop",
					},
				},
			},
			registry: tool.NewRegistry(tool.ToolDef{
				Name:       "bash",
				ExecPath:   helper,
				Subcommand: "bash",
			}),
			workDir:     tempRepo,
			homeDir:     t.TempDir(),
			human:       output.NewStream(&stdout),
			status:      output.NewStream(&stderr),
			events:      output.NewStream(&stdout),
			sharedInput: bufio.NewReader(strings.NewReader("y\n")),
		}, nil
	}

	cmd := newRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--exec", "run bash"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	wantCWD := filepath.Join(tempRepo, "subdir")
	if !strings.Contains(got, `approval: turn=0 requested tool=bash mode=prompt args={"command":"pwd","cwd":"`+wantCWD+`"}`) {
		t.Fatalf("stdout = %q, want approval prompt with normalized args", got)
	}
	if !strings.Contains(got, `approval: turn=0 accepted tool=bash mode=prompt args={"command":"pwd","cwd":"`+wantCWD+`"} message=approved`) {
		t.Fatalf("stdout = %q, want approval acceptance with normalized args", got)
	}
	if !strings.Contains(got, "status: stopped at turn 1: reached the max token limit") {
		t.Fatalf("stdout = %q, want explicit max token stop after approval flow", got)
	}
}

func TestExecModeToolApprovalUnavailableCommunicatedToModel(t *testing.T) {
	helper := mustBuildCLIHelperBinary(t)
	tempRepo := t.TempDir()

	oldBuildRuntime := buildRuntime
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
	})

	var prov *fakeProvider
	buildRuntime = func(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
		_ = ctx
		_ = cmd
		_ = flags
		cfg := testRuntimeConfig("test-model")
		cfg.Limits.MaxTurns = 4
		cfg.Limits.MaxTokens = 0
		cfg.Approval = config.ApprovalConfig{
			Default: config.ApprovalModePrompt,
			Overrides: map[string]config.ApprovalMode{
				"bash": config.ApprovalModePrompt,
			},
		}
		cfg.Paths = config.PathsConfig{
			ProjectRootOnly: true,
		}
		prov = &fakeProvider{
			responses: []provider.ChatResponse{
				{
					Message: provider.Message{
						Role: provider.MessageRoleAssistant,
						ToolCalls: []provider.ToolCall{
							{
								ID:   "call_1",
								Name: "bash",
								Arguments: map[string]any{
									"command": "pwd",
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
				{
					Message: provider.Message{
						Role:    provider.MessageRoleAssistant,
						Content: "I cannot execute bash because approval is unavailable.",
					},
					FinishReason: "stop",
				},
			},
		}
		return cliRuntime{
			cfg:      cfg,
			provider: prov,
			registry: tool.NewRegistry(tool.ToolDef{
				Name:       "bash",
				ExecPath:   helper,
				Subcommand: "bash",
			}),
			workDir:     tempRepo,
			homeDir:     t.TempDir(),
			human:       output.NewStream(io.Discard),
			status:      output.NewStream(io.Discard),
			events:      output.NoopSink{},
			sharedInput: bufio.NewReader(strings.NewReader("")),
			approvalIn:  bufio.NewReader(strings.NewReader("")),
		}, nil
	}

	cmd := newRootCommand()
	cmd.SetArgs([]string{"--exec", "run bash"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil (approval error communicated to model, not fatal)", err)
	}

	if len(prov.requests) < 2 {
		t.Fatalf("expected at least 2 model calls, got %d — tool error must be sent back to model", len(prov.requests))
	}
	var toolResultContent string
	for _, msg := range prov.requests[1].Messages {
		if msg.Role == provider.MessageRoleTool {
			toolResultContent = msg.Content
			break
		}
	}
	if !strings.Contains(toolResultContent, "approval input is unavailable") {
		t.Fatalf("second request tool message = %q, want content containing approval error", toolResultContent)
	}
}

func TestExecModeMaxTurnsFlagOverridesConfig(t *testing.T) {
	oldBuildRuntime := buildRuntime
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
	})

	var stdout, stderr bytes.Buffer
	buildRuntime = func(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
		_ = ctx
		_ = cmd
		_ = flags
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
				Handler: func(ctx context.Context, input map[string]any) (any, error) {
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

	_, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "list files"}}, nil)
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
	return config.Config{
		Scheduler: config.SchedulerConfig{
			Parallelism: 1,
		},
		Model: alias,
		Models: map[string]config.ModelConfig{
			alias: {
				Type:                "openai_compat",
				BaseURL:             "http://localhost:11434/v1",
				APIKey:              "",
				Model:               alias,
				MaxCompletionTokens: 64,
				ContextSize:         4096,
				Compaction: config.CompactionConfig{
					SafetyMarginTokens: 16,
					SummaryMaxTokens:   32,
				},
			},
		},
		Limits: config.LimitsConfig{
			MaxTurns:           4,
			MaxTokens:          64,
			ToolTimeoutDefault: config.MustDuration("30s"),
		},
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModeAuto,
		},
		ProjectContext: config.ProjectContextConfig{
			MaxTokens: 128,
		},
	}
}

func TestInteractiveSkillsSnapshotTracksEnabledSubset(t *testing.T) {
	skills := newInteractiveSkills([]string{"review", "debug", "test"})
	if got, want := skills.Snapshot(), []string{"review", "debug", "test"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("initial Snapshot() = %v, want %v", got, want)
	}

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
				cfg.ProjectContext.MaxTokens = 64
				cfg.Approval.Overrides = map[string]config.ApprovalMode{
					"bash": config.ApprovalModeAuto,
				}
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

	result, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "run bash"}}, nil)
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
			payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
			if !ok {
				t.Fatalf("diagnostic payload type = %T, want output.ContextDiagnosticsEvent", event.Payload)
			}
			kinds = append(kinds, payload.Kind)
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

	cfg := testRuntimeConfig("test-model")
	cfg.Models["test-model"] = config.ModelConfig{
		Type:                "openai_compat",
		BaseURL:             "http://localhost:11434/v1",
		Model:               "test-model",
		MaxCompletionTokens: 64,
		ContextSize:         1,
		Compaction: config.CompactionConfig{
			SafetyMarginTokens: 16,
			SummaryMaxTokens:   8,
		},
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
	}

	_, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "fix the bug"}}, nil)
	if err == nil {
		t.Fatal("Run() error = nil, want context window failure")
	}
	if !strings.Contains(err.Error(), "request exceeds context window") {
		t.Fatalf("Run() error = %v, want context window failure", err)
	}
	if got, want := len(providerStub.requests), 0; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
}

func TestRequestSnapshotStoreStartsEmpty(t *testing.T) {
	store := &requestSnapshotStore{}
	if _, ok := store.Snapshot(); ok {
		t.Fatal("Snapshot() ok = true, want false")
	}
}

func TestCLIRunnerUpdatesSnapshotBudgetWhenModelChanges(t *testing.T) {
	store := &requestSnapshotStore{}
	sink := output.SinkFunc(func(event output.Event) {
		payload, ok := event.Payload.(output.APIRequestEvent)
		if !ok {
			return
		}
		store.Store(output.RequestContextSnapshot{
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

	cfg := testRuntimeConfig("small")
	cfg.Models = map[string]config.ModelConfig{
		"small": {
			Type:                "openai_compat",
			BaseURL:             "http://localhost:11434/v1",
			Model:               "gpt-4o-mini",
			MaxCompletionTokens: 32,
			ContextSize:         1024,
			Compaction: config.CompactionConfig{
				SafetyMarginTokens: 8,
				SummaryMaxTokens:   16,
			},
		},
		"large": {
			Type:                "openai_compat",
			BaseURL:             "http://localhost:11434/v1",
			Model:               "gpt-4o",
			MaxCompletionTokens: 96,
			ContextSize:         8192,
			Compaction: config.CompactionConfig{
				SafetyMarginTokens: 24,
				SummaryMaxTokens:   48,
			},
		},
	}

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

	if _, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "first"}}, nil); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	first, ok := store.Snapshot()
	if !ok {
		t.Fatal("first Snapshot() ok = false, want true")
	}
	if got, want := first.ModelBudget.ContextSize, 1024; got != want {
		t.Fatalf("first context size = %d, want %d", got, want)
	}
	if got, want := first.ModelBudget.MaxCompletionTokens, 32; got != want {
		t.Fatalf("first max completion tokens = %d, want %d", got, want)
	}

	runner.runtime.cfg.Model = "large"
	if _, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "second"}}, nil); err != nil {
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

func TestCLIRunnerPropagatesExtraParamsToProvider(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "done"},
				FinishReason: "stop",
			},
		},
	}

	cfg := testRuntimeConfig("test-model")
	cfg.Models["test-model"] = config.ModelConfig{
		Type:                "openai_compat",
		BaseURL:             "http://localhost:11434/v1",
		Model:               "test-model",
		ExtraParams:         map[string]any{"temperature": 0.7, "top_p": 0.9},
		MaxCompletionTokens: 64,
		ContextSize:         4096,
		Compaction: config.CompactionConfig{
			SafetyMarginTokens: 16,
			SummaryMaxTokens:   32,
		},
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
		maxTurns: 1,
	}

	if _, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "hello"}}, nil); err != nil {
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

func (p *fakeProvider) ChatCompletion(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if len(p.responses) == 0 {
		return provider.ChatResponse{}, fmt.Errorf("no response configured")
	}
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

func (p *fakeProvider) StreamChatCompletion(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
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

func mustBuildCLIHelperBinary(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(cliHelperSource), 0o644); err != nil {
		t.Fatalf("write helper source: %v", err)
	}
	bin := filepath.Join(dir, "helper")
	cmd := exec.Command("go", "build", "-o", bin, source)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build helper binary: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return bin
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
