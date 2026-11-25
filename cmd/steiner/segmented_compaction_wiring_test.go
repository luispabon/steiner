package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

// nonDefaultSegmentedPolicy is distinguishable from both the zero value and
// the parsed defaults, so a test can prove the runtime value was copied.
func nonDefaultSegmentedPolicy() agent.SegmentedCompactionPolicy {
	return agent.SegmentedCompactionPolicy{
		Enabled:                  true,
		MaxSegments:              3,
		MaxChainPercent:          20,
		MaxPostCompactionPercent: 40,
	}
}

func TestRunRequestsCarrySegmentedCompactionPolicy(t *testing.T) {
	policy := nonDefaultSegmentedPolicy()
	runner := cliRunner{runtime: cliRuntime{segmentedCompaction: policy}}

	mainReq := buildRunRequest(runner, runnerSetup{}, tool.NewRegistry(), nil, runHooks{})
	if mainReq.SegmentedCompaction != policy {
		t.Errorf("buildRunRequest SegmentedCompaction = %+v, want %+v", mainReq.SegmentedCompaction, policy)
	}

	compactionReq := runner.compactionRunRequest(runnerSetup{}, nil, agent.LastMainRequest{})
	if compactionReq.SegmentedCompaction != policy {
		t.Errorf("compactionRunRequest SegmentedCompaction = %+v, want %+v", compactionReq.SegmentedCompaction, policy)
	}
}

// TestRunRequestsDefaultSegmentedCompactionDisabled covers a runtime built with
// the feature unset: both requests carry a disabled policy, so flag-off
// behaviour is unchanged.
func TestRunRequestsDefaultSegmentedCompactionDisabled(t *testing.T) {
	runner := cliRunner{}

	mainReq := buildRunRequest(runner, runnerSetup{}, tool.NewRegistry(), nil, runHooks{})
	if mainReq.SegmentedCompaction.Enabled {
		t.Errorf("buildRunRequest SegmentedCompaction.Enabled = true, want false")
	}

	compactionReq := runner.compactionRunRequest(runnerSetup{}, nil, agent.LastMainRequest{})
	if compactionReq.SegmentedCompaction.Enabled {
		t.Errorf("compactionRunRequest SegmentedCompaction.Enabled = true, want false")
	}
}

func TestDelegateDepsCarrySegmentedCompactionPolicy(t *testing.T) {
	policy := nonDefaultSegmentedPolicy()
	runner := cliRunner{runtime: cliRuntime{segmentedCompaction: policy}}

	deps := runner.newDelegateDeps(runnerSetup{}, nil, nil, nil, "")
	if deps.SegmentedCompaction != policy {
		t.Errorf("DelegateDeps.SegmentedCompaction = %+v, want %+v", deps.SegmentedCompaction, policy)
	}
}

// TestBuildRuntimeRejectsInvalidSegmentedCompactionEnv proves a bad value fails
// startup before the runtime touches the project directory: the project root is
// unusable, so a project-directory error would mean the policy was parsed too
// late.
func TestBuildRuntimeRejectsInvalidSegmentedCompactionEnv(t *testing.T) {
	for _, tt := range []struct {
		name  string
		env   string
		value string
	}{
		{name: "enabled", env: agent.SegmentedCompactionEnv, value: "maybe"},
		{name: "max segment count", env: agent.SegmentMaxCountEnv, value: "0"},
		{name: "max chain percent", env: agent.SegmentMaxChainPercentEnv, value: "0"},
		{name: "max post percent", env: agent.SegmentMaxPostPercentEnv, value: "101"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.env, tt.value)
			blocker := filepath.Join(t.TempDir(), "blocker")
			if err := os.WriteFile(blocker, nil, 0o644); err != nil {
				t.Fatalf("create blocker file: %v", err)
			}
			projectRoot := filepath.Join(blocker, "project")

			_, err := buildRuntimeWithRoots(context.Background(), &cobra.Command{}, &cliFlags{}, projectRoot, projectRoot, "")
			if err == nil {
				t.Fatal("buildRuntimeWithRoots() error = nil, want an invalid policy error")
			}
			if !strings.Contains(err.Error(), "load segmented compaction policy: ") {
				t.Fatalf("error = %q, want it to wrap the policy load", err)
			}
			if !strings.Contains(err.Error(), tt.env) {
				t.Fatalf("error = %q, want it to name %s", err, tt.env)
			}
		})
	}
}
