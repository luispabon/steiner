//go:build claudesub_probe

package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// Experiment-specific tail windows, part of each experiment's step budget.
const (
	claudeSubProbeRaceTail   = 5 * time.Second
	claudeSubProbeQueuedTail = 10 * time.Second
)

// claudeSubProbeToolSpec is the probe's only published tool. Its schema rejects
// unexpected input: a single enumerated tag and no extra properties. The
// production MCP host holds the call open until the probe resolves it, which is
// what makes window B observable.
func claudeSubProbeToolSpec() ToolSpec {
	return ToolSpec{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        claudeSubProbeToolName,
			Description: "Synthetic probe tool with no side effects. Pass the tag from the prompt and nothing else. The call blocks until the probe releases it.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tag": map[string]any{"type": "string", "enum": []string{"E2", "E3"}},
				},
				"required":             []string{"tag"},
				"additionalProperties": false,
			},
		},
	}
}

// claudeSubProbeSubAgentSpec is the non-persisted spec a sub-agent call uses:
// claudeSubArgs adds --no-session-persistence.
func claudeSubProbeSubAgentSpec(model string) claudeSubStartSpec {
	return claudeSubStartSpec{
		SystemPrompt: claudeSubProbeSystemPrompt,
		Tools:        []ToolSpec{claudeSubProbeToolSpec()},
		Model:        model,
		Persist:      false,
	}
}

// claudeSubProbeParentSpec is the persisted spec a parent call uses.
func claudeSubProbeParentSpec(model string) claudeSubStartSpec {
	spec := claudeSubProbeSubAgentSpec(model)
	spec.Persist = true
	return spec
}

// claudeSubProbePlannedArgv is one planned invocation: a label, the argv the
// probe would use including argv[0], and the reported switches it carries.
type claudeSubProbePlannedArgv struct {
	Name  string   `json:"name"`
	Argv  []string `json:"argv"`
	Flags []string `json:"argv_switches"`
}

// claudeSubProbePlannedInvocations builds every argv the probe uses by calling
// the production claudeSubArgs with the spawn options the probe's specs produce.
// The session-private system prompt and MCP config paths are placeholders: the
// session directory does not exist until the process starts.
func claudeSubProbePlannedInvocations(cliPath, model string) []claudeSubProbePlannedArgv {
	if cliPath == "" {
		cliPath = "claude"
	}
	subAgent := claudeSubProbeSpawnOptions(model, false, claudeSubResume{})
	parent := claudeSubProbeSpawnOptions(model, true, claudeSubResume{})
	resume := claudeSubProbeSpawnOptions(model, true, claudeSubResume{SessionID: "<session-id>", AtUUID: "<assistant-uuid>"})
	fork := claudeSubProbeSpawnOptions(model, true, claudeSubResume{SessionID: "<session-id>", AtUUID: "<assistant-uuid>", Fork: true})
	invocation := func(name string, opts claudeSubSpawnOptions) claudeSubProbePlannedArgv {
		args := claudeSubArgs(opts)
		return claudeSubProbePlannedArgv{
			Name:  name,
			Argv:  append([]string{cliPath}, args...),
			Flags: claudeSubProbeFlagSummary(claudeSubProbeArgvFlags(args)),
		}
	}
	return []claudeSubProbePlannedArgv{
		invocation("E1-E4 and E6 sub-agent process", subAgent),
		invocation("E5 parent persisted process", parent),
		invocation("E5 resume at an assistant uuid", resume),
		invocation("E5 and E7 fork resume", fork),
	}
}

// claudeSubProbeSpawnOptions mirrors the spawn options startClaudeSubSession
// builds for a probe spec with no effort request.
func claudeSubProbeSpawnOptions(model string, persist bool, resume claudeSubResume) claudeSubSpawnOptions {
	opts := claudeSubSpawnOptions{
		Model:            model,
		SystemPromptFile: "<session-dir>/system.md",
		MCPConfigFile:    "<session-dir>/mcp.json",
		Persist:          persist,
		Resume:           resume,
	}
	opts.Effort, opts.ThinkingDisabled = claudeSubEffort("")
	return opts
}

