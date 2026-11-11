package provider_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// claudeCoreDefaultBashCap returns limits.tool_timeouts.bash from the default
// config. Load runs with an empty env and temp dirs so no user or project
// config changes the defaults.
func claudeCoreDefaultBashCap(t *testing.T) time.Duration {
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

// claudeCoreToolSpecs returns the production core builtin tool specs for bashCap.
func claudeCoreToolSpecs(bashCap time.Duration) []provider.ToolSpec {
	env := builtin.Env{BashTimeoutCap: bashCap}
	return tool.NewRegistry(builtin.Builtins(env)...).ToProviderSpecs()
}

// claudeCoreCanonicalTools encodes specs as indented JSON with a trailing newline.
func claudeCoreCanonicalTools(specs []provider.ToolSpec) ([]byte, error) {
	data, err := json.MarshalIndent(specs, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode core tool specs: %w", err)
	}
	return append(data, '\n'), nil
}

// TestClaudeTokenProductionCoreBuiltinsAreDeterministic checks that the current
// core builtin provider specs serialize deterministically. It deliberately does
// not pin the tool name list, so adding or retiring a builtin leaves it valid.
func TestClaudeTokenProductionCoreBuiltinsAreDeterministic(t *testing.T) {
	t.Parallel()
	bashCap := claudeCoreDefaultBashCap(t)
	specs := claudeCoreToolSpecs(bashCap)
	if len(specs) == 0 {
		t.Fatal("core builtin tool specs are empty")
	}
	got, err := claudeCoreCanonicalTools(specs)
	if err != nil {
		t.Fatal(err)
	}
	again, err := claudeCoreCanonicalTools(claudeCoreToolSpecs(bashCap))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, again) {
		t.Fatal("canonical tool JSON differs between two builds of the same builtins")
	}
}
