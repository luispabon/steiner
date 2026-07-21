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

func TestPhasePromptDelivery(t *testing.T) {
	tests := []struct {
		phase              oneshot.Phase
		expectedPromptText string
	}{
		{phase: oneshot.PhasePlan, expectedPromptText: "mandatory advisor"},
		{phase: oneshot.PhaseImplement, expectedPromptText: "## Execution Artifact"},
		{phase: oneshot.PhaseReview, expectedPromptText: "mandatory advisor"},
	}

	for _, tt := range tests {
		t.Run(string(tt.phase), func(t *testing.T) {
			phasePrompt, err := oneshot.LoadPrompt(tt.phase)
			if err != nil {
				t.Fatalf("LoadPrompt(%s) failed: %v", tt.phase, err)
			}
			if phasePrompt == "" {
				t.Fatalf("LoadPrompt(%s) returned empty string", tt.phase)
			}

			if !strings.Contains(phasePrompt, tt.expectedPromptText) {
				t.Errorf("phase prompt for %s missing expected text %q\nGot: %q",
					tt.phase, tt.expectedPromptText, phasePrompt)
			}

			wfMode := prompt.DelegatedChildWorkflowMode()
			if wfMode == "" {
				t.Error("DelegatedChildWorkflowMode() returned empty string")
			}
		})
	}
}
