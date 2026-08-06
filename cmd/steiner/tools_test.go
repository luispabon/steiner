package main

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/mcp"
)

// buildMCPFixture compiles the internal/mcp fixture server once per test.
func buildMCPFixture(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fixtureserver")
	cmd := exec.Command("go", "build", "-o", bin, "../../internal/mcp/testdata/fixtureserver") //nolint:noctx
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixtureserver: %v\n%s", err, out)
	}
	return bin
}

// mcpFixtureManager connects the fixture server and returns the manager.
func mcpFixtureManager(t *testing.T) *mcp.Manager {
	t.Helper()
	mgr := mcp.Connect(context.Background(), config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"fixture": {Enabled: true, Command: buildMCPFixture(t)},
		},
	}, nil, nil, func(string) {}, func(string) {}, io.Discard, false)
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

// registryTestConfig returns a minimal config with the default tool timeout set.
func registryTestConfig() config.Config {
	return config.Config{
		Limits: config.LimitsConfig{ToolTimeoutDefault: config.MustDuration("30s")},
		Tools:  map[string]config.ToolConfig{},
	}
}

func TestRuntimeRegistryWithNilManagerRegistersNoMCPTools(t *testing.T) {
	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, nil, nil)
	for _, name := range registry.Names() {
		if strings.HasPrefix(name, "mcp__") {
			t.Fatalf("nil manager produced MCP tool %q", name)
		}
	}
}

func TestRuntimeRegistryRegistersMCPToolsAlongsideBuiltins(t *testing.T) {
	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, nil, mcpFixtureManager(t))

	for _, want := range []string{"bash", "read", "mcp__fixture__echo", "mcp__fixture__boom"} {
		if _, ok := registry.Get(want); !ok {
			t.Fatalf("registry missing %q; names: %v", want, registry.Names())
		}
	}
}

func TestSubAgentSubsetExcludesMCPTools(t *testing.T) {
	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, nil, mcpFixtureManager(t))

	// Registry.Subset is include-list based, so MCP tools never appear in a
	// sub-agent allowlist. Ticket #6 handles deliberate exposure.
	subset := registry.Subset(delegation.AgentAllowedTools(delegation.AgentTypeCode), nil)
	want := []string{"bash", "glob", "grep", "ls", "mutate", "read"}
	if got := subset.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sub-agent subset = %v, want %v", got, want)
	}
}
