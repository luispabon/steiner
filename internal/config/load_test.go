package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestApplyMCPDefaultsConnectTimeout(t *testing.T) {
	if defaultMCPConnectTimeout != 15*time.Second {
		t.Fatalf("defaultMCPConnectTimeout = %v, want 15s", defaultMCPConnectTimeout)
	}

	tests := []struct {
		name    string
		timeout Duration
		want    Duration
	}{
		{name: "absent defaults to 15s", want: MustDuration("15s")},
		{name: "explicit zero defaults to 15s", timeout: MustDuration("0s"), want: MustDuration("15s")},
		{name: "explicit value passes through", timeout: MustDuration("30s"), want: MustDuration("30s")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Command: "npx", ConnectTimeout: tt.timeout},
				},
			}
			applyMCPDefaults(&cfg)
			if got := cfg.Servers["example"].ConnectTimeout; got != tt.want {
				t.Errorf("connect_timeout = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMCPConnectTimeoutPatch(t *testing.T) {
	patch, err := parseConfigPatch(`mcp:
  servers:
    example:
      transport: stdio
      command: npx
      connect_timeout: 30s
`)
	if err != nil {
		t.Fatalf("parseConfigPatch() error = %v", err)
	}
	if patch.MCP == nil || patch.MCP.Servers == nil {
		t.Fatal("patch.MCP.Servers = nil, want parsed mcp patch")
	}
	srv := (*patch.MCP.Servers)["example"]
	if srv.ConnectTimeout == nil {
		t.Fatal("srv.ConnectTimeout = nil, want parsed connect_timeout")
	}
	if got, want := *srv.ConnectTimeout, MustDuration("30s"); got != want {
		t.Fatalf("connect_timeout = %v, want %v", got, want)
	}

	var dst MCPServerConfig
	applyMCPServerPatch(&dst, &srv)
	if got, want := dst.ConnectTimeout, MustDuration("30s"); got != want {
		t.Fatalf("applied connect_timeout = %v, want %v", got, want)
	}
}

func TestMCPFilterAndSubAgentsYAMLParsing(t *testing.T) {
	patch, err := parseConfigPatch(`mcp:
  servers:
    example:
      transport: stdio
      command: npx
      allowed_tools:
        - echo
        - read_file
      blocked_tools:
        - dangerous_tool
      sub_agents:
        - explore
        - code
`)
	if err != nil {
		t.Fatalf("parseConfigPatch() error = %v", err)
	}
	if patch.MCP == nil || patch.MCP.Servers == nil {
		t.Fatal("patch.MCP.Servers = nil, want parsed mcp patch")
	}
	srv := (*patch.MCP.Servers)["example"]
	if srv.AllowedTools == nil {
		t.Fatal("srv.AllowedTools = nil, want parsed allowed_tools")
	}
	if srv.BlockedTools == nil {
		t.Fatal("srv.BlockedTools = nil, want parsed blocked_tools")
	}
	if srv.SubAgents == nil {
		t.Fatal("srv.SubAgents = nil, want parsed sub_agents")
	}
	if got, want := *srv.AllowedTools, []string{"echo", "read_file"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("allowed_tools = %#v, want %#v", got, want)
	}
	if got, want := *srv.BlockedTools, []string{"dangerous_tool"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("blocked_tools = %#v, want %#v", got, want)
	}
	if got, want := *srv.SubAgents, []string{"explore", "code"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sub_agents = %#v, want %#v", got, want)
	}

	var dst MCPServerConfig
	applyMCPServerPatch(&dst, &srv)
	if got, want := dst.AllowedTools, []string{"echo", "read_file"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("applied allowed_tools = %#v, want %#v", got, want)
	}
	if got, want := dst.BlockedTools, []string{"dangerous_tool"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("applied blocked_tools = %#v, want %#v", got, want)
	}
	if got, want := dst.SubAgents, []string{"explore", "code"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("applied sub_agents = %#v, want %#v", got, want)
	}
}

func TestMCPFilterAndSubAgentsYAMLParsingEmpty(t *testing.T) {
	patch, err := parseConfigPatch(`mcp:
  servers:
    example:
      transport: stdio
      command: npx
      allowed_tools: []
      blocked_tools: []
      sub_agents: []
`)
	if err != nil {
		t.Fatalf("parseConfigPatch() error = %v", err)
	}
	srv := (*patch.MCP.Servers)["example"]
	if srv.AllowedTools == nil || len(*srv.AllowedTools) != 0 {
		t.Fatalf("srv.AllowedTools = %#v, want explicit empty slice", srv.AllowedTools)
	}
	if srv.BlockedTools == nil || len(*srv.BlockedTools) != 0 {
		t.Fatalf("srv.BlockedTools = %#v, want explicit empty slice", srv.BlockedTools)
	}
	if srv.SubAgents == nil || len(*srv.SubAgents) != 0 {
		t.Fatalf("srv.SubAgents = %#v, want explicit empty slice", srv.SubAgents)
	}
}

func TestCodexTransportYAMLParsing(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want CodexTransport
	}{
		{
			name: "parses http transport",
			yaml: `providers:
  codex:
    type: codex
    codex:
      transport: http`,
			want: CodexTransportHTTP,
		},
		{
			name: "parses websocket transport",
			yaml: `providers:
  codex:
    type: codex
    codex:
      transport: websocket`,
			want: CodexTransportWebSocket,
		},
		{
			name: "unset transport leaves initial value untouched",
			yaml: `providers:
  codex:
    type: codex`,
			want: CodexTransportHTTP,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := parseConfigPatch(tt.yaml)
			if err != nil {
				t.Fatalf("parseConfigPatch() error = %v", err)
			}
			if patch.Providers == nil {
				t.Fatal("patch.Providers = nil, want parsed providers")
			}
			providers := *patch.Providers
			if _, ok := providers["codex"]; !ok {
				t.Fatal("codex provider not found in patch")
			}
			codexProvider := providers["codex"]

			// Create initial config with default values
			dst := CodexConfig{Transport: CodexTransportHTTP}

			// Apply patch
			if codexProvider.Codex != nil {
				applyCodexPatch(&dst, codexProvider.Codex)
			}

			if got := dst.Transport; got != tt.want {
				t.Fatalf("Transport = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadResolvesDefaultProfile(t *testing.T) {
	cfg := loadProfileTestConfig(t, `models:
  definitions:
    default:
      provider: local
      id: base
    fast:
      provider: local
      id: fast
  profiles:
    default:
      default_model: default
      sub_agents:
        code: default
`)

	if got, want := cfg.Models.Effective.ProfileName, "default"; got != want {
		t.Fatalf("effective profile = %q, want %q", got, want)
	}
	if got, want := cfg.Models.Effective.DefaultModel, "default"; got != want {
		t.Fatalf("effective default model = %q, want %q", got, want)
	}
	if got, want := cfg.Models.Effective.ActiveOrchestratorModel, "default"; got != want {
		t.Fatalf("active orchestrator model = %q, want %q", got, want)
	}
}

func TestLoadResolvesNamedProfileAndCopiesAssignments(t *testing.T) {
	cfg := loadProfileTestConfig(t, `models:
  definitions:
    default:
      provider: local
      id: base
    fast:
      provider: local
      id: fast
  profiles:
    default:
      default_model: default
      advisor: default
      sub_agents:
        code: default
        explore: default
      oneshot:
        plan: default
      workflow_handoff:
        review: default
    fast:
      default_model: fast
      sub_agents:
        code: fast
`, CLIOverrides{Profile: "fast"})

	effective := cfg.Models.Effective
	if effective.ProfileName != "fast" || effective.DefaultModel != "fast" || effective.ActiveOrchestratorModel != "fast" {
		t.Fatalf("effective assignments = %#v", effective)
	}
	if effective.Advisor != "default" || effective.SubAgents["code"] != "fast" || effective.SubAgents["explore"] != "default" {
		t.Fatalf("effective role assignments = %#v", effective)
	}
	if effective.OneShot["plan"] != "default" || effective.WorkflowHandoff["review"] != "default" {
		t.Fatalf("effective action assignments = %#v", effective)
	}

	effective.SubAgents["code"] = "changed"
	effective.OneShot["plan"] = "changed"
	effective.WorkflowHandoff["review"] = "changed"
	profile := cfg.Models.Profiles["fast"]
	if profile.DefaultModel != "fast" || profile.SubAgents["code"] != "fast" {
		t.Fatalf("canonical selected profile changed: %#v", profile)
	}
	if profile.OneShot != nil || profile.WorkflowHandoff != nil {
		t.Fatalf("canonical inherited assignments changed: %#v", profile)
	}
	baseline := cfg.Models.Profiles["default"]
	if baseline.OneShot["plan"] != "default" || baseline.WorkflowHandoff["review"] != "default" {
		t.Fatalf("canonical baseline assignments changed: %#v", baseline)
	}
}

func TestLoadModelOverridesApplyAfterProfileResolution(t *testing.T) {
	tests := []struct {
		name      string
		cliModel  string
		wantModel string
	}{
		{name: "environment", wantModel: "env"},
		{name: "cli wins", cliModel: "cli", wantModel: "cli"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadProfileTestConfig(t, `models:
  definitions:
    default:
      provider: local
      id: base
    fast:
      provider: local
      id: fast
    env:
      provider: local
      id: env
    cli:
      provider: local
      id: cli
  profiles:
    default:
      default_model: default
      sub_agents:
        code: default
    fast:
      default_model: fast
      sub_agents:
        code: fast
`, CLIOverrides{Profile: "fast", Model: tt.cliModel}, map[string]string{"STEINER_MODEL": "env"})

			if got := cfg.Models.Effective.ActiveOrchestratorModel; got != tt.wantModel {
				t.Fatalf("active orchestrator model = %q, want %q", got, tt.wantModel)
			}
			if got := cfg.Models.Effective.DefaultModel; got != "fast" {
				t.Fatalf("effective default model = %q, want profile model fast", got)
			}
			if got := cfg.Models.Effective.SubAgents["code"]; got != "fast" {
				t.Fatalf("effective code model = %q, want profile model fast", got)
			}
		})
	}
}

func TestLoadRejectsUnknownProfile(t *testing.T) {
	_, err := loadProfileTestConfigResult(t, `models:
  definitions:
    default:
      provider: local
      id: base
  profiles:
    default:
      default_model: default
`, CLIOverrides{Profile: "missing"}, nil)
	if err == nil || !strings.Contains(err.Error(), "profile is not defined") {
		t.Fatalf("Load() error = %v, want unknown profile error", err)
	}
}

func loadProfileTestConfig(t *testing.T, contents string, args ...any) Config {
	t.Helper()
	var cli CLIOverrides
	var env map[string]string
	if len(args) > 0 {
		cli = args[0].(CLIOverrides)
	}
	if len(args) > 1 {
		env = args[1].(map[string]string)
	} else {
		env = map[string]string{}
	}
	cfg, err := loadProfileTestConfigResult(t, contents, cli, env)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return cfg
}

func loadProfileTestConfigResult(t *testing.T, contents string, cli CLIOverrides, env map[string]string) (Config, error) {
	t.Helper()
	if env == nil {
		env = map[string]string{}
	}
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	cli.ConfigPath = configPath
	return Load(LoadOptions{
		HomeDir:    filepath.Join(tempDir, "home"),
		WorkingDir: tempDir,
		Env:        env,
		CLI:        cli,
	})
}
