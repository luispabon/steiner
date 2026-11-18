package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
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
		cfg := testRuntimeConfig("test-model")
		cfg.SubAgent.Enabled = true
		return cliRuntime{
			cfg:          cfg,
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
		cfg := testRuntimeConfig("test-model")
		cfg.SubAgent.Enabled = true
		return cliRuntime{
			cfg:          cfg,
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

func TestNewPhaseRunnerRoutesClaudeSubscriptionToProviderFactory(t *testing.T) {
	oldBuildPhaseRuntime := buildPhaseRuntime
	t.Cleanup(func() { buildPhaseRuntime = oldBuildPhaseRuntime })
	var (
		gotModels   []provider.ResolvedModel
		gotSessions []string
	)
	built := &fakeProvider{}
	buildPhaseRuntime = func(context.Context, *cobra.Command, *cliFlags, string, string, string) (cliRuntime, error) {
		return cliRuntime{
			providerFactory: func(rm provider.ResolvedModel, sessionID string) (provider.Provider, error) {
				gotModels = append(gotModels, rm)
				gotSessions = append(gotSessions, sessionID)
				return built, nil
			},
		}, nil
	}
	runner, err := newPhaseRunner(context.Background(), nil, &cliFlags{}, phaseRunnerParams{})
	if err != nil {
		t.Fatalf("newPhaseRunner() error = %v", err)
	}
	phase := runner.(phaseRunner)
	models := []provider.ResolvedModel{
		{
			Alias:                 "claude",
			ProviderConfig:        config.ProviderConfig{Type: config.ProviderTypeClaudeSubscription},
			BackendModelID:        "claude-sonnet",
			EffectiveProviderType: config.ProviderTypeClaudeSubscription,
		},
		{
			Alias:                 "effective-claude",
			ProviderConfig:        config.ProviderConfig{Type: config.ProviderTypeOpenAICompat},
			BackendModelID:        "claude-opus",
			EffectiveProviderType: config.ProviderTypeClaudeSubscription,
		},
	}
	for i, rm := range models {
		got, err := phase.runner.runtime.providerFactory(rm, "session")
		if err != nil {
			t.Fatalf("providerFactory(%#v) error = %v, want nil", rm, err)
		}
		if got != provider.Provider(built) {
			t.Fatalf("providerFactory(%#v) provider = %v, want the runtime provider", rm, got)
		}
		if !reflect.DeepEqual(gotModels[i], rm) {
			t.Fatalf("factory model = %#v, want %#v (effective provider type unchanged)", gotModels[i], rm)
		}
		if gotSessions[i] != "session" {
			t.Fatalf("factory session ID = %q, want session", gotSessions[i])
		}
	}
}

func TestNewPhaseRunnerRoutesClaudeSubscriptionThroughDelegatedChild(t *testing.T) {
	oldBuildPhaseRuntime := buildPhaseRuntime
	t.Cleanup(func() { buildPhaseRuntime = oldBuildPhaseRuntime })

	cfg := testRuntimeConfig("claude")
	cfg.Providers = map[string]config.ProviderConfig{
		"claude": {Type: config.ProviderTypeClaudeSubscription},
		"local":  {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
	}
	cfg.Models.Definitions["claude"] = config.ModelConfig{Provider: "claude", ID: "claude-sonnet"}
	cfg.Models.Definitions["test-model"] = config.ModelConfig{Provider: "local", ID: "test-model"}
	cfg.Models.Effective.DefaultModel = "claude"
	cfg.Models.Effective.Advisor = "test-model"
	cfg.SubAgent.Enabled = true
	workDir := t.TempDir()
	stopChild := errors.New("child provider requested")
	var models []provider.ResolvedModel
	buildPhaseRuntime = func(context.Context, *cobra.Command, *cliFlags, string, string, string) (cliRuntime, error) {
		return cliRuntime{
			cfg:      cfg,
			workDir:  workDir,
			events:   output.NoopSink{},
			registry: tool.NewRegistry(),
			providerFactory: func(rm provider.ResolvedModel, _ string) (provider.Provider, error) {
				models = append(models, rm)
				if effectiveProviderType(rm) == config.ProviderTypeClaudeSubscription {
					return nil, stopChild
				}
				return &fakeProvider{}, nil
			},
		}, nil
	}

	runner, err := newPhaseRunner(context.Background(), &cobra.Command{}, &cliFlags{}, phaseRunnerParams{})
	if err != nil {
		t.Fatalf("newPhaseRunner() error = %v", err)
	}
	phase := runner.(phaseRunner)
	deps := phase.runner.newDelegateDeps(runnerSetup{provider: &fakeProvider{}}, nil, nil, nil, "")
	registry, err := delegation.BuildDelegateRegistry(deps)
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}
	def, ok := registry.Get(delegation.SubAgentToolName)
	if !ok {
		t.Fatal("delegated sub_agent tool missing")
	}
	_, err = def.Handler(context.Background(), subAgentTask("explore", "inspect", "context", "findings"))
	if !errors.Is(err, stopChild) && !strings.Contains(err.Error(), stopChild.Error()) {
		t.Fatalf("delegated child error = %v, want %v", err, stopChild)
	}
	var advisorRouted, childRouted bool
	for _, rm := range models {
		switch rm.Alias {
		case "test-model":
			advisorRouted = rm.EffectiveProviderType == config.ProviderTypeOpenAICompat && rm.BackendModelID == "test-model"
		case "claude":
			childRouted = rm.EffectiveProviderType == config.ProviderTypeClaudeSubscription &&
				rm.ProviderConfig.Type == config.ProviderTypeClaudeSubscription &&
				rm.BackendModelID == "claude-sonnet"
		}
	}
	if !advisorRouted {
		t.Fatalf("advisor model never reached the provider factory; factory models = %#v", models)
	}
	if !childRouted {
		t.Fatalf("claude_subscription child model never reached the provider factory; factory models = %#v", models)
	}
}

// claudeOneshotConfig returns a config whose models all run on a local provider
// except the "claude" alias, which uses claude_subscription.
func claudeOneshotConfig() config.Config {
	cfg := testRuntimeConfig("local-model")
	cfg.Providers["claude"] = config.ProviderConfig{Type: config.ProviderTypeClaudeSubscription}
	cfg.Models.Definitions["claude"] = config.ModelConfig{Provider: "claude", ID: "claude-sonnet"}
	cfg.Models.Effective.DefaultModel = "local-model"
	cfg.Models.Effective.Advisor = "local-model"
	cfg.Models.Effective.OneShot = map[string]string{}
	cfg.Models.Effective.SubAgents = map[string]string{}
	cfg.SubAgent.Enabled = true
	return cfg
}

func TestOneshotEntryPointsAcceptClaudeSubscription(t *testing.T) {
	oldBuildRuntime := buildRuntime
	oldNewOrchestrator := newOneshotOrchestrator
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
		newOneshotOrchestrator = oldNewOrchestrator
	})

	tests := []struct {
		name string
		run  func(*cobra.Command, *cliFlags) error
		prep func(*testing.T, string)
	}{
		{name: "run", run: func(cmd *cobra.Command, flags *cliFlags) error {
			return runOneshotTask(cmd, flags, "Build parser")
		}},
		{name: "resume", run: func(cmd *cobra.Command, flags *cliFlags) error {
			return runOneshotResume(cmd, flags, "run-abc")
		}, prep: func(t *testing.T, projectRoot string) {
			t.Helper()
			path := oneshot.RunIdentity{ID: "run-abc"}.ManifestPath(projectRoot)
			mustMkdirAll(t, filepath.Dir(path))
			data, err := json.MarshalIndent(oneshot.Manifest{RunID: "run-abc", Slug: "build-parser", Task: "Build parser"}, "", "  ")
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}
			writeFile(t, path, string(data))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectRoot := t.TempDir()
			if tt.prep != nil {
				tt.prep(t, projectRoot)
			}
			buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
				cfg := claudeOneshotConfig()
				cfg.Models.Effective.DefaultModel = "claude"
				return cliRuntime{cfg: cfg, projectRoot: projectRoot, events: output.NoopSink{}}, nil
			}
			constructed := false
			newOneshotOrchestrator = func(oneshot.Dependencies) (oneshotOrchestrator, error) {
				constructed = true
				return &fakeOneshotOrchestrator{
					runManifest:    oneshot.Manifest{RunID: "run-abc"},
					resumeManifest: oneshot.Manifest{RunID: "run-abc"},
				}, nil
			}

			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			cmd.SetOut(&bytes.Buffer{})
			if err := tt.run(cmd, &cliFlags{}); err != nil {
				t.Fatalf("error = %v, want a claude_subscription run to reach the orchestrator", err)
			}
			if !constructed {
				t.Fatal("newOneshotOrchestrator was not called; want the run to start")
			}
		})
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

			if params.CurrentEffective != nil {
				t.Errorf("phaseParams(%s).CurrentEffective is non-nil, want nil for headless factory", tt.phase)
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

func TestPhaseParamsCarriesOrchestrationLevel(t *testing.T) {
	factory := phaseRunnerFactory{
		orchestrationLevel: func() config.OrchestrationLevel { return config.OrchestrationLevelLow },
	}

	params, err := factory.phaseParams(oneshot.PhasePlan, "", nil, config.AdvisorConfig{})
	if err != nil {
		t.Fatalf("phaseParams failed: %v", err)
	}
	if params.OrchestrationLevel == nil {
		t.Fatal("phaseParams().OrchestrationLevel is nil, want getter carried through")
	}
	if got, want := params.OrchestrationLevel(), config.OrchestrationLevelLow; got != want {
		t.Errorf("phaseParams().OrchestrationLevel() = %q, want %q", got, want)
	}
}

func TestPhaseRunnerFactoryCarriesSessionDate(t *testing.T) {
	fixture, _ := time.Parse("2006-01-02", "2024-01-15")
	sessionDate := prompt.NewSessionDate(fixture)

	factory := phaseRunnerFactory{
		sessionDate: sessionDate,
	}

	for _, phase := range []oneshot.Phase{oneshot.PhasePlan, oneshot.PhaseImplement, oneshot.PhaseReview} {
		t.Run(string(phase), func(t *testing.T) {
			params, err := factory.phaseParams(phase, "", nil, config.AdvisorConfig{})
			if err != nil {
				t.Fatalf("phaseParams failed: %v", err)
			}
			if params.SessionDate != sessionDate {
				t.Errorf("phaseParams(%s).SessionDate = %v, want %v", phase, params.SessionDate, sessionDate)
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

// oneshotResolveCall records one call to the resolve seam.
type oneshotResolveCall struct {
	deps       oneshot.Dependencies
	resolution oneshot.Resolution
}

// oneshotResolveSpy replaces the resolve seam and records each call.
type oneshotResolveSpy struct {
	manifest oneshot.Manifest
	err      error
	calls    []oneshotResolveCall
}

func (s *oneshotResolveSpy) install(t *testing.T) {
	t.Helper()
	old := resolveOneshotCloseout
	t.Cleanup(func() { resolveOneshotCloseout = old })
	resolveOneshotCloseout = func(_ context.Context, deps oneshot.Dependencies, resolution oneshot.Resolution) (oneshot.Manifest, error) {
		s.calls = append(s.calls, oneshotResolveCall{deps: deps, resolution: resolution})
		return s.manifest, s.err
	}
}

// stubOneshotResolveRuntime replaces buildRuntime with a runtime rooted at
// projectRoot. The returned flag reports whether it was called.
func stubOneshotResolveRuntime(t *testing.T, projectRoot string) *bool {
	t.Helper()
	built := false
	old := buildRuntime
	t.Cleanup(func() { buildRuntime = old })
	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
		built = true
		cfg := testRuntimeConfig("test-model")
		cfg.SubAgent.Enabled = true
		return cliRuntime{cfg: cfg, projectRoot: projectRoot, events: output.NoopSink{}}, nil
	}
	return &built
}

// writeOneshotRunManifest writes manifest at runID's manifest path.
func writeOneshotRunManifest(t *testing.T, projectRoot, runID string, manifest oneshot.Manifest) {
	t.Helper()
	path := oneshot.RunIdentity{ID: runID}.ManifestPath(projectRoot)
	mustMkdirAll(t, filepath.Dir(path))
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	writeFile(t, path, string(data))
}

// executeOneshotArgs runs the root command with args and returns its output.
func executeOneshotArgs(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newRootCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestOneshotCommandResolveRecordsURL(t *testing.T) {
	const recordedURL = "https://example.com/pr/1"
	projectRoot := t.TempDir()
	writeOneshotRunManifest(t, projectRoot, "run-1", oneshot.Manifest{RunID: "run-1", Slug: "build-parser", Task: "Build parser"})
	stubOneshotResolveRuntime(t, projectRoot)
	spy := &oneshotResolveSpy{manifest: oneshot.Manifest{
		RunID:         "run-1",
		Slug:          "build-parser",
		Task:          "Build parser",
		CloseoutState: "created",
		CloseoutURL:   recordedURL,
	}}
	spy.install(t)

	stdout, stderr, err := executeOneshotArgs(t, "oneshot", "--resolve", "run-1", "--url", recordedURL)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(spy.calls) != 1 {
		t.Fatalf("resolve calls = %d, want 1", len(spy.calls))
	}
	call := spy.calls[0]
	if got, want := call.resolution.Kind, oneshot.ResolutionRecordURL; got != want {
		t.Errorf("resolution kind = %q, want %q", got, want)
	}
	if got, want := call.resolution.URL, recordedURL; got != want {
		t.Errorf("resolution URL = %q, want %q", got, want)
	}
	if got, want := call.deps.Identity, (oneshot.RunIdentity{ID: "run-1", Slug: "build-parser"}); got != want {
		t.Errorf("dependency identity = %+v, want %+v", got, want)
	}
	if got, want := call.deps.ProjectRoot, projectRoot; got != want {
		t.Errorf("dependency project root = %q, want %q", got, want)
	}
	if call.deps.ManifestStore == nil {
		t.Error("dependency manifest store is nil, want the run's store")
	}
	if !strings.Contains(stdout, "closeout: created "+recordedURL) {
		t.Errorf("stdout missing closeout line:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"run_id": "run-1"`) {
		t.Errorf("stdout missing manifest JSON:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestOneshotCommandResolveAuthorisesRetry(t *testing.T) {
	projectRoot := t.TempDir()
	writeOneshotRunManifest(t, projectRoot, "run-1", oneshot.Manifest{RunID: "run-1", Slug: "build-parser", Task: "Build parser"})
	stubOneshotResolveRuntime(t, projectRoot)
	spy := &oneshotResolveSpy{manifest: oneshot.Manifest{
		RunID:         "run-1",
		Slug:          "build-parser",
		Task:          "Build parser",
		CloseoutState: "retry_authorized",
	}}
	spy.install(t)

	stdout, stderr, err := executeOneshotArgs(t, "oneshot", "--resolve", "run-1", "--retry")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(spy.calls) != 1 {
		t.Fatalf("resolve calls = %d, want 1", len(spy.calls))
	}
	call := spy.calls[0]
	if got, want := call.resolution.Kind, oneshot.ResolutionRetry; got != want {
		t.Errorf("resolution kind = %q, want %q", got, want)
	}
	if call.resolution.URL != "" {
		t.Errorf("resolution URL = %q, want empty", call.resolution.URL)
	}
	if !strings.Contains(stdout, "closeout: retry_authorized") {
		t.Errorf("stdout missing closeout line:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestOneshotCommandResolveFlagValidation(t *testing.T) {
	const recordedURL = "https://example.com/pr/1"
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "resolve with list",
			args:    []string{"oneshot", "--resolve", "run-1", "--url", recordedURL, "--list"},
			wantErr: "--list cannot be combined with --resolve",
		},
		{
			name:    "resolve with resume",
			args:    []string{"oneshot", "--resolve", "run-1", "--retry", "--resume", "run-2"},
			wantErr: "--resume cannot be combined with --resolve",
		},
		{
			name:    "resolve with task",
			args:    []string{"oneshot", "Build parser", "--resolve", "run-1", "--url", recordedURL},
			wantErr: "--resolve cannot be combined with a task",
		},
		{
			name:    "url without resolve",
			args:    []string{"oneshot", "--url", recordedURL},
			wantErr: "--url requires --resolve",
		},
		{
			name:    "retry without resolve",
			args:    []string{"oneshot", "--retry"},
			wantErr: "--retry requires --resolve",
		},
		{
			name:    "url and retry together",
			args:    []string{"oneshot", "--resolve", "run-1", "--url", recordedURL, "--retry"},
			wantErr: "--url cannot be combined with --retry",
		},
		{
			name:    "resolve without outcome",
			args:    []string{"oneshot", "--resolve", "run-1"},
			wantErr: "--resolve requires exactly one of --url or --retry",
		},
		{
			name:    "retry explicitly false with resolve",
			args:    []string{"oneshot", "--resolve", "run-1", "--retry=false"},
			wantErr: "--resolve requires exactly one of --url or --retry",
		},
		{
			name:    "retry explicitly false without resolve",
			args:    []string{"oneshot", "--retry=false"},
			wantErr: "oneshot requires a task, --resume <id>, --list, or --resolve <id>",
		},
		{
			name:    "url and retry without resolve",
			args:    []string{"oneshot", "--url", recordedURL, "--retry"},
			wantErr: "--url cannot be combined with --retry",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectRoot := t.TempDir()
			built := stubOneshotResolveRuntime(t, projectRoot)
			spy := &oneshotResolveSpy{manifest: oneshot.Manifest{RunID: "run-1", CloseoutState: "created"}}
			spy.install(t)

			_, _, err := executeOneshotArgs(t, tt.args...)
			if err == nil {
				t.Fatalf("Execute() error = nil, want a flag validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Execute() error = %q, want it to contain %q", err, tt.wantErr)
			}
			if len(spy.calls) != 0 {
				t.Errorf("resolve calls = %d, want 0", len(spy.calls))
			}
			if *built {
				t.Error("buildRuntime was called, want validation to reject before any work")
			}
		})
	}
}

func TestOneshotCommandRequiresAMode(t *testing.T) {
	built := stubOneshotResolveRuntime(t, t.TempDir())

	_, _, err := executeOneshotArgs(t, "oneshot")
	if err == nil {
		t.Fatal("Execute() error = nil, want a mode validation error")
	}
	for _, want := range []string{"task", "--resume <id>", "--list", "--resolve <id>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Execute() error = %q, want it to mention %q", err, want)
		}
	}
	if *built {
		t.Error("buildRuntime was called, want validation to reject before any work")
	}
}

func TestOneshotCommandResolvePropagatesError(t *testing.T) {
	projectRoot := t.TempDir()
	writeOneshotRunManifest(t, projectRoot, "run-1", oneshot.Manifest{RunID: "run-1", Slug: "build-parser", Task: "Build parser"})
	stubOneshotResolveRuntime(t, projectRoot)
	sentinel := errors.New(`run run-1 closeout state "created" is not unknown, nothing to resolve`)
	spy := &oneshotResolveSpy{err: sentinel}
	spy.install(t)

	stdout, _, err := executeOneshotArgs(t, "oneshot", "--resolve", "run-1", "--retry")
	if err == nil {
		t.Fatal("Execute() error = nil, want the engine error")
	}
	if !strings.Contains(err.Error(), "resolve oneshot closeout:") {
		t.Errorf("Execute() error = %q, want it to name the resolve step", err)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Execute() error = %v, want it to wrap %v", err, sentinel)
	}
	if len(spy.calls) != 1 {
		t.Errorf("resolve calls = %d, want 1", len(spy.calls))
	}
	if strings.Contains(stdout, "closeout:") {
		t.Errorf("stdout = %q, want no closeout line on failure", stdout)
	}
}

func TestOneshotCommandResolveMissingManifest(t *testing.T) {
	tests := []struct {
		name    string
		write   func(*testing.T, string)
		wantErr string
	}{
		{
			name:    "no manifest",
			write:   func(*testing.T, string) {},
			wantErr: "load oneshot manifest:",
		},
		{
			name: "manifest without run id",
			write: func(t *testing.T, projectRoot string) {
				t.Helper()
				writeOneshotRunManifest(t, projectRoot, "run-1", oneshot.Manifest{Slug: "build-parser", Task: "Build parser"})
			},
			wantErr: "load oneshot manifest: run id is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectRoot := t.TempDir()
			tt.write(t, projectRoot)
			stubOneshotResolveRuntime(t, projectRoot)
			spy := &oneshotResolveSpy{manifest: oneshot.Manifest{RunID: "run-1"}}
			spy.install(t)

			_, _, err := executeOneshotArgs(t, "oneshot", "--resolve", "run-1", "--retry")
			if err == nil {
				t.Fatal("Execute() error = nil, want a manifest load error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Execute() error = %q, want it to contain %q", err, tt.wantErr)
			}
			if len(spy.calls) != 0 {
				t.Errorf("resolve calls = %d, want 0", len(spy.calls))
			}
		})
	}
}

func TestNewPhaseRunnerBlocksOnMCPReadiness(t *testing.T) {
	sentinel := errors.New("stop after capture")
	var seen []bool
	orig := buildPhaseRuntime
	buildPhaseRuntime = func(_ context.Context, _ *cobra.Command, flags *cliFlags, _, _, _ string) (cliRuntime, error) {
		seen = append(seen, flags.asyncMCP)
		return cliRuntime{}, sentinel
	}
	t.Cleanup(func() { buildPhaseRuntime = orig })

	shared := &cliFlags{asyncMCP: true}
	factory := phaseRunnerFactory{flags: shared}

	// Flags mutated after the factory exists must not change the outcome.
	for _, asyncAfter := range []bool{true, false} {
		shared.asyncMCP = asyncAfter
		if _, err := factory.NewPhaseRunner(context.Background(), oneshot.PhasePlan, "", nil, config.AdvisorConfig{}); !errors.Is(err, sentinel) {
			t.Fatalf("NewPhaseRunner() error = %v, want sentinel", err)
		}
		if got := shared.asyncMCP; got != asyncAfter {
			t.Errorf("shared flags asyncMCP = %v, want %v (must not be mutated)", got, asyncAfter)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("buildPhaseRuntime calls = %d, want 2", len(seen))
	}
	for i, got := range seen {
		if got {
			t.Errorf("call %d: phase runtime asyncMCP = true, want false", i)
		}
	}
}
