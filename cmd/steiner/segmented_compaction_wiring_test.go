package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// nonDefaultSegmentedPolicy is distinguishable from both the zero value and
// the parsed defaults, so a test can prove the runtime value was copied.
func nonDefaultSegmentedPolicy() config.SegmentedCompactionPolicy {
	return config.SegmentedCompactionPolicy{
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

// segmentedCompactionRuntimeFixture writes a minimal provider config into a
// temp project and isolates the caches and home state, so the real runtime
// builders run without touching the developer's directories.
func segmentedCompactionRuntimeFixture(t *testing.T) (*cobra.Command, *cliFlags, string) {
	t.Helper()
	useSeededModelsDevCache(t)
	isolateQuotaState(t)
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
	return cmd, &cliFlags{configPath: configPath, trustProjectConfig: true, exec: true}, projectRoot
}

// isolateSegmentedCompactionEnv clears the four experiment variables this
// package parses so ambient values cannot leak into a test. t.Setenv records the
// current values first, so cleanup restores the developer's environment.
func isolateSegmentedCompactionEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		config.SegmentedCompactionEnv,
		config.SegmentMaxCountEnv,
		config.SegmentMaxChainPercentEnv,
		config.SegmentMaxPostPercentEnv,
	} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}

// segmentedEnabledFromEnv returns the policy the env vars in the test should
// parse to, so the assertion does not restate the parser's defaults.
func segmentedEnabledFromEnv(t *testing.T) config.SegmentedCompactionPolicy {
	t.Helper()
	want, err := config.ParseSegmentedCompactionPolicy(os.LookupEnv)
	if err != nil {
		t.Fatalf("ParseSegmentedCompactionPolicy() error = %v", err)
	}
	if !want.Enabled {
		t.Fatal("parsed policy is disabled; the test env vars did not take effect")
	}
	return want
}

// TestPhaseRunnerRuntimeCarriesParsedSegmentedCompaction proves the oneshot
// phase runtime built through newPhaseRunner (and its real buildPhaseRuntime
// constructor) carries the policy parsed from the environment.
func TestPhaseRunnerRuntimeCarriesParsedSegmentedCompaction(t *testing.T) {
	isolateSegmentedCompactionEnv(t)
	t.Setenv(config.SegmentedCompactionEnv, "1")
	t.Setenv(config.SegmentMaxCountEnv, "3")
	cmd, flags, projectRoot := segmentedCompactionRuntimeFixture(t)

	phase, err := newPhaseRunner(context.Background(), cmd, flags, phaseRunnerParams{
		ProjectRoot: projectRoot,
		WorkDir:     projectRoot,
		MaxTurns:    1,
	})
	if err != nil {
		t.Fatalf("newPhaseRunner() error = %v", err)
	}
	runner, ok := phase.(phaseRunner)
	if !ok {
		t.Fatalf("newPhaseRunner() = %T, want phaseRunner", phase)
	}
	rt := runner.runner.runtime
	t.Cleanup(func() { closeRuntime(&rt) })

	want := segmentedEnabledFromEnv(t)
	if want.MaxSegments != 3 {
		t.Fatalf("parsed MaxSegments = %d, want the test env value 3", want.MaxSegments)
	}
	if got := rt.segmentedCompaction; got != want {
		t.Fatalf("phase runtime segmentedCompaction = %+v, want %+v", got, want)
	}
}

// TestInteractiveRuntimeCarriesParsedSegmentedCompaction proves the policy
// parsed by the real runtime builder survives the interactive session and
// buildInteractiveRuntime rewiring.
func TestInteractiveRuntimeCarriesParsedSegmentedCompaction(t *testing.T) {
	isolateSegmentedCompactionEnv(t)
	t.Setenv(config.SegmentedCompactionEnv, "1")
	t.Setenv(config.SegmentMaxChainPercentEnv, "20")
	cmd, flags, projectRoot := segmentedCompactionRuntimeFixture(t)

	rt, err := buildRuntimeWithRoots(context.Background(), cmd, flags, projectRoot, projectRoot, "")
	if err != nil {
		t.Fatalf("buildRuntimeWithRoots() error = %v", err)
	}
	t.Cleanup(func() { closeRuntime(&rt) })
	sess, err := buildInteractiveSession(rt)
	if err != nil {
		t.Fatalf("buildInteractiveSession() error = %v", err)
	}
	rt = buildInteractiveRuntime(rt, sess)

	want := segmentedEnabledFromEnv(t)
	if want.MaxChainPercent != 20 {
		t.Fatalf("parsed MaxChainPercent = %v, want the test env value 20", want.MaxChainPercent)
	}
	if got := rt.segmentedCompaction; got != want {
		t.Fatalf("interactive runtime segmentedCompaction = %+v, want %+v", got, want)
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
		{name: "enabled", env: config.SegmentedCompactionEnv, value: "maybe"},
		{name: "max segment count", env: config.SegmentMaxCountEnv, value: "0"},
		{name: "max chain percent", env: config.SegmentMaxChainPercentEnv, value: "0"},
		{name: "max post percent", env: config.SegmentMaxPostPercentEnv, value: "101"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolateSegmentedCompactionEnv(t)
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
