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
	"github.com/luispabon/steiner/internal/prompt"
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

type phaseRunnerParams struct {
	ProjectRoot        string
	WorkDir            string
	ModelAlias         string
	Approver           tool.ApprovalResponder
	AdvisorCfg         config.AdvisorConfig
	MaxTurns           int
	RunMode            string
	StreamingPreferred bool
	Events             output.EventSink
	PromptCacheKey     string
	PhasePrompt        string
	WorkflowMode       prompt.WorkflowMode
}

func newPhaseRunner(ctx context.Context, cmd *cobra.Command, flags *cliFlags, params phaseRunnerParams) (oneshot.PhaseRunner, error) {
	runtime, err := buildRuntimeWithRoots(ctx, cmd, flags, params.ProjectRoot, params.WorkDir, params.ModelAlias)
	if err != nil {
		return nil, err
	}
	if params.Events != nil {
		runtime.events = output.NewMultiSink(params.Events, runtime.events)
	}
	phaseAdvisor := runtime.cfg.Advisor
	if params.AdvisorCfg.MaxUsesPerRun > 0 {
		phaseAdvisor.MaxUsesPerRun = params.AdvisorCfg.MaxUsesPerRun
	}
	if params.AdvisorCfg.MaxTokens != nil {
		value := *params.AdvisorCfg.MaxTokens
		phaseAdvisor.MaxTokens = &value
	}
	phaseAdvisor.Enabled = true
	runtime.cfg.Advisor = phaseAdvisor

	runner := cliRunner{
		runtime:            runtime,
		approver:           params.Approver,
		maxTurns:           params.MaxTurns,
		runMode:            params.RunMode,
		streamingPreferred: params.StreamingPreferred,
		promptCacheKey:     params.PromptCacheKey,
		phasePrompt:        params.PhasePrompt,
		workflowMode:       params.WorkflowMode,
	}
	if alias := strings.TrimSpace(params.ModelAlias); alias != "" {
		runner.currentAlias = func() string {
			return alias
		}
	}
	return phaseRunner{runner: runner}, nil
}

func (r phaseRunner) RunPhase(ctx context.Context, conversation []agent.Message, skillNames []string, drainSteers func() []agent.SteerMessage) (oneshot.RunResult, error) {
	defer closeRuntime(&r.runner.runtime)
	return r.runner.RunPhase(ctx, conversation, skillNames, drainSteers)
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
	if manifest.ReportPath != "" {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "report: %s\n", manifest.ReportPath); err != nil {
			return err
		}
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
	if updated.ReportPath != "" {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "report: %s\n", updated.ReportPath); err != nil {
			return err
		}
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
	events   output.EventSink
}

func (f phaseRunnerFactory) NewPhaseRunner(ctx context.Context, phase oneshot.Phase, modelAlias string, approver tool.ApprovalResponder, advisorCfg config.AdvisorConfig) (oneshot.PhaseRunner, error) {
	phasePrompt, err := oneshot.LoadPrompt(phase)
	if err != nil {
		return nil, err
	}
	return newPhaseRunner(ctx, f.cmd, f.flags, phaseRunnerParams{
		ProjectRoot:        f.rootDir,
		WorkDir:            f.identity.WorktreePath(f.rootDir),
		ModelAlias:         modelAlias,
		Approver:           approver,
		AdvisorCfg:         advisorCfg,
		MaxTurns:           0,
		RunMode:            "oneshot",
		StreamingPreferred: false,
		Events:             f.events,
		PromptCacheKey:     f.identity.ID,
		PhasePrompt:        phasePrompt,
		WorkflowMode:       prompt.DelegatedChildWorkflowMode(),
	})
}

func renderOneshotRuns(stream *output.EventStream, runs []oneshot.ResumableRun) {
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
