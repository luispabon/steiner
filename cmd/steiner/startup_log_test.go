package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

func TestStartupLoggerDisabledWithoutTerminal(t *testing.T) {
	var buf bytes.Buffer
	logger := newStartupLogger(&buf)
	if logger.enabled {
		t.Fatal("newStartupLogger() enabled = true for a bytes.Buffer, want false")
	}
	logger.step("Loading configuration", "project trusted (stored)")
	if buf.Len() != 0 {
		t.Fatalf("disabled logger wrote %q, want nothing", buf.String())
	}
}

func TestStartupLoggerStepRendersLine(t *testing.T) {
	tests := []struct {
		name     string
		label    string
		detail   string
		contains []string
	}{
		{
			name:     "with detail",
			label:    "Loading configuration",
			detail:   "project trusted (stored)",
			contains: []string{"\u2714", "Loading configuration", "project trusted (stored)"},
		},
		{
			name:     "without detail",
			label:    "Starting TUI",
			detail:   "",
			contains: []string{"\u2714", "Starting TUI"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := &startupLogger{w: &buf, enabled: true}
			logger.step(tt.label, tt.detail)
			out := buf.String()
			for _, want := range tt.contains {
				if !strings.Contains(out, want) {
					t.Errorf("output %q missing %q", out, want)
				}
			}
			if !strings.HasPrefix(out, "  ") {
				t.Errorf("output %q does not start with the two-space indent", out)
			}
			if !strings.HasSuffix(out, "\n") {
				t.Errorf("output %q does not end with a newline", out)
			}
			if tt.detail == "" && strings.Contains(out, " \n") {
				t.Errorf("output %q has a trailing space before the newline", out)
			}
		})
	}
}

func TestStartupLoggerNilStepIsNoop(t *testing.T) {
	var logger *startupLogger
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("step on nil logger panicked: %v", r)
			}
		}()
		logger.step("Runtime ready", displayVersion(version))
		logger.step("Starting TUI", "")
	}()
}

