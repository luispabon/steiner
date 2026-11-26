package delegation

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// segmentedCompactionEnabledPolicy returns a non-default policy so a propagated
// value cannot be confused with the zero value a dropped field would leave.
func segmentedCompactionEnabledPolicy() config.SegmentedCompactionPolicy {
	return config.SegmentedCompactionPolicy{
		Enabled:                  true,
		MaxSegments:              5,
		MaxChainPercent:          20,
		MaxPostCompactionPercent: 40,
	}
}

// segmentedPolicyCase pairs a policy with the subtest name that reports it.
type segmentedPolicyCase struct {
	name   string
	policy config.SegmentedCompactionPolicy
}

// segmentedPolicyCases is the flag-off/enabled pair every propagation test runs.
func segmentedPolicyCases() []segmentedPolicyCase {
	return []segmentedPolicyCase{
		{name: "flag off", policy: config.SegmentedCompactionPolicy{}},
		{name: "enabled", policy: segmentedCompactionEnabledPolicy()},
	}
}

// captureRunRequestRunner returns a runner that records the request it is
// handed and reports a successful run.
func captureRunRequestRunner(captured *agent.RunRequest) *mockRunner {
	return &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		*captured = req
		return successRunState(), nil
	}}
}

// TestBuildChildRunThreadsSegmentedCompaction proves an ordinary child run
// request carries the parent's segmented compaction policy, and that the
// flag-off zero value stays disabled.
func TestBuildChildRunThreadsSegmentedCompaction(t *testing.T) {
	t.Parallel()
	for _, tt := range segmentedPolicyCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deps := SubAgentHandlerDeps{
				Provider:            stubProvider{},
				ResolvedModel:       provider.ResolvedModel{BackendModelID: "child-model"},
				ParentReg:           tool.NewRegistry(tool.ToolDef{Name: "read"}),
				Sandbox:             tool.Unsandboxed{},
				WorkDir:             t.TempDir(),
				SegmentedCompaction: tt.policy,
			}
			override := ChildBootstrapOverrides{
				AgentType:     AgentTypeExplore,
				AllowedTools:  []string{"read"},
				Provider:      stubProvider{},
				ResolvedModel: provider.ResolvedModel{BackendModelID: "child-model"},
			}

			req, _, err := BuildChildRun(context.Background(), deps, override, Spec{Task: "t", AgentID: "a", Limits: Limits{MaxTurns: 1}})
			if err != nil {
				t.Fatalf("BuildChildRun() error = %v", err)
			}
			if req.SegmentedCompaction != tt.policy {
				t.Fatalf("child SegmentedCompaction = %+v, want %+v", req.SegmentedCompaction, tt.policy)
			}
		})
	}
}

// TestVisionChildRunThreadsSegmentedCompaction proves the vision agent's child
// run request carries the parent's segmented compaction policy. Vision builds
// its child run from ChildBootstrapOverrides rather than the specialized path,
// so a dropped policy there would not be caught by the other child-run tests.
func TestVisionChildRunThreadsSegmentedCompaction(t *testing.T) {
	t.Parallel()
	for _, tt := range segmentedPolicyCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			imgPath := filepath.Join(dir, "segment.png")
			if err := os.WriteFile(imgPath, []byte("fake-png-content"), 0o600); err != nil {
				t.Fatalf("write temp image: %v", err)
			}
			store := agent.NewImageStore(dir)
			ref := store.Register(imgPath, "image/png", 10, 20, 15)

			var captured agent.RunRequest
			deps := minimalDeps(captureRunRequestRunner(&captured))
			deps.WorkDir = t.TempDir()
			deps.ImageStore = store
			deps.SegmentedCompaction = tt.policy

			if _, err := newVisionHandler(deps)(context.Background(), subAgentTaskWithImageID("describe the image", ref.ID)); err != nil {
				t.Fatalf("vision handler() error = %v", err)
			}
			if captured.SegmentedCompaction != tt.policy {
				t.Fatalf("vision child SegmentedCompaction = %+v, want %+v", captured.SegmentedCompaction, tt.policy)
			}
		})
	}
}

