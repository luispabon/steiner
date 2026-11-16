//go:build claudesub_probe

package provider

// This file is the opt-in live probe for the claude CLI's interrupt and resume
// semantics (issue 1011). It never runs in a normal `go test ./internal/provider`:
// the claudesub_probe build tag keeps it out of the package, and the test skips
// unless STEINER_CLAUDESUB_PROBE selects plan or run.
//
// Plan mode prints what a run would do and starts no process:
//
//	STEINER_CLAUDESUB_PROBE=plan go test -tags claudesub_probe -count=1 -timeout 30m -run TestClaudeSubProbe -v ./internal/provider
//
// Run mode makes real subscription requests, so it needs an explicit ack:
//
//	STEINER_CLAUDESUB_PROBE=run STEINER_CLAUDESUB_PROBE_ACK=i-accept-live-requests \
//	  go test -tags claudesub_probe -count=1 -timeout 30m -run TestClaudeSubProbe -v ./internal/provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Probe gating, defaults and bounds. The probe spends real subscription quota
// only in run mode, and run mode is the only mode that starts a CLI process.
const (
	claudeSubProbeModeEnv            = "STEINER_CLAUDESUB_PROBE"
	claudeSubProbeAckEnv             = "STEINER_CLAUDESUB_PROBE_ACK"
	claudeSubProbeAckValue           = "i-accept-live-requests"
	claudeSubProbeModelEnv           = "STEINER_CLAUDESUB_PROBE_MODEL"
	claudeSubProbeOutEnv             = "STEINER_CLAUDESUB_PROBE_OUT"
	claudeSubProbeCleanupEnv         = "STEINER_CLAUDESUB_PROBE_CLEANUP"
	claudeSubProbeOnlyEnv            = "STEINER_CLAUDESUB_PROBE_ONLY"
	claudeSubProbeE7TrialsEnv        = "STEINER_CLAUDESUB_PROBE_E7_TRIALS"
	claudeSubProbeModePlan           = "plan"
	claudeSubProbeModeRun            = "run"
	claudeSubProbeDefaultModel       = "haiku"
	claudeSubProbeToolName           = "probe_wait"
	claudeSubProbeToolOutputMaxBytes = 65536 // internal/config default, so the child env matches production
	claudeSubProbeExperimentTimeout  = 150 * time.Second
	claudeSubProbeRunTimeout         = 25 * time.Minute
	claudeSubProbeRequiredTimeout    = "30m"
	claudeSubProbeLocateTimeout      = 30 * time.Second
	claudeSubProbeStatusCompleted    = "completed"
	claudeSubProbeStatusFailed       = "failed"
)

// claudeSubProbeSystemPrompt is the only system prompt a probe process gets.
const claudeSubProbeSystemPrompt = "You are a probe target. Follow the user's instruction exactly and keep answers short unless you are asked for a long list."

// claudeSubProbeSteps is one experiment's declared step budget. The experiment
// body spends it in order through next, and the experiment table sums the same
// declaration against the bound, so a bound cannot drift from the waits it
// covers.
type claudeSubProbeSteps struct {
	gate  time.Duration
	waits []time.Duration
	extra []time.Duration
	short bool
	// trials is how many trials a repeated experiment (E7) runs: its declared
	// waits are the per-trial plan repeated trials times.
	trials int
}

// next spends the experiment's next declared wait. A body that asks for more
// waits than the plan declares marks the plan short, which fails the experiment.
func (s *claudeSubProbeSteps) next() time.Duration {
	if len(s.waits) == 0 {
		s.short = true
		return 0
	}
	wait := s.waits[0]
	s.waits = s.waits[1:]
	return wait
}

// copy returns an independent consumer of the same plan, so a run leaves the
// declaration intact for the bound check.
func (s *claudeSubProbeSteps) copy() *claudeSubProbeSteps {
	return &claudeSubProbeSteps{gate: s.gate, waits: s.waits, extra: s.extra, trials: s.trials}
}

// plan reports the worst case the plan declares: the init gate, every declared
// extra budget such as the interrupt control settle or a child process's gate,
// and every wait the body consumes.
func (s *claudeSubProbeSteps) plan() time.Duration {
	total := s.gate
	for _, wait := range s.waits {
		total += wait
	}
	for _, extra := range s.extra {
		total += extra
	}
	return total
}

