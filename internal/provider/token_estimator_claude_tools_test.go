package provider_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

const (
	claudeFixtureToolsPath    = "testdata/claude_token_estimation/tools.json"
	claudeFixtureManifestPath = "testdata/claude_token_estimation/manifest.json"
)

// claudeFixtureToolsScope describes the tools.json contract for bashCap.
func claudeFixtureToolsScope(bashCap time.Duration) string {
	return fmt.Sprintf("core builtins from builtin.Builtins(builtin.Env{BashTimeoutCap: %ds}) via tool.Registry.ToProviderSpecs, sorted by name; excludes advisor, lsp_*, mcp__*, config-defined, and sub-agent tools; core defaults only", int64(bashCap/time.Second))
}

// claudeFixtureDefaultBashCap returns limits.tool_timeouts.bash from the default
// config. Load runs with an empty env and temp dirs so no user or project
// config changes the defaults.
func claudeFixtureDefaultBashCap(t *testing.T) time.Duration {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{
		Env:        map[string]string{},
		HomeDir:    t.TempDir(),
		WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	return time.Duration(cfg.Limits.ToolTimeouts["bash"].Duration())
}

// claudeFixtureCoreToolSpecs returns the production core builtin tool specs
// for bashCap.
func claudeFixtureCoreToolSpecs(bashCap time.Duration) []provider.ToolSpec {
	env := builtin.Env{BashTimeoutCap: bashCap}
	return tool.NewRegistry(builtin.Builtins(env)...).ToProviderSpecs()
}

// claudeFixtureCanonicalTools encodes specs as indented JSON with a trailing newline.
func claudeFixtureCanonicalTools(specs []provider.ToolSpec) ([]byte, error) {
	data, err := json.MarshalIndent(specs, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode claude fixture tools: %w", err)
	}
	return append(data, '\n'), nil
}

// The tools.json fixture is frozen historical evidence of real Claude requests.
// Live production schemas evolve independently, so this test checks only
// current core names and deterministic serialization, not fixture equality.
func TestClaudeTokenProductionCoreBuiltinsAreDeterministic(t *testing.T) {
	bashCap := claudeFixtureDefaultBashCap(t)
	specs := claudeFixtureCoreToolSpecs(bashCap)
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Function.Name)
	}
	want := []string{"bash", "display_file", "fetch_url", "glob", "grep", "ls", "mutate", "read", "workflow_handoff"}
	if !slices.Equal(names, want) {
		t.Fatalf("production core tool names = %v, want %v", names, want)
	}
	got, err := claudeFixtureCanonicalTools(specs)
	if err != nil {
		t.Fatal(err)
	}
	again, err := claudeFixtureCanonicalTools(claudeFixtureCoreToolSpecs(bashCap))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, again) {
		t.Fatal("canonical tool JSON differs between two builds of the same builtins")
	}
}

func TestClaudeTokenToolsManifestPinsFixtureHashAndScope(t *testing.T) {
	raw, err := os.ReadFile(claudeFixtureManifestPath)
	if err != nil {
		t.Fatalf("read claude fixture manifest: %v", err)
	}
	var manifest struct {
		Tools struct {
			Path                  string `json:"path"`
			Bytes                 int    `json:"bytes"`
			SHA256                string `json:"sha256"`
			Scope                 string `json:"scope"`
			BashTimeoutCapSeconds int    `json:"bash_timeout_cap_seconds"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse claude fixture manifest: %v", err)
	}
	data, err := os.ReadFile(claudeFixtureToolsPath)
	if err != nil {
		t.Fatalf("read claude tools fixture: %v", err)
	}
	sum := sha256.Sum256(data)
	if manifest.Tools.Path != "tools.json" {
		t.Errorf("manifest tools path = %q, want tools.json", manifest.Tools.Path)
	}
	if manifest.Tools.Bytes != len(data) {
		t.Errorf("manifest tools bytes = %d, fixture has %d", manifest.Tools.Bytes, len(data))
	}
	if got := hex.EncodeToString(sum[:]); manifest.Tools.SHA256 != got {
		t.Errorf("manifest tools sha256 = %s, fixture is %s", manifest.Tools.SHA256, got)
	}
	bashCap := claudeFixtureDefaultBashCap(t)
	if want := int(bashCap / time.Second); manifest.Tools.BashTimeoutCapSeconds != want {
		t.Errorf("manifest bash_timeout_cap_seconds = %d, default config has %d", manifest.Tools.BashTimeoutCapSeconds, want)
	}
	if want := claudeFixtureToolsScope(bashCap); manifest.Tools.Scope != want {
		t.Errorf("manifest tools scope = %q, want %q", manifest.Tools.Scope, want)
	}
}
