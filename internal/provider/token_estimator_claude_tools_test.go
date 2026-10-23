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

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

const (
	claudeFixtureToolsPath    = "testdata/claude_token_estimation/tools.json"
	claudeFixtureManifestPath = "testdata/claude_token_estimation/manifest.json"
	claudeFixtureToolsScope   = "core builtins from builtin.Builtins(builtin.Env{BashTimeoutCap: 600s}) via tool.Registry.ToProviderSpecs, sorted by name; excludes advisor, lsp_*, mcp__*, config-defined, and sub-agent tools; core defaults only"
)

// claudeFixtureCoreToolSpecs returns the production core builtin tool specs
// with the default 600s bash timeout cap.
func claudeFixtureCoreToolSpecs() []provider.ToolSpec {
	env := builtin.Env{BashTimeoutCap: 600 * time.Second}
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

func TestClaudeTokenToolsFixtureMatchesProductionCoreBuiltins(t *testing.T) {
	specs := claudeFixtureCoreToolSpecs()
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
	again, err := claudeFixtureCanonicalTools(claudeFixtureCoreToolSpecs())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, again) {
		t.Fatal("canonical tool JSON differs between two builds of the same builtins")
	}
	fixture, err := os.ReadFile(claudeFixtureToolsPath)
	if err != nil {
		t.Fatalf("read claude tools fixture: %v", err)
	}
	if !bytes.Equal(got, fixture) {
		t.Fatalf("tools fixture does not match production core builtins: got %d bytes, fixture has %d bytes", len(got), len(fixture))
	}
}

func TestClaudeTokenToolsManifestPinsFixtureHashAndScope(t *testing.T) {
	raw, err := os.ReadFile(claudeFixtureManifestPath)
	if err != nil {
		t.Fatalf("read claude fixture manifest: %v", err)
	}
	var manifest struct {
		Tools struct {
			Path   string `json:"path"`
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
			Scope  string `json:"scope"`
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
	if manifest.Tools.Scope != claudeFixtureToolsScope {
		t.Errorf("manifest tools scope = %q, want %q", manifest.Tools.Scope, claudeFixtureToolsScope)
	}
}
