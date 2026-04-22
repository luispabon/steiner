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

	writeFile(t, filepath.Join(globalDir, "config.yaml"), `provider:
  model: global-model
  parallelism: 2
limits:
  max_turns: 25
approval:
  default: auto
paths:
  project_root_only: false
`)
	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `provider:
  model: project-model
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
	cmd.SetArgs([]string{"--model", "cli-model", "--verbose", "config"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var got config.Config
	if err := yaml.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal config output: %v\noutput:\n%s", err, stdout.String())
	}
	if got.Provider.Model != "cli-model" {
		t.Fatalf("provider.model = %q, want cli-model", got.Provider.Model)
	}
	if got.Provider.Parallelism != 2 {
		t.Fatalf("provider.parallelism = %d, want 2", got.Provider.Parallelism)
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
	configPath := filepath.Join(tempDir, "broken.yaml")
	writeFile(t, configPath, `provider:
  type: unsupported
limits:
  max_turns: 0
`)

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--config", configPath, "config"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid config error")
	}
	if !strings.Contains(stderr.String(), "invalid config:") {
		t.Fatalf("stderr = %q, want invalid config error", stderr.String())
	}
	if !strings.Contains(stderr.String(), "provider.type") {
		t.Fatalf("stderr = %q, want provider.type validation failure", stderr.String())
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
				"read":   config.ApprovalModeAuto,
				"glob":   config.ApprovalModeAuto,
				"search": config.ApprovalModeAuto,
				"write":  config.ApprovalModePrompt,
				"edit":   config.ApprovalModePrompt,
				"bash":   config.ApprovalModePrompt,
			},
		},
		Tools: map[string]config.ToolConfig{},
	})
	if err != nil {
		t.Fatalf("runtimeRegistry() error = %v", err)
	}

	got := registry.Names()
	want := []string{"bash", "edit", "glob", "read", "search", "write"}
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
		return cliRuntime{
			cfg: config.Config{
				Provider: config.ProviderConfig{
					Model: "test-model",
				},
				Limits: config.LimitsConfig{
					MaxTurns:  4,
					MaxTokens: 64,
				},
				ProjectContext: config.ProjectContextConfig{
					MaxTokens: 128,
				},
			},
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
			events:      output.NewStream(&stderr),
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
	if got := stderr.String(); !strings.Contains(got, "status: run complete after 1 turn") {
		t.Fatalf("stderr = %q, want stop reason", got)
	}
}

func TestCLIRunnerReturnsCancelledDiagnosticsWithoutError(t *testing.T) {
	runner := cliRunner{
		runtime: cliRuntime{
			cfg: config.Config{
				Provider: config.ProviderConfig{
					Model: "test-model",
				},
				Limits: config.LimitsConfig{
					MaxTurns:  4,
					MaxTokens: 64,
				},
				ProjectContext: config.ProjectContextConfig{
					MaxTokens: 128,
				},
				Approval: config.ApprovalConfig{
					Default: config.ApprovalModeAuto,
				},
			},
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
		return cliRuntime{
			cfg: config.Config{
				Provider: config.ProviderConfig{
					Model: "test-model",
				},
				Limits: config.LimitsConfig{
					MaxTurns:  4,
					MaxTokens: 64,
				},
				ProjectContext: config.ProjectContextConfig{
					MaxTokens: 128,
				},
			},
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
			close:       fileSink.Close,
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
		return cliRuntime{
			cfg: config.Config{
				Provider: config.ProviderConfig{
					Model: "test-model",
				},
				Limits: config.LimitsConfig{
					MaxTurns:  4,
					MaxTokens: 64,
				},
				Approval: config.ApprovalConfig{
					Default: config.ApprovalModePrompt,
					Overrides: map[string]config.ApprovalMode{
						"bash": config.ApprovalModePrompt,
					},
				},
				Paths: config.PathsConfig{
					ProjectRootOnly: true,
				},
			},
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
			events:      output.NewStream(&stderr),
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

	got := stderr.String()
	wantCWD := filepath.Join(tempRepo, "subdir")
	if !strings.Contains(got, `approve tool=bash mode=prompt args={"command":"pwd","cwd":"`+wantCWD+`"} [y/N]`) {
		t.Fatalf("stderr = %q, want approval prompt with normalized args", got)
	}
	if !strings.Contains(stdout.String(), "final answer") {
		t.Fatalf("stdout = %q, want final answer", stdout.String())
	}
}

func TestExecModeReturnsExplicitErrorWhenApprovalInputIsUnavailable(t *testing.T) {
	helper := mustBuildCLIHelperBinary(t)
	tempRepo := t.TempDir()

	oldBuildRuntime := buildRuntime
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
	})

	buildRuntime = func(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
		_ = ctx
		_ = cmd
		_ = flags
		return cliRuntime{
			cfg: config.Config{
				Provider: config.ProviderConfig{
					Model: "test-model",
				},
				Limits: config.LimitsConfig{
					MaxTurns:  4,
					MaxTokens: 64,
				},
				Approval: config.ApprovalConfig{
					Default: config.ApprovalModePrompt,
					Overrides: map[string]config.ApprovalMode{
						"bash": config.ApprovalModePrompt,
					},
				},
				Paths: config.PathsConfig{
					ProjectRootOnly: true,
				},
			},
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
									},
								},
							},
						},
						FinishReason: "tool_calls",
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
	if err == nil {
		t.Fatal("Execute() error = nil, want approval input error")
	}
	if !strings.Contains(err.Error(), "approval input is unavailable") {
		t.Fatalf("Execute() error = %v, want approval input error", err)
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
			cfg: config.Config{
				Provider: config.ProviderConfig{
					Model:       "test-model",
					Temperature: 0.2,
				},
				Limits: config.LimitsConfig{
					MaxTurns:  4,
					MaxTokens: 64,
				},
				ProjectContext: config.ProjectContextConfig{
					MaxTokens: 128,
				},
			},
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

func TestInteractiveInputPrefersRawStdin(t *testing.T) {
	raw := strings.NewReader("raw")
	shared := bufio.NewReader(strings.NewReader("shared"))

	if got := interactiveInput(cliRuntime{stdin: raw, sharedInput: shared}); got != raw {
		t.Fatalf("interactiveInput() = %#v, want raw stdin reader", got)
	}
	if got := interactiveInput(cliRuntime{sharedInput: shared}); got != shared {
		t.Fatalf("interactiveInput() fallback = %#v, want shared input reader", got)
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
			cfg: config.Config{
				Provider: config.ProviderConfig{
					Model:       "test-model",
					Temperature: 0.2,
				},
				Limits: config.LimitsConfig{
					MaxTurns:  6,
					MaxTokens: 100,
				},
				ProjectContext: config.ProjectContextConfig{
					MaxTokens: 64,
				},
				Approval: config.ApprovalConfig{
					Default: config.ApprovalModeAuto,
					Overrides: map[string]config.ApprovalMode{
						"bash": config.ApprovalModeAuto,
					},
				},
			},
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
	if !containsString(kinds, "compaction") {
		t.Fatalf("diagnostic kinds = %v, want compaction event", kinds)
	}
	if !foundStopReason {
		t.Fatalf("result diagnostics = %#v, want stop reason event", result.Diagnostics)
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