// claudeSubProbeConfig is the settled configuration of one probe invocation.
type claudeSubProbeConfig struct {
	Model       string
	Root        string
	WorkDir     string
	ReportDir   string
	ReportPath  string
	RecorderDir string
	CLIPath     string
	Cleanup     bool
	// Only is the raw STEINER_CLAUDESUB_PROBE_ONLY value: a comma-separated list
	// of experiment ID prefixes. Empty selects every experiment.
	Only string
	// E7Trials is the raw STEINER_CLAUDESUB_PROBE_E7_TRIALS value: E7's trial
	// count. Empty derives it from the selection and the probe's ceiling.
	E7Trials string
}

// claudeSubProbeCLI is the located CLI the run used.
type claudeSubProbeCLI struct {
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
}

// claudeSubProbeEnv is the reportable shape of the child environment: every key,
// the values of the probe-relevant non-secret keys, and nothing else.
type claudeSubProbeEnv struct {
	Keys   []string          `json:"keys"`
	Values map[string]string `json:"non_secret_values"`
}

// claudeSubProbeResume is the resume target a process started with.
type claudeSubProbeResume struct {
	SessionID string `json:"session_id,omitempty"`
	AtUUID    string `json:"at_uuid,omitempty"`
	Fork      bool   `json:"fork,omitempty"`
}

// claudeSubProbeExit is the exit evidence for one process. Status is meaningful
// only when StatusKnown is set: the child is reaped by the production teardown,
// and a teardown that ran out of deadline leaves the status unknown rather than
// reporting a false 0.
type claudeSubProbeExit struct {
	AtMS                 int64  `json:"at_ms"`
	Status               int    `json:"status"`
	StatusKnown          bool   `json:"status_known"`
	WaitErr              string `json:"wait_error,omitempty"`
	ProcErr              string `json:"proc_err,omitempty"`
	ConnErr              string `json:"conn_err,omitempty"`
	PoolCloseErr         string `json:"pool_close_err,omitempty"`
	Unobserved           int    `json:"unobserved_events,omitempty"`
	ExitedBeforeTeardown bool   `json:"exited_before_teardown,omitempty"`
}

// claudeSubProbeProcessReport is one CLI process: the argv and environment shape
// it ran with, its whole recorded exchange, and its exit evidence.
type claudeSubProbeProcessReport struct {
	Name                string                     `json:"name"`
	Key                 string                     `json:"key"`
	Persist             bool                       `json:"persist"`
	Resume              claudeSubProbeResume       `json:"resume,omitempty"`
	Dir                 string                     `json:"dir,omitempty"`
	Argv                []string                   `json:"argv,omitempty"`
	Flags               []string                   `json:"argv_switches,omitempty"`
	Env                 *claudeSubProbeEnv         `json:"env,omitempty"`
	StartError          string                     `json:"start_error,omitempty"`
	InitPath            string                     `json:"init_path,omitempty"`
	WarmupRejectedCalls []string                   `json:"warmup_rejected_calls,omitempty"`
	Init                *claudeSubProbeInit        `json:"init,omitempty"`
	SessionID           string                     `json:"session_id,omitempty"`
	Turns               []*claudeSubProbeTurn      `json:"turns,omitempty"`
	Timeline            []claudeSubProbeEntry      `json:"timeline,omitempty"`
	TimelineTruncated   bool                       `json:"timeline_truncated,omitempty"`
	Dropped             int                        `json:"dropped_events,omitempty"`
	MCPCalls            []claudeSubProbeCallSample `json:"mcp_calls,omitempty"`
	Exit                *claudeSubProbeExit        `json:"exit,omitempty"`
	// ExpectedCrash records that the experiment itself killed this process, so its
	// exit before teardown and the teardown result that follows a crash are the
	// measurement rather than failures. E7 sets it.
	ExpectedCrash bool     `json:"expected_crash,omitempty"`
	TeardownErr   string   `json:"teardown_err,omitempty"`
	Findings      []string `json:"findings,omitempty"`
}