func TestStartupLogDetailHelpers(t *testing.T) {
	cfg := config.Config{
		MCP: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
			"one":   {Enabled: true},
			"two":   {Enabled: true},
			"three": {Enabled: false},
		}},
		LSP: config.LSPConfig{Servers: map[string]config.LSPServerConfig{
			"go":     {Enabled: true},
			"python": {Enabled: false},
		}},
	}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "mcp background", got: mcpStartupDetail(cfg, true), want: "2 server(s) (background)"},
		{name: "mcp blocking", got: mcpStartupDetail(cfg, false), want: "2 server(s)"},
		{name: "lsp", got: lspStartupDetail(cfg), want: "1 configured (start on first use)"},
		{name: "trust unresolved", got: trustSourceDetail(&cliFlags{}), want: ""},
		{name: "trust resolved", got: trustSourceDetail(&cliFlags{trustResolved: true, trustSource: "stored"}), want: "project trusted (stored)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("detail = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestOrchestratorModelRef(t *testing.T) {
	tests := []struct {
		name     string
		active   string
		fallback string
		want     string
	}{
		{name: "active set", active: "worker", fallback: "router", want: "worker"},
		{name: "whitespace active falls back", active: "  \t", fallback: "router", want: "router"},
		{name: "both empty", active: "", fallback: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{Models: config.ModelsConfig{Effective: config.EffectiveModelAssignments{
				ActiveOrchestratorModel: tt.active,
				DefaultModel:            tt.fallback,
			}}}
			if got := orchestratorModelRef(cfg); got != tt.want {
				t.Errorf("orchestratorModelRef() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStartupLoggerBuildRuntimeSteps(t *testing.T) {
	const baseConfig = `providers:
  openrouter:
    type: openrouter
    api_key: sk-test
models:
  profiles:
    default:
      default_model: router
  definitions:
    router:
      provider: openrouter
      id: anthropic/claude-sonnet-5-5
`
	tests := []struct {
		name    string
		config  string
		wantLSP bool
	}{
		{name: "defaults", config: baseConfig},
		{
			name: "lsp enabled",
			config: baseConfig + `lsp:
  enabled: true
  servers:
    go:
      enabled: true
      command: gopls
      file_extensions: [".go"]
      root_markers: ["go.mod", ".git"]
`,
			wantLSP: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectRoot := t.TempDir()
			configPath := filepath.Join(projectRoot, "config.yaml")
			writeFile(t, configPath, tt.config)
			cmd := &cobra.Command{}
			cmd.SetIn(strings.NewReader(""))
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			var buf bytes.Buffer
			flags := &cliFlags{
				configPath:         configPath,
				trustProjectConfig: true,
				exec:               true,
				startupLog:         &startupLogger{w: &buf, enabled: true},
			}

			rt, err := buildRuntimeWithRoots(context.Background(), cmd, flags, projectRoot, projectRoot, "")
			if err != nil {
				t.Fatalf("buildRuntimeWithRoots() error = %v", err)
			}
			defer closeRuntime(&rt)

			out := buf.String()
			for _, want := range []string{
				projectRoot,
				"project trusted (flag)",
				"router",
				"disabled",
				// MCP is enabled by default, with no servers configured here.
				"0 server(s)",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("startup log missing %q:\n%s", want, out)
				}
			}

			// Steps must appear in the order buildRuntimeWithRoots emits them.
			prev := -1
			for _, label := range []string{
				"Preparing project directory",
				"Loading configuration",
				"Checking model configuration",
				"Logging",
				"Discovering skills",
				"Sandbox",
				"Connecting MCP servers",
			} {
				idx := strings.Index(out, label)
				if idx < 0 {
					t.Fatalf("startup log missing %q:\n%s", label, out)
				}
				if idx <= prev {
					t.Errorf("startup log line %q at offset %d, want after the previous line at offset %d:\n%s", label, idx, prev, out)
				}
				prev = idx
			}

			// LSP is disabled by default, so it has no startup line.
			if !tt.wantLSP {
				if strings.Contains(out, "LSP servers") {
					t.Errorf("startup log has %q with LSP disabled:\n%s", "LSP servers", out)
				}
				return
			}
			if !strings.Contains(out, "LSP servers") {
				t.Errorf("startup log missing %q with LSP enabled:\n%s", "LSP servers", out)
			}
			if !strings.Contains(out, "1 configured") {
				t.Errorf("startup log missing LSP count with LSP enabled:\n%s", out)
			}
		})
	}
}

func TestRunInteractiveModeStartupSteps(t *testing.T) {
	oldBuildRuntime := buildRuntime
	oldRunTeaProgram := runTeaProgram
	oldQuitTeaProgram := quitTeaProgram
	oldNewStartupLogger := newStartupLoggerFn
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
		runTeaProgram = oldRunTeaProgram
		quitTeaProgram = oldQuitTeaProgram
		newStartupLoggerFn = oldNewStartupLogger
	})

	var buf bytes.Buffer
	newStartupLoggerFn = func(io.Writer) *startupLogger {
		return &startupLogger{w: &buf, enabled: true}
	}
	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
		return cliRuntime{
			cfg: config.Config{
				Models: config.ModelsConfig{
					Effective: config.EffectiveModelAssignments{
						DefaultModel:            "test",
						ActiveOrchestratorModel: "test",
					},
					Definitions: map[string]config.ModelConfig{"test": {Provider: "local", ID: "test-model"}},
				},
				Providers: map[string]config.ProviderConfig{"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"}},
			},
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			events:   output.NoopSink{},
		}, nil
	}
	runTeaProgram = func(*tea.Program) (tea.Model, error) {
		return nil, errors.New("boom")
	}
	quitTeaProgram = func(*tea.Program) {}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := runInteractiveMode(cmd, &cliFlags{}); err != nil {
		t.Fatalf("runInteractiveMode() error = %v, want nil", err)
	}

	out := buf.String()
	prev := -1
	for _, label := range []string{"Runtime ready", "Session created", "Starting TUI"} {
		idx := strings.Index(out, label)
		if idx < 0 {
			t.Fatalf("startup log missing %q:\n%s", label, out)
		}
		if idx <= prev {
			t.Errorf("startup log line %q at offset %d, want after the previous line at offset %d:\n%s", label, idx, prev, out)
		}
		prev = idx
	}
}
