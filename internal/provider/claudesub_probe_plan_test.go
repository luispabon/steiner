//go:build claudesub_probe

package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
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
		invocation("E5 fork resume", fork),
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

// claudeSubProbeExperiments is the probe's experiment table. Each argv is the
// planned main process for that experiment, and each timeout bounds every step
// of that experiment, so the steps' budgets fit inside it.
func claudeSubProbeExperiments(cfg claudeSubProbeConfig) []claudeSubProbeExperiment {
	planned := map[string][]string{}
	for _, invocation := range claudeSubProbePlannedInvocations(cfg.CLIPath, cfg.Model) {
		planned[invocation.Name] = invocation.Argv
	}
	const shortBound = 120 * time.Second
	return []claudeSubProbeExperiment{
		{
			name:    "E1-text-interrupt-reuse",
			summary: "interrupt a streaming text turn, then reuse the same process for a follow-up turn",
			argv:    planned["E1-E4 and E6 sub-agent process"],
			timeout: shortBound,
			run:     claudeSubProbeE1,
		},
		{
			name:    "E2-tool-exec-interrupt",
			summary: "interrupt while a blocking MCP tool call runs, then reuse the process",
			argv:    planned["E1-E4 and E6 sub-agent process"],
			timeout: shortBound,
			run:     claudeSubProbeE2,
		},
		{
			name:    "E3-tool-stream-interrupt",
			summary: "interrupt at the streamed tool_use block start, then reuse the process",
			argv:    planned["E1-E4 and E6 sub-agent process"],
			timeout: shortBound,
			run:     claudeSubProbeE3,
		},
		{
			name:    "E4-interrupt-after-result",
			summary: "interrupt immediately after a result and record whether a stray result arrives before the next turn",
			argv:    planned["E1-E4 and E6 sub-agent process"],
			timeout: shortBound,
			run:     claudeSubProbeE4,
		},
		{
			name:    "E5-resume-at-uuid",
			summary: "interrupt a persisted turn, then resume at an earlier assistant uuid and fork it",
			argv:    planned["E5 parent persisted process"],
			timeout: 240 * time.Second,
			run:     claudeSubProbeE5,
		},
		{
			name:    "E6-queued-after-interrupt",
			summary: "queue a user message immediately after an interrupt and record whether it runs",
			argv:    planned["E1-E4 and E6 sub-agent process"],
			timeout: shortBound,
			run:     claudeSubProbeE6,
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
	fmt.Fprintf(w, "bounds: probe ceiling %s, per-experiment budgets in the experiment list below\n", claudeSubProbeRunTimeout)
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

	fmt.Fprintln(w, "experiments (bound, then summary):")
	for _, experiment := range claudeSubProbeExperiments(cfg) {
		fmt.Fprintf(w, "  %s (%s): %s\n", experiment.name, experiment.timeout, experiment.summary)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "init validation: the first process event must be a system/init event within 20s, reporting only mcp__steiner__probe_wait, the requested model, and no MCP server other than steiner; no prompt is written before it passes")
	fmt.Fprintln(w, "init reporting: the report records the init event's tools, model, capabilities, CLI version, and the names in its plugins, mcp_servers, slash_commands, agents, skills and hook fields")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "exact commands:")
	fmt.Fprintf(w, "  plan: STEINER_CLAUDESUB_PROBE=plan go test -tags claudesub_probe -count=1 -timeout %s -run TestClaudeSubProbe -v ./internal/provider\n", claudeSubProbeRequiredTimeout)
	fmt.Fprintf(w, "  run:  STEINER_CLAUDESUB_PROBE=run %s=%s go test -tags claudesub_probe -count=1 -timeout %s -run TestClaudeSubProbe -v ./internal/provider\n",
		claudeSubProbeAckEnv, claudeSubProbeAckValue, claudeSubProbeRequiredTimeout)
	fmt.Fprintln(w, "run mode spends real subscription requests and writes CLI session files under the CLI config directory")
	fmt.Fprintf(w, "optional environment: %s (default %s), %s, %s=1 for transcript cleanup\n",
		claudeSubProbeModelEnv, claudeSubProbeDefaultModel, claudeSubProbeOutEnv, claudeSubProbeCleanupEnv)
}
