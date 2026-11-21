package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
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
		name      string
		detail    string
		contains  []string
		trailings bool
	}{
		{
			name:     "with detail",
			detail:   "project trusted (stored)",
			contains: []string{"\u2714", "Loading configuration", "project trusted (stored)"},
		},
		{
			name:     "without detail",
			detail:   "",
			contains: []string{"\u2714", "Starting TUI"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := &startupLogger{w: &buf, enabled: true}
			label := "Loading configuration"
			if tt.detail == "" {
				label = "Starting TUI"
			}
			logger.step(label, tt.detail)
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
	logger.step("Runtime ready", displayVersion(version))
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

func TestStartupLoggerBuildRuntimeSteps(t *testing.T) {
	projectRoot := t.TempDir()
	configPath := filepath.Join(projectRoot, "config.yaml")
	writeFile(t, configPath, `providers:
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
`)
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
		"Preparing project directory",
		projectRoot,
		"Loading configuration",
		"project trusted (flag)",
		"Checking model configuration",
		"router",
		"Logging",
		"disabled",
		"Discovering skills",
		"Sandbox",
		// MCP is enabled by default, with no servers configured here.
		"Connecting MCP servers",
		"0 server(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("startup log missing %q:\n%s", want, out)
		}
	}
	// LSP is disabled by default, so it has no startup line.
	if strings.Contains(out, "LSP servers") {
		t.Errorf("startup log has %q with LSP disabled:\n%s", "LSP servers", out)
	}
}
