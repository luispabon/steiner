package delegation

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// TestBuildDelegateRegistryForwardsExecutorConfigToChild proves that the
// ExecutorConfig on DelegateDeps reaches the executor of a child started through
// BuildDelegateRegistry. Config is left empty so the only path policy the child
// can receive is the one forwarded from ExecutorConfig.
func TestBuildDelegateRegistryForwardsExecutorConfigToChild(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		blockPaths bool
		wantDenied bool
	}{
		{name: "configured blocked path reaches child executor", blockPaths: true, wantDenied: true},
		{name: "zero ExecutorConfig allows the same read", blockPaths: false, wantDenied: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			workDir := t.TempDir()
			h := newParallelHarness(delegationParentResponse("explore"), 1)
			h.workDir = workDir
			close(h.done)

			cfg := config.Config{}
			if tc.blockPaths {
				cfg.Paths = config.PathsConfig{BlockedPaths: []string{filepath.Join(workDir, "secrets")}}
			}
			base := tool.NewRegistry(tool.ToolDef{
				Name:    "read",
				Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
			})
			childEvents := &lockedSink{}
			store := NewSessionStore()

			reg, err := BuildDelegateRegistry(DelegateDeps{
				BaseRegistry:       base,
				SubAgentCfg:        config.SubAgentConfig{Enabled: true, MaxTurns: 1, MaxTokens: 1000, MaxFollowUps: 100, MaxParallel: 1},
				Provider:           h.provider,
				Config:             config.Config{},
				ExecutorConfig:     tool.ExecutorConfigFromConfig(cfg),
				WorkDir:            workDir,
				Events:             &lockedSink{},
				ChildEvents:        childEvents,
				StreamingPreferred: true,
				SessionStore:       store,
			})
			if err != nil {
				t.Fatalf("BuildDelegateRegistry() error = %v", err)
			}
			executor := tool.NewExecutor(reg, config.Config{}, nil, workDir, "", tool.Unsandboxed{})
			if _, err := executor.Execute(context.Background(), SubAgentToolName, "call-0", delegationParentResponse("explore").Message.ToolCalls[0].Arguments); err != nil {
				t.Fatalf("delegate Execute() error = %v", err)
			}

			var agentID string
			for _, e := range childEvents.delegationEvents() {
				if payload, ok := e.Payload.(output.DelegationStartedEvent); ok {
					agentID = payload.AgentID
				}
			}
			if agentID == "" {
				t.Fatal("no delegation started event captured; child did not run")
			}
			session, ok := store.Get(agentID)
			if !ok {
				t.Fatalf("child session for agent %q not saved", agentID)
			}

			_, err = session.Request.Executor.Execute(context.Background(), "read", "", map[string]any{"path": filepath.Join(workDir, "secrets", "key.txt")})
			var toolErr *tool.ToolExecutionError
			denied := errors.As(err, &toolErr) && toolErr.Kind == "policy_denied"
			if tc.wantDenied && !denied {
				t.Fatalf("child Execute(read blocked path) error = %v, want policy_denied from the forwarded ExecutorConfig", err)
			}
			if !tc.wantDenied && err != nil {
				t.Fatalf("child Execute(read) error = %v, want success when no paths are blocked", err)
			}
		})
	}
}
