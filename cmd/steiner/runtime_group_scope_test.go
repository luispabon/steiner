package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestRuntimeFallbackGroupScopeIsInitializedOnce(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	writeFile(t, configPath, `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  profiles:
    default:
      default_model: test-model
  definitions:
    test-model:
      provider: local
      id: test-model
`)
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	rt, err := buildRuntimeWithRoots(context.Background(), cmd, &cliFlags{configPath: configPath}, root, root, "")
	if err != nil {
		t.Fatalf("buildRuntimeWithRoots() error = %v", err)
	}
	defer closeRuntime(&rt)
	if rt.delegationSupervisor == nil || rt.delegationFallbackGroupScope == "" {
		t.Fatal("runtime delegation fallback scope is not initialized")
	}
	ledger := rt.delegationSupervisor.SnapshotGroupLedger(rt.delegationFallbackGroupScope)
	if ledger.Version != 1 || len(ledger.Names) != 0 {
		t.Fatalf("fallback ledger = %+v, want version 1 and no names", ledger)
	}
}
