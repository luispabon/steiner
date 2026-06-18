package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

type oneshotOrchestrator interface {
	Run(context.Context) (oneshot.Manifest, error)
	Resume(context.Context) (oneshot.Manifest, error)
}

var newOneshotOrchestrator = func(deps oneshot.Dependencies) (oneshotOrchestrator, error) {
	return oneshot.NewOrchestrator(deps)
}

var listOneshotRuns = oneshot.ListRuns

type phaseRunner struct {
	runner cliRunner
}

func newPhaseRunner(ctx context.Context, cmd *cobra.Command, flags *cliFlags, projectRoot, workDir, modelAlias string, approver tool.ApprovalResponder, advisorCfg config.AdvisorConfig, maxTurns int, runMode string, streamingPreferred bool) (oneshot.PhaseRunner, error) {
	runtime, err := buildRuntimeWithRoots(ctx, cmd, flags, projectRoot, workDir, modelAlias)
	if err != nil {
		return nil, err
	}
	phaseAdvisor := runtime.cfg.Advisor
	if advisorCfg.Model != "" {
		phaseAdvisor.Model = advisorCfg.Model
	}
	if advisorCfg.MaxUsesPerRun > 0 {
		phaseAdvisor.MaxUsesPerRun = advisorCfg.MaxUsesPerRun
	}
	if advisorCfg.MaxTokens != nil {
		value := *advisorCfg.MaxTokens
		phaseAdvisor.MaxTokens = &value
	}
	phaseAdvisor.Enabled = true
	runtime.cfg.Advisor = phaseAdvisor

	runner := cliRunner{
		runtime:            runtime,
		approver:           approver,
		maxTurns:           maxTurns,
		runMode:            runMode,
		streamingPreferred: streamingPreferred,
	}
	if alias := strings.TrimSpace(modelAlias); alias != "" {
		runner.currentAlias = func() string {
			return alias
		}
	}
	return phaseRunner{runner: runner}, nil
}

func (r phaseRunner) RunPhase(ctx context.Context, conversation []agent.Message, skillNames []string, steerCh <-chan string) (oneshot.RunResult, error) {
	defer closeRuntime(&r.runner.runtime)
	return r.runner.RunPhase(ctx, conversation, skillNames, steerCh)
}

func runOneshotTask(cmd *cobra.Command, flags *cliFlags, task string) error {
	rt, err := buildRuntime(cmd.Context(), cmd, flags)
	if err != nil {
		return err
	}
	defer closeRuntime(&rt)

	identity, err := oneshot.NewRunIdentity(task)
	if err != nil {
		return err
	}

	orch, err := newOneshotOrchestrator(oneshot.Dependencies{
		ProjectRoot:  rt.projectRoot,
		Identity:     identity,
		Task:         task,
		Config:       rt.cfg,
		SessionStore: rt.sessionStore,
		RunnerFactory: phaseRunnerFactory{
			cmd:      cmd,
			flags:    flags,
			rootDir:  rt.projectRoot,
			identity: identity,
		},
		Events: rt.events,
	})
	if err != nil {
		return err
	}

	manifest, err := orch.Run(cmd.Context())
	if err != nil {
		return err
	}
	return printOneshotManifest(cmd.OutOrStdout(), manifest)
}

func runOneshotResume(cmd *cobra.Command, flags *cliFlags, resumeID string) error {
	rt, err := buildRuntime(cmd.Context(), cmd, flags)
	if err != nil {
		return err
	}
	defer closeRuntime(&rt)

	store := oneshot.NewManifestStore(oneshot.RunIdentity{ID: strings.TrimSpace(resumeID)}.ManifestPath(rt.projectRoot))
	manifest, err := store.Read()
	if err != nil {
		return fmt.Errorf("load oneshot manifest: %w", err)
	}
	if strings.TrimSpace(manifest.RunID) == "" {
		return fmt.Errorf("load oneshot manifest: run id is required")
	}

	identity := oneshot.RunIdentity{ID: manifest.RunID, Slug: manifest.Slug}
	orch, err := newOneshotOrchestrator(oneshot.Dependencies{
		ProjectRoot:   rt.projectRoot,
		Identity:      identity,
		Task:          manifest.Task,
		Config:        rt.cfg,
		ManifestStore: store,
		SessionStore:  rt.sessionStore,
		RunnerFactory: phaseRunnerFactory{
			cmd:      cmd,
			flags:    flags,
			rootDir:  rt.projectRoot,
			identity: identity,
		},
		Events: rt.events,
	})
	if err != nil {
		return err
	}

	updated, err := orch.Resume(cmd.Context())
	if err != nil {
		return err
	}
	return printOneshotManifest(cmd.OutOrStdout(), updated)
}

func runOneshotList(cmd *cobra.Command) error {
	projectRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	runs, err := listOneshotRuns(projectRoot)
	if err != nil {
		return err
	}
	renderOneshotRuns(output.NewStream(cmd.OutOrStdout()), runs)
	return nil
}

func printOneshotManifest(w interface{ Write([]byte) (int, error) }, manifest oneshot.Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal oneshot manifest: %w", err)
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

type phaseRunnerFactory struct {
	cmd      *cobra.Command
	flags    *cliFlags
	rootDir  string
	identity oneshot.RunIdentity
}

func (f phaseRunnerFactory) NewPhaseRunner(ctx context.Context, phase oneshot.Phase, modelAlias string, approver tool.ApprovalResponder, advisorCfg config.AdvisorConfig) (oneshot.PhaseRunner, error) {
	return newPhaseRunner(ctx, f.cmd, f.flags, f.rootDir, f.identity.WorktreePath(f.rootDir), modelAlias, approver, advisorCfg, 0, "oneshot", false)
}

func renderOneshotRuns(stream *output.Stream, runs []oneshot.ResumableRun) {
	if stream == nil {
		return
	}
	if len(runs) == 0 {
		stream.Printf("no resumable oneshot runs\n")
		return
	}

	stream.Printf("%-4s %-16s %-20s %-18s %-12s %-18s %s\n", "#", "Run ID", "Phase", "Status", "Lock", "Updated", "Task")
	for i, run := range runs {
		task := run.Task
		if len(task) > 48 {
			task = task[:45] + "..."
		}
		stream.Printf("%-4d %-16s %-20s %-18s %-12s %-18s %s\n", i, run.RunID, string(run.ResumePhase), run.Status, run.LockState, formatRelativeTime(run.UpdatedAt), task)
	}
}