// claudeSubProbeExperiments is the probe's experiment table. Each steps plan is
// both the declaration the bound is checked against and the waits the body
// spends in order, so the two cannot drift: waits are consumed by the body and
// extra holds the budgets no body call takes (the interrupt control settle and
// a child process's own init gate).
func claudeSubProbeExperiments(cfg claudeSubProbeConfig) []claudeSubProbeExperiment {
	planned := map[string][]string{}
	for _, invocation := range claudeSubProbePlannedInvocations(cfg.CLIPath, cfg.Model) {
		planned[invocation.Name] = invocation.Argv
	}
	subAgent := planned["E1-E4 and E6 sub-agent process"]
	parent := planned["E5 parent persisted process"]
	crashFork := planned["E5 and E7 fork resume"]
	crashPlan := claudeSubProbeE7Steps()
	steps := func(waits, extra []time.Duration) *claudeSubProbeSteps {
		return &claudeSubProbeSteps{gate: claudeSubProbeInitWorstCase, waits: waits, extra: extra}
	}
	return []claudeSubProbeExperiment{
		{
			name:    "E1-text-interrupt-reuse",
			summary: "interrupt a streaming text turn, then reuse the same process for a follow-up turn",
			argv:    subAgent,
			timeout: 180 * time.Second,
			steps: steps(
				[]time.Duration{
					claudeSubProbeResultTimeout, claudeSubProbeReadyTimeout, claudeSubProbeResultTimeout,
					claudeSubProbeTailWindow, claudeSubProbeResultTimeout,
				},
				[]time.Duration{claudeSubProbeInterruptAck},
			),
			run: claudeSubProbeE1,
		},
		{
			name:    "E2-tool-exec-interrupt",
			summary: "interrupt while a blocking MCP tool call runs, then reuse the process",
			argv:    subAgent,
			timeout: 190 * time.Second,
			steps: steps(
				[]time.Duration{
					claudeSubProbeReadyTimeout, claudeSubProbeReadyTimeout, claudeSubProbeResultTimeout,
					claudeSubProbeTailWindow, claudeSubProbeTailWindow, claudeSubProbeResultTimeout,
					claudeSubProbeTailWindow,
				},
				[]time.Duration{claudeSubProbeInterruptAck},
			),
			run: claudeSubProbeE2,
		},
		{
			name:    "E3-tool-stream-interrupt",
			summary: "interrupt at the streamed tool_use block start, then reuse the process",
			argv:    subAgent,
			timeout: 170 * time.Second,
			steps: steps(
				[]time.Duration{
					claudeSubProbeReadyTimeout, claudeSubProbeResultTimeout, claudeSubProbeTailWindow,
					claudeSubProbeTailWindow, claudeSubProbeResultTimeout, claudeSubProbeTailWindow,
				},
				[]time.Duration{claudeSubProbeInterruptAck},
			),
			run: claudeSubProbeE3,
		},
		{
			name:    "E4-interrupt-after-result",
			summary: "interrupt immediately after a result and record whether a stray result arrives before the next turn",
			argv:    subAgent,
			timeout: 150 * time.Second,
			steps: steps(
				[]time.Duration{
					claudeSubProbeResultTimeout, claudeSubProbeTailWindow, claudeSubProbeResultTimeout,
					claudeSubProbeRaceTail,
				},
				[]time.Duration{claudeSubProbeInterruptAck},
			),
			run: claudeSubProbeE4,
		},
		{
			name:    "E5-resume-at-uuid",
			summary: "interrupt a persisted turn, then resume at an earlier assistant uuid and fork it",
			argv:    parent,
			timeout: 330 * time.Second,
			steps: steps(
				[]time.Duration{
					claudeSubProbeResultTimeout, claudeSubProbeResultTimeout, claudeSubProbeReadyTimeout,
					claudeSubProbeResultTimeout, claudeSubProbeTailWindow, claudeSubProbeResultTimeout,
					claudeSubProbeResultTimeout,
				},
				[]time.Duration{claudeSubProbeInterruptAck, claudeSubProbeInitWorstCase, claudeSubProbeInitWorstCase},
			),
			run: claudeSubProbeE5,
		},
		{
			name:    "E6-queued-after-interrupt",
			summary: "queue a user message immediately after an interrupt and record whether it runs",
			argv:    subAgent,
			timeout: 190 * time.Second,
			steps: steps(
				[]time.Duration{
					claudeSubProbeReadyTimeout, claudeSubProbeReadyTimeout, claudeSubProbeResultTimeout,
					claudeSubProbeResultTimeout, claudeSubProbeQueuedTail,
				},
				[]time.Duration{claudeSubProbeInterruptAck},
			),
			run: claudeSubProbeE6,
		},
		{
			name:    "E7-crash-resume-continue",
			summary: "SIGKILL a persisted turn mid-list, then resume at the earlier assistant uuid, fork it, and measure how the continuation joins the quoted partial",
			argv:    crashFork,
			timeout: crashPlan.plan(),
			steps:   crashPlan,
			run:     claudeSubProbeE7,
		},
	}
}