// claudeSubProbeCleanupReport states what the probe deleted. Only transcript
// files of sessions the probe itself recorded are ever candidates.
type claudeSubProbeCleanupReport struct {
	Enabled bool     `json:"enabled"`
	Removed []string `json:"removed,omitempty"`
	Absent  []string `json:"absent,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
	Errors  []string `json:"errors,omitempty"`
	Note    string   `json:"note"`
}

// claudeSubProbeExperimentReport is one experiment's outcome and evidence.
type claudeSubProbeExperimentReport struct {
	Name        string                         `json:"name"`
	Summary     string                         `json:"summary"`
	Status      string                         `json:"status"`
	Error       string                         `json:"error,omitempty"`
	PlannedArgv []string                       `json:"planned_argv,omitempty"`
	Processes   []*claudeSubProbeProcessReport `json:"processes,omitempty"`
	Transcripts []claudeSubProbeTranscript     `json:"transcripts,omitempty"`
	Findings    []string                       `json:"findings,omitempty"`
}

// claudeSubProbeReport is the whole report one run writes.
type claudeSubProbeReport struct {
	Mode         string                            `json:"mode"`
	StartedAt    string                            `json:"started_at"`
	DurationMS   int64                             `json:"duration_ms"`
	Model        string                            `json:"model"`
	CLI          claudeSubProbeCLI                 `json:"cli"`
	Root         string                            `json:"root"`
	WorkDir      string                            `json:"work_dir"`
	SessionStore string                            `json:"session_store"`
	RecorderDir  string                            `json:"recorder_dir,omitempty"`
	ReportPath   string                            `json:"report_path"`
	Note         string                            `json:"note"`
	Env          *claudeSubProbeEnv                `json:"env"`
	Experiments  []*claudeSubProbeExperimentReport `json:"experiments"`
	Cleanup      claudeSubProbeCleanupReport       `json:"cleanup"`
}

// claudeSubProbeExperiment is one experiment of the probe. timeout is the bound
// every step of the experiment runs inside, and steps is the same declaration
// the body consumes.
type claudeSubProbeExperiment struct {
	name    string
	summary string
	argv    []string
	timeout time.Duration
	steps   *claudeSubProbeSteps
	run     func(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport, steps *claudeSubProbeSteps) error
}

// TestClaudeSubProbe is the probe entry point. It skips unless the mode
// environment variable selects plan or run.
func TestClaudeSubProbe(t *testing.T) {
	mode := strings.TrimSpace(os.Getenv(claudeSubProbeModeEnv))
	switch mode {
	case "":
		t.Skip("live probe is opt-in: set " + claudeSubProbeModeEnv + "=plan to print the plan, or =run with " + claudeSubProbeAckEnv)
	case claudeSubProbeModePlan:
		cfg := claudeSubProbePlanConfig()
		experiments, err := claudeSubProbeExperiments(cfg)
		if err != nil {
			t.Fatalf("claude subscription probe plan: %v", err)
		}
		claudeSubProbePrintPlan(os.Stdout, cfg, experiments)
	case claudeSubProbeModeRun:
		if got := os.Getenv(claudeSubProbeAckEnv); got != claudeSubProbeAckValue {
			t.Fatalf("%s=run makes real requests to the signed-in Claude subscription and writes CLI session files under the CLI config directory, so it needs an explicit acknowledgement: set %s=%s",
				claudeSubProbeModeEnv, claudeSubProbeAckEnv, claudeSubProbeAckValue)
		}
		claudeSubProbeRun(t)
	default:
		t.Fatalf("%s=%q is not a probe mode: use %s or %s", claudeSubProbeModeEnv, mode, claudeSubProbeModePlan, claudeSubProbeModeRun)
	}
}

// claudeSubProbePlanConfig resolves the configuration plan mode describes. It
// reads the environment and looks the CLI up on PATH; it never executes
// anything and never creates a directory.
func claudeSubProbePlanConfig() claudeSubProbeConfig {
	root := filepath.Join(os.TempDir(), "steiner-claudesub-probe-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	cfg := claudeSubProbeConfig{
		Model:       strings.TrimSpace(os.Getenv(claudeSubProbeModelEnv)),
		Root:        root,
		WorkDir:     filepath.Join(root, "work"),
		ReportDir:   root,
		ReportPath:  filepath.Join(root, "report.json"),
		RecorderDir: filepath.Join(root, "recordings"),
		Cleanup:     strings.TrimSpace(os.Getenv(claudeSubProbeCleanupEnv)) == "1",
		Only:        os.Getenv(claudeSubProbeOnlyEnv),
		E7Trials:    os.Getenv(claudeSubProbeE7TrialsEnv),
	}
	if cfg.Model == "" {
		cfg.Model = claudeSubProbeDefaultModel
	}
	if out := strings.TrimSpace(os.Getenv(claudeSubProbeOutEnv)); out != "" {
		cfg.ReportPath = out
		cfg.ReportDir = filepath.Dir(out)
	}
	if path, err := exec.LookPath("claude"); err == nil {
		cfg.CLIPath = path
	}
	return cfg
}

// claudeSubProbePrepareRun materialises the run's private directories. Raw stdout
// capture is always inside that root, so an inherited STEINER_CLAUDESUB_RECORD
// cannot point the probe's recording somewhere else. The root is kept after the
// run so the report and the recordings stay readable.
func claudeSubProbePrepareRun() (claudeSubProbeConfig, error) {
	cfg := claudeSubProbePlanConfig()
	root, err := os.MkdirTemp("", "steiner-claudesub-probe-*")
	if err != nil {
		return cfg, fmt.Errorf("create probe directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return cfg, fmt.Errorf("secure probe directory: %w", err)
	}
	cfg.Root = root
	cfg.WorkDir = filepath.Join(root, "work")
	if err := os.Mkdir(cfg.WorkDir, 0o700); err != nil {
		return cfg, fmt.Errorf("create probe working directory: %w", err)
	}
	if strings.TrimSpace(os.Getenv(claudeSubProbeOutEnv)) == "" {
		cfg.ReportPath = filepath.Join(root, "report.json")
		cfg.ReportDir = root
	} else {
		cfg.ReportDir = filepath.Dir(cfg.ReportPath)
	}
	cfg.RecorderDir = filepath.Join(root, "recordings")
	if err := os.Setenv("STEINER_CLAUDESUB_RECORD", cfg.RecorderDir); err != nil {
		return cfg, fmt.Errorf("enable probe stdout capture: %w", err)
	}
	return cfg, nil
}

// claudeSubProbeRun runs every experiment, fails soft on each one, writes the
// report, and optionally removes the transcript files it recorded.
func claudeSubProbeRun(t *testing.T) {
	cfg, err := claudeSubProbePrepareRun()
	if err != nil {
		t.Fatalf("prepare claude subscription probe: %v", err)
	}
	// The selection is resolved before the CLI is located, so an unknown prefix or
	// an impossible E7 trial count fails before anything is spawned.
	experiments, err := claudeSubProbeExperiments(cfg)
	if err != nil {
		t.Fatalf("select claude subscription probe experiments: %v", err)
	}
	if deadline, ok := t.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < claudeSubProbeRunTimeout+time.Minute {
			t.Logf("probe: the test deadline is %s away, below the probe ceiling %s; run with -timeout %s", remaining.Round(time.Second), claudeSubProbeRunTimeout, claudeSubProbeRequiredTimeout)
		}
	}

	locateCtx, cancelLocate := context.WithTimeout(context.Background(), claudeSubProbeLocateTimeout)
	cli, err := locateClaudeSubCLI(locateCtx, exec.LookPath, claudeSubExecRunner)
	cancelLocate()
	if err != nil {
		t.Fatalf("locate claude CLI for the live probe: %v", err)
	}
	t.Logf("probe: claude CLI %s at %s", cli.Version, cli.Path)
	t.Logf("probe: working directory %s", cfg.WorkDir)
	t.Logf("probe: report %s", cfg.ReportPath)
	t.Logf("probe: raw stdout capture %s", cfg.RecorderDir)

	report := &claudeSubProbeReport{
		Mode:         claudeSubProbeModeRun,
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
		Model:        cfg.Model,
		CLI:          claudeSubProbeCLI{Path: cli.Path, Version: cli.Version},
		Root:         cfg.Root,
		WorkDir:      cfg.WorkDir,
		SessionStore: claudeSubProbeConfigDir(),
		RecorderDir:  cfg.RecorderDir,
		ReportPath:   cfg.ReportPath,
		Note:         "the private temporary root is kept after the run; it holds the report and the raw stdout recordings",
		Env:          claudeSubProbeEnvSummary(claudeSubSessionEnv(os.Environ(), claudeSubProbeToolOutputMaxBytes)),
	}
	started := time.Now()

	overall, cancelOverall := context.WithTimeout(context.Background(), claudeSubProbeRunTimeout)
	defer cancelOverall()
	for _, experiment := range experiments {
		exp := &claudeSubProbeExperimentReport{Name: experiment.name, Summary: experiment.summary, PlannedArgv: experiment.argv}
		report.Experiments = append(report.Experiments, exp)
		timeout := experiment.timeout
		if timeout <= 0 {
			timeout = claudeSubProbeExperimentTimeout
		}
		steps := experiment.steps.copy()
		expCtx, cancelExp := context.WithTimeout(overall, timeout)
		err := experiment.run(expCtx, cfg, exp, steps)
		cancelExp()
		if steps.short {
			err = errors.Join(err, errors.New("probe step plan ran out before the experiment finished"))
		}
		// Findings and failure checks run after the experiment returned, so every
		// process it started has already recorded its exit evidence.
		for _, process := range exp.Processes {
			exp.Findings = append(exp.Findings, claudeSubProbeFindings(process)...)
			err = errors.Join(err, claudeSubProbeProcessFailure(process))
		}
		if err != nil {
			exp.Status = claudeSubProbeStatusFailed
			exp.Error = claudeSubProbeErrorString(err)
			t.Logf("probe %s failed: %v", exp.Name, err)
			continue
		}
		exp.Status = claudeSubProbeStatusCompleted
		t.Logf("probe %s completed", exp.Name)
	}
	report.DurationMS = time.Since(started).Milliseconds()
	report.Cleanup = claudeSubProbeCleanupSessions(cfg, claudeSubProbeConfigDir(), report)

	if err := claudeSubProbeWriteReport(cfg.ReportPath, report); err != nil {
		t.Fatalf("write claude subscription probe report: %v", err)
	}
	t.Logf("probe report written: %s", cfg.ReportPath)
}

// claudeSubProbeProcessFailure reports the per-process conditions that fail an
// experiment even when its body returned no error: an incomplete timeline, an
// exit before the probe began teardown, and an unexpected teardown result. A
// non-zero exit status after the probe's own stdin close is evidence, not a
// failure, and a process the experiment killed itself (ExpectedCrash) is expected
// to show both the early exit and the crash-derived teardown result.
func claudeSubProbeProcessFailure(rep *claudeSubProbeProcessReport) error {
	var errs []error
	if rep.TimelineTruncated {
		errs = append(errs, fmt.Errorf("probe process %s timeline hit the %d entry cap, so its exchange is incomplete", rep.Name, claudeSubProbeTimelineMax))
	}
	if rep.Exit != nil && rep.Exit.ExitedBeforeTeardown && !rep.ExpectedCrash {
		errs = append(errs, fmt.Errorf("probe process %s exited before teardown began (status %d, wait error %q)", rep.Name, rep.Exit.Status, rep.Exit.WaitErr))
	}
	if rep.TeardownErr != "" && !rep.ExpectedCrash && !claudeSubProbeExpectedTeardownError(rep.TeardownErr) {
		errs = append(errs, fmt.Errorf("probe process %s teardown reported %s", rep.Name, rep.TeardownErr))
	}
	return errors.Join(errs...)
}

// claudeSubProbeExpectedTeardownError reports the teardown results the probe
// treats as expected: a bounded teardown that ran out of deadline while its
// cleanup kept going. Anything else is a failure.
func claudeSubProbeExpectedTeardownError(message string) bool {
	return strings.Contains(message, "teardown did not complete") || strings.Contains(message, "cleanup did not complete")
}

// claudeSubProbeWriteReport writes the report as one JSON file with mode 0o600.
func claudeSubProbeWriteReport(path string, report *claudeSubProbeReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode probe report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create probe report directory: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write probe report: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure probe report: %w", err)
	}
	return nil
}

// claudeSubProbeStart starts one process for exp and registers its report.
// gatingPrompt is the tool-free prompt the init gate uses when init does not
// arrive before input: empty means the probe's own fixed warmup prompt, which
// only the ephemeral (non-persisted) specs use.
func claudeSubProbeStart(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport, name, key string, spec claudeSubStartSpec, gatingPrompt string) (*claudeSubProbeProcess, error) {
	return claudeSubProbeStartGated(ctx, cfg, exp, name, key, spec, gatingPrompt, nil)
}

// claudeSubProbeStartGated is claudeSubProbeStart for a gating turn whose wire
// line is not the single text block gatingPrompt would produce: E7's continuation
// line carries the original prompt and the note as two text blocks.
func claudeSubProbeStartGated(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport, name, key string, spec claudeSubStartSpec, gatingPrompt string, gatingBlocks []map[string]any) (*claudeSubProbeProcess, error) {
	rep := &claudeSubProbeProcessReport{Name: name, Key: key, Persist: spec.Persist, Resume: claudeSubProbeResumeOf(spec.Resume)}
	exp.Processes = append(exp.Processes, rep)
	process, err := claudeSubProbeStartProcess(ctx, cfg, rep, key, spec, gatingPrompt, gatingBlocks)
	if err != nil {
		rep.StartError = claudeSubProbeErrorString(err)
		return nil, fmt.Errorf("start probe process %s: %w", name, err)
	}
	return process, nil
}

// claudeSubProbeFinish tears a process down. The result is recorded in the
// process report, and the run loop fails the experiment when it is unexpected, so
// the experiment body does not need to act on it here.
func claudeSubProbeFinish(process *claudeSubProbeProcess) {
	_ = process.teardown() // teardown records the result in the process report
}

// claudeSubProbeResumeOf maps the production resume target to its report form.
func claudeSubProbeResumeOf(resume claudeSubResume) claudeSubProbeResume {
	return claudeSubProbeResume{SessionID: resume.SessionID, AtUUID: resume.AtUUID, Fork: resume.Fork}
}