// TestSpecializedChildRunThreadsSegmentedCompaction proves the specialized
// sub_agent path (buildSpecializedRun) forwards the policy too.
func TestSpecializedChildRunThreadsSegmentedCompaction(t *testing.T) {
	t.Parallel()
	policy := segmentedCompactionEnabledPolicy()
	var captured agent.RunRequest
	deps := minimalDeps(captureRunRequestRunner(&captured))
	deps.WorkDir = t.TempDir()
	deps.SegmentedCompaction = policy

	if _, err := SubAgentToolDef(deps, nil).Handler(context.Background(), subAgentTask(AgentTypeExplore, "inspect the repo")); err != nil {
		t.Fatalf("sub_agent handler() error = %v", err)
	}
	if captured.SegmentedCompaction != policy {
		t.Fatalf("child SegmentedCompaction = %+v, want %+v", captured.SegmentedCompaction, policy)
	}
}

// TestFollowUpContinuationCarriesSegmentedCompaction proves a stored child
// session's follow_up resume inherits the policy recorded on its original
// request: buildContinuationRequest copies the request by value and runFollowUp
// seeds it from session.Request.
func TestFollowUpContinuationCarriesSegmentedCompaction(t *testing.T) {
	t.Parallel()
	policy := segmentedCompactionEnabledPolicy()
	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec:    Spec{AgentID: "child-segmented", Task: "inspect code", Limits: Limits{MaxTurns: 2}},
		Request: agent.RunRequest{Prompt: promptWithConversation("initial task"), SegmentedCompaction: policy},
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "initial task"},
			{Role: agent.MessageRoleAssistant, Content: "first answer"},
		},
		TurnCount: 1,
	})

	var captured agent.RunRequest
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SubAgentCfg:  config.SubAgentConfig{MaxTurns: 5, MaxTokens: 100, MaxFollowUps: 3},
		Events:       noopEventSink{},
		SessionStore: store,
		Runner:       captureRunRequestRunner(&captured),
	})

	if _, err := handler(context.Background(), map[string]any{"agent_id": "child-segmented", "message": "continue"}); err != nil {
		t.Fatalf("follow_up handler() error = %v", err)
	}
	if captured.SegmentedCompaction != policy {
		t.Fatalf("follow-up request SegmentedCompaction = %+v, want %+v", captured.SegmentedCompaction, policy)
	}
}

// TestRemediationContinuationCarriesSegmentedCompaction proves the post-run
// remediation continuation, which runs after the initial child run, forwards
// the policy carried by the request it derives its continuation from.
func TestRemediationContinuationCarriesSegmentedCompaction(t *testing.T) {
	t.Parallel()
	policy := segmentedCompactionEnabledPolicy()
	req := remediationTestRequest()
	req.SegmentedCompaction = policy
	var captured agent.RunRequest
	runner := captureRunRequestRunner(&captured)
	cfg := remediationConfigForDirty([]string{"changed.go"}, nil, func() (bool, error) { return true, nil })

	_, _, _, outcome, err := applyRemediation(
		context.Background(), remediationTestSpec(), req, runner,
		successRunState(), TokenUsage{}, cfg, newTraceCollector("remediation-agent", "implement the task"),
	)
	if err != nil {
		t.Fatalf("applyRemediation() error = %v", err)
	}
	if outcome != remediationAttempted {
		t.Fatalf("outcome = %q, want %q", outcome, remediationAttempted)
	}
	if captured.SegmentedCompaction != policy {
		t.Fatalf("remediation request SegmentedCompaction = %+v, want %+v", captured.SegmentedCompaction, policy)
	}
}

// TestBuildDelegateRegistryThreadsSegmentedCompaction proves BuildDelegateRegistry
// copies the policy from DelegateDeps into the spawned child request.
func TestBuildDelegateRegistryThreadsSegmentedCompaction(t *testing.T) {
	t.Parallel()
	var builds atomic.Int32
	policy := segmentedCompactionEnabledPolicy()
	base := compactionWiringRegistryDeps(t, compactionWiringRegistryConfig(), &builds, nil)
	base.SegmentedCompaction = policy

	reg, store := buildCompactionWiringRegistry(t, base)
	req := spawnCompactionWiringChild(t, reg, store, subAgentTask(AgentTypeExplore, "inspect the codebase"), "explore-segments")
	if req.SegmentedCompaction != policy {
		t.Fatalf("child SegmentedCompaction = %+v, want %+v", req.SegmentedCompaction, policy)
	}
}