// claudeSubProbePrintPlan reports everything a run would do without starting a
// process: the located CLI, the argv built by claudeSubArgs and the switches it
// carries, the child environment shape built by claudeSubSessionEnv, the
// directories, the expected session store, the model, the bounds, and the exact
// commands.
func claudeSubProbePrintPlan(w io.Writer, cfg claudeSubProbeConfig) {
	fmt.Fprintln(w, "claude subscription live probe: plan")
	fmt.Fprintln(w, "plan mode starts no process, makes no request, and creates no directory")
	fmt.Fprintln(w)
	cliPath := cfg.CLIPath
	if cliPath == "" {
		cliPath = "<claude not found on PATH>"
	}
	fmt.Fprintf(w, "claude CLI on PATH (looked up, not executed): %s\n", cliPath)
	fmt.Fprintf(w, "model: %s\n", cfg.Model)
	fmt.Fprintf(w, "probe root (would be created, 0o700, kept after the run): %s\n", cfg.Root)
	fmt.Fprintf(w, "working directory given to the CLI: %s\n", cfg.WorkDir)
	fmt.Fprintf(w, "report path (0o600): %s\n", cfg.ReportPath)
	fmt.Fprintf(w, "raw stdout capture (always inside the probe root, any inherited STEINER_CLAUDESUB_RECORD is overridden): %s\n", cfg.RecorderDir)
	configDir := claudeSubProbeConfigDir()
	fmt.Fprintf(w, "CLI session store: %s\n", configDir)
	fmt.Fprintf(w, "CLI project directory for this cwd: %s\n", claudeSubProbeProjectDir(configDir, cfg.WorkDir))
	fmt.Fprintf(w, "remove the recorded session transcripts afterwards: %v (set %s=1 to enable)\n", cfg.Cleanup, claudeSubProbeCleanupEnv)
	fmt.Fprintf(w, "bounds: probe ceiling %s, per-experiment bounds listed below\n", claudeSubProbeRunTimeout)
	fmt.Fprintln(w)

	env := claudeSubProbeEnvSummary(claudeSubSessionEnv(os.Environ(), claudeSubProbeToolOutputMaxBytes))
	fmt.Fprintf(w, "child environment from claudeSubSessionEnv(os.Environ(), %d): %d keys, values redacted\n", claudeSubProbeToolOutputMaxBytes, len(env.Keys))
	for _, key := range env.Keys {
		fmt.Fprintf(w, "  %s=<redacted>\n", key)
	}
	fmt.Fprintln(w, "child environment values for the non-secret probe keys:")
	for _, key := range claudeSubProbeOpenEnvKeys {
		fmt.Fprintf(w, "  %s=%s\n", key, env.Values[key])
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "argv built by claudeSubArgs (argv[0] is the CLI path), with the load-deciding switches:")
	for _, invocation := range claudeSubProbePlannedInvocations(cfg.CLIPath, cfg.Model) {
		encoded, err := json.Marshal(invocation.Argv)
		if err != nil {
			fmt.Fprintf(w, "  %s: <encode error: %v>\n", invocation.Name, err)
			continue
		}
		fmt.Fprintf(w, "  %s:\n    %s\n    switches: %v\n", invocation.Name, encoded, invocation.Flags)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "experiments (bound, worst-case step plan, then summary):")
	for _, experiment := range claudeSubProbeExperiments(cfg) {
		fmt.Fprintf(w, "  %s (bound %s, steps %s): %s\n", experiment.name, experiment.timeout, experiment.steps.plan(), experiment.summary)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "init handling: the CLI is given 10s to report system/init before input. Without it the probe sends one gating turn and rejects any MCP tool call with an error result until that turn's result arrives; init must validate within 20s of it or the experiment fails, and no other prompt is written until then")
	fmt.Fprintln(w, "gating turn: a fixed warmup prompt for the non-persisted specs, and the experiment's own first tool-free prompt for the persisted and resumed ones (T1 for the parent, T4 for E5's resume and fork processes, the continuation line for E7's resumed fork), so no extra turn enters a persisted transcript")
	fmt.Fprintln(w, "init validation: exactly mcp__steiner__probe_wait, the requested model, and no MCP server other than steiner")
	fmt.Fprintln(w, "init reporting: the report records the init event's tools, model, capabilities, CLI version, and the names in its plugins, mcp_servers, slash_commands, agents, skills and hook fields, plus which init path happened")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "exact commands:")
	fmt.Fprintf(w, "  plan: STEINER_CLAUDESUB_PROBE=plan go test -tags claudesub_probe -count=1 -timeout %s -run TestClaudeSubProbe -v ./internal/provider\n", claudeSubProbeRequiredTimeout)
	fmt.Fprintf(w, "  run:  STEINER_CLAUDESUB_PROBE=run %s=%s go test -tags claudesub_probe -count=1 -timeout %s -run TestClaudeSubProbe -v ./internal/provider\n",
		claudeSubProbeAckEnv, claudeSubProbeAckValue, claudeSubProbeRequiredTimeout)
	fmt.Fprintln(w, "run mode spends real subscription requests and writes CLI session files under the CLI config directory")
	fmt.Fprintf(w, "optional environment: %s (default %s), %s, %s=1 for transcript cleanup\n",
		claudeSubProbeModelEnv, claudeSubProbeDefaultModel, claudeSubProbeOutEnv, claudeSubProbeCleanupEnv)
}
