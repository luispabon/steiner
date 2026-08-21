package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

type fakeOneshotOrchestrator struct {
	runManifest    oneshot.Manifest
	resumeManifest oneshot.Manifest
	runErr         error
	resumeErr      error
}

func (f *fakeOneshotOrchestrator) Run(context.Context) (oneshot.Manifest, error) {
	return f.runManifest, f.runErr
}

func (f *fakeOneshotOrchestrator) Resume(context.Context) (oneshot.Manifest, error) {
	return f.resumeManifest, f.resumeErr
}

func TestOneshotCommandList(t *testing.T) {
	oldListRuns := listOneshotRuns
	t.Cleanup(func() { listOneshotRuns = oldListRuns })

	listOneshotRuns = func(string) ([]oneshot.ResumableRun, error) {
		return []oneshot.ResumableRun{
			{
				RunID:        "run-123",
				Task:         "Ship parser",
				ResumePhase:  oneshot.PhaseImplement,
				LockState:    "absent",
				Status:       "resume at implement",
				UpdatedAt:    time.Now().Add(-time.Minute),
				WorktreePath: "/tmp/worktree",
			},
		}, nil
	}

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"oneshot", "--list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	for _, want := range []string{"run-123", "Ship parser", "resume at implement"} {
		if !strings.Contains(got, want) {
			t.Fatalf("list output missing %q:\n%s", want, got)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestOneshotCommandRun(t *testing.T) {
	oldBuildRuntime := buildRuntime
	oldNewOrchestrator := newOneshotOrchestrator
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
		newOneshotOrchestrator = oldNewOrchestrator
	})

	projectRoot := t.TempDir()
	captured := oneshot.Dependencies{}
	newOneshotOrchestrator = func(deps oneshot.Dependencies) (oneshotOrchestrator, error) {
		captured = deps
		return &fakeOneshotOrchestrator{
			runManifest: oneshot.Manifest{
				RunID:        "run-abc",
				Slug:         "build-parser",
				Task:         "Build parser",
				Branch:       "oneshot/build-parser-run-abc",
				WorktreePath: filepath.Join(projectRoot, ".steiner", "worktrees", "oneshot-run-abc"),
			},
		}, nil
	}
	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
		return cliRuntime{
			cfg:          testRuntimeConfig("test-model"),
			projectRoot:  projectRoot,
			sessionStore: nil,
			events:       output.NoopSink{},
		}, nil
	}

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"oneshot", "Build parser"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if captured.Task != "Build parser" {
		t.Fatalf("dependency task = %q, want Build parser", captured.Task)
	}
	if got, want := captured.Identity.Slug, oneshot.SlugFromTask("Build parser"); got != want {
		t.Fatalf("dependency slug = %q, want %q", got, want)
	}
	if got, want := captured.ProjectRoot, projectRoot; got != want {
		t.Fatalf("dependency project root = %q, want %q", got, want)
	}
	if !strings.Contains(stdout.String(), `"run_id": "run-abc"`) {
		t.Fatalf("stdout missing run manifest:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestOneshotCommandResume(t *testing.T) {
	oldBuildRuntime := buildRuntime
	oldNewOrchestrator := newOneshotOrchestrator
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
		newOneshotOrchestrator = oldNewOrchestrator
	})

	projectRoot := t.TempDir()
	resumeID := "resume-123"
	manifest := oneshot.Manifest{
		RunID:        resumeID,
		Slug:         "repair-cache",
		Task:         "Repair cache",
		Branch:       "oneshot/repair-cache-resume-123",
		WorktreePath: filepath.Join(projectRoot, ".steiner", "worktrees", "oneshot-"+resumeID),
	}
	stateDir := oneshot.RunIdentity{ID: resumeID}.StateDir(projectRoot)
	mustMkdirAll(t, stateDir)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	writeFile(t, filepath.Join(stateDir, "run.json"), string(data))

	captured := oneshot.Dependencies{}
	newOneshotOrchestrator = func(deps oneshot.Dependencies) (oneshotOrchestrator, error) {
		captured = deps
		return &fakeOneshotOrchestrator{
			resumeManifest: oneshot.Manifest{
				RunID:        resumeID,
				Slug:         manifest.Slug,
				Task:         manifest.Task,
				Branch:       manifest.Branch,
				WorktreePath: manifest.WorktreePath,
			},
		}, nil
	}
	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
		return cliRuntime{
			cfg:          testRuntimeConfig("test-model"),
			projectRoot:  projectRoot,
			sessionStore: nil,
			events:       output.NoopSink{},
		}, nil
	}

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"oneshot", "--resume", resumeID})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if captured.Task != manifest.Task {
		t.Fatalf("dependency task = %q, want %q", captured.Task, manifest.Task)
	}
	if got, want := captured.Identity.Slug, manifest.Slug; got != want {
		t.Fatalf("dependency slug = %q, want %q", got, want)
	}
	if got, want := captured.ProjectRoot, projectRoot; got != want {
		t.Fatalf("dependency project root = %q, want %q", got, want)
	}
	if !strings.Contains(stdout.String(), `"run_id": "resume-123"`) {
		t.Fatalf("stdout missing resumed manifest:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestPhaseParamsCarryPhasePrompt(t *testing.T) {
	tests := []struct {
		phase          oneshot.Phase
		expectedMarker string
	}{
		{phase: oneshot.PhasePlan, expectedMarker: "# Plan Phase"},
		{phase: oneshot.PhaseImplement, expectedMarker: "## Execution Artifact"},
		{phase: oneshot.PhaseReview, expectedMarker: "# Review Phase"},
	}

	factory := phaseRunnerFactory{}

	for _, tt := range tests {
		t.Run(string(tt.phase), func(t *testing.T) {
			params, err := factory.phaseParams(tt.phase, "", nil, config.AdvisorConfig{})
			if err != nil {
				t.Fatalf("phaseParams(%s) failed: %v", tt.phase, err)
			}

			if !strings.Contains(params.PhasePrompt, tt.expectedMarker) {
				t.Errorf("phaseParams(%s).PhasePrompt missing expected marker %q\nGot: %q",
					tt.phase, tt.expectedMarker, params.PhasePrompt)
			}

			if params.WorkflowMode != prompt.DelegatedChildWorkflowMode() {
				t.Errorf("phaseParams(%s).WorkflowMode = %q, want %q",
					tt.phase, params.WorkflowMode, prompt.DelegatedChildWorkflowMode())
			}

			if !params.StreamingPreferred {
				t.Errorf("phaseParams(%s).StreamingPreferred = false, want true", tt.phase)
			}
		})
	}
}

func TestPhaseParamsProjectAgentsPathFromWorktree(t *testing.T) {
	projectRoot := t.TempDir()
	factory := phaseRunnerFactory{
		rootDir:  projectRoot,
		identity: oneshot.RunIdentity{ID: "run-abc"},
	}

	params, err := factory.phaseParams(oneshot.PhaseImplement, "", nil, config.AdvisorConfig{})
	if err != nil {
		t.Fatalf("phaseParams failed: %v", err)
	}

	wantWorktree := filepath.Join(projectRoot, ".steiner", "worktrees", "oneshot-run-abc")
	if got, want := params.WorkDir, wantWorktree; got != want {
		t.Errorf("WorkDir = %q, want %q", got, want)
	}
	if got, want := params.ProjectAgentsPath, filepath.Join(wantWorktree, "AGENTS.md"); got != want {
		t.Errorf("ProjectAgentsPath = %q, want %q", got, want)
	}
	if got, want := params.ProjectRoot, projectRoot; got != want {
		t.Errorf("ProjectRoot = %q, want %q (must stay the main checkout)", got, want)
	}
}

func TestPromptAssemblyCarriesPhasePrompt(t *testing.T) {
	t.Run("phase runner carries phase prompt", func(t *testing.T) {
		rt := cliRuntime{}
		runner := cliRunner{runtime: rt, phasePrompt: "SENTINEL-PHASE-PROMPT", workflowMode: prompt.DelegatedChildWorkflowMode()}

		opts := runner.promptAssembly(nil, nil, prompt.ModelTokenBudget{}, config.ModelPrompts{})

		if got, want := opts.PhasePrompt, "SENTINEL-PHASE-PROMPT"; got != want {
			t.Errorf("AssemblyOptions.PhasePrompt = %q, want %q", got, want)
		}
		if got, want := opts.WorkflowMode, prompt.DelegatedChildWorkflowMode(); got != want {
			t.Errorf("AssemblyOptions.WorkflowMode = %q, want %q", got, want)
		}
	})

	t.Run("phase runner carries worktree AGENTS.md path", func(t *testing.T) {
		runner := cliRunner{runtime: cliRuntime{}, projectAgentsPath: "SENTINEL-AGENTS-PATH"}

		opts := runner.promptAssembly(nil, nil, prompt.ModelTokenBudget{}, config.ModelPrompts{})

		if got, want := opts.ProjectAgentsPath, "SENTINEL-AGENTS-PATH"; got != want {
			t.Errorf("AssemblyOptions.ProjectAgentsPath = %q, want %q", got, want)
		}
	})

	t.Run("non-phase runner has no phase prompt", func(t *testing.T) {
		runner := cliRunner{}

		opts := runner.promptAssembly(nil, nil, prompt.ModelTokenBudget{}, config.ModelPrompts{})

		if got := opts.PhasePrompt; got != "" {
			t.Errorf("AssemblyOptions.PhasePrompt = %q, want empty", got)
		}
	})
}
