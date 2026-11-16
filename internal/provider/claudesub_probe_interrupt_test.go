//go:build claudesub_probe

package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Synthetic prompts. Every prompt carries a PROBE marker so the report can show
// which turn the model answered without recording any real content.
const (
	claudeSubProbePromptBaseline = "PROBE-T1 Reply with exactly: probe-ok. Do not use tools."
	claudeSubProbePromptLongList = "PROBE-T2 Write a numbered list from 1 to 200, one item per line, in the form item-<n>. Use at least 200 lines and no tools."
	claudeSubProbePromptRecall   = "PROBE-T3 Quote the last user message you saw, then say whether your previous answer was cut off. Answer in under 40 words."
	claudeSubProbePromptToolE2   = "PROBE-T1E2 Call the probe_wait tool exactly once with tag E2. Wait for its result and report it. Do not use any other tool."
	claudeSubProbePromptToolE3   = "PROBE-T1E3 Call the probe_wait tool exactly once with tag E3. Wait for its result and report it. Do not use any other tool."
	claudeSubProbePromptNextTurn = "PROBE-T2 Reply with exactly: after-interrupt. Do not use tools."
)

// claudeSubProbeE1 interrupts a streaming text turn and reuses the process.
func claudeSubProbeE1(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport) error {
	process, err := claudeSubProbeStart(ctx, cfg, exp, "sub-agent", "probe-e1", claudeSubProbeSubAgentSpec(cfg.Model))
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(process)

	if err := process.runTurn(ctx, claudeSubProbePromptBaseline); err != nil {
		return err
	}
	turn, err := process.beginTurn(ctx, claudeSubProbePromptLongList)
	if err != nil {
		return err
	}
	if _, err := process.pumpUntil(ctx, claudeSubProbeReadyTimeout, claudeSubProbeIsTextDelta); err != nil {
		return err
	}
	turn.Interrupted = true
	if err := process.interrupt(ctx); err != nil {
		return err
	}
	if err := process.awaitResult(ctx); err != nil {
		return err
	}
	process.tail(ctx, claudeSubProbeTailWindow)
	return process.runTurn(ctx, claudeSubProbePromptRecall)
}

// claudeSubProbeE2 interrupts while the MCP tool call is parked in the host
// (window B), then releases the handler late.
func claudeSubProbeE2(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport) error {
	process, err := claudeSubProbeStart(ctx, cfg, exp, "sub-agent", "probe-e2", claudeSubProbeSubAgentSpec(cfg.Model))
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(process)

	turn, err := process.beginTurn(ctx, claudeSubProbePromptToolE2)
	if err != nil {
		return err
	}
	entry, err := process.pumpUntil(ctx, claudeSubProbeReadyTimeout, claudeSubProbeIsToolUseStart)
	if err != nil {
		return err
	}
	process.sampleMCP("tool_use observed")
	call := process.sess.beginPendingCall(entry.ToolUseID)
	if call == nil {
		return errors.New("probe MCP call was not registered")
	}
	if err := claudeSubProbeAwaitMCPHandler(ctx, process, entry.ToolUseID, claudeSubProbeReadyTimeout); err != nil {
		return err
	}
	process.sampleMCP("tools/call handler parked")
	turn.Interrupted = true
	if err := process.interrupt(ctx); err != nil {
		return err
	}
	process.sampleMCP("after interrupt")
	if err := process.awaitResult(ctx); err != nil {
		return err
	}
	process.sampleMCP("after interrupted result")
	process.tail(ctx, claudeSubProbeTailWindow)

	// Release the handler late: the MCP request may still be pending, and a
	// resolved result shows whether the CLI is still listening for it.
	process.sess.host.resolve(call, claudeSubToolResult{Text: "probe released the tool call late"})
	process.sampleMCP("after late release")
	process.tail(ctx, claudeSubProbeTailWindow)
	return process.runTurn(ctx, claudeSubProbePromptNextTurn)
}

// claudeSubProbeE3 interrupts at the streamed tool_use block start (window A).
func claudeSubProbeE3(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport) error {
	process, err := claudeSubProbeStart(ctx, cfg, exp, "sub-agent", "probe-e3", claudeSubProbeSubAgentSpec(cfg.Model))
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(process)

	turn, err := process.beginTurn(ctx, claudeSubProbePromptToolE3)
	if err != nil {
		return err
	}
	entry, err := process.pumpUntil(ctx, claudeSubProbeReadyTimeout, claudeSubProbeIsToolUseStart)
	if err != nil {
		return err
	}
	call := process.sess.beginPendingCall(entry.ToolUseID)
	process.sampleMCP("tool_use block start")
	turn.Interrupted = true
	if err := process.interrupt(ctx); err != nil {
		return err
	}
	process.sampleMCP("after interrupt")
	if err := process.awaitResult(ctx); err != nil {
		return err
	}
	process.sampleMCP("after interrupted result")
	process.tail(ctx, claudeSubProbeTailWindow)
	if call != nil {
		process.sess.host.resolve(call, claudeSubToolResult{Text: "probe released the tool call late"})
		process.sampleMCP("after late release")
		process.tail(ctx, claudeSubProbeTailWindow)
	}
	return process.runTurn(ctx, claudeSubProbePromptNextTurn)
}

// claudeSubProbeE4 interrupts immediately after a result and checks that the next
// turn receives exactly one result.
func claudeSubProbeE4(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport) error {
	process, err := claudeSubProbeStart(ctx, cfg, exp, "sub-agent", "probe-e4", claudeSubProbeSubAgentSpec(cfg.Model))
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(process)

	if err := process.runTurn(ctx, "PROBE-T1E4 Reply with exactly: race-a. Do not use tools."); err != nil {
		return err
	}
	if err := process.interrupt(ctx); err != nil {
		return err
	}
	mark := process.timelineLen()
	process.tail(ctx, claudeSubProbeTailWindow)
	if err := process.runTurn(ctx, "PROBE-T2E4 Reply with exactly: race-b. Do not use tools."); err != nil {
		return err
	}
	process.tail(ctx, 5*time.Second)
	results := claudeSubProbeResultsAfter(process, mark)
	exp.Findings = append(exp.Findings, fmt.Sprintf("E4 results from the interrupt onward: %d", len(results)))
	if len(results) != 1 {
		return fmt.Errorf("probe E4 saw %d results from the interrupt onward, want exactly 1", len(results))
	}
	return nil
}

// claudeSubProbeE6 queues a user message immediately after an interrupt, without
// draining first, and records whether it runs as a turn.
func claudeSubProbeE6(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport) error {
	process, err := claudeSubProbeStart(ctx, cfg, exp, "sub-agent", "probe-e6", claudeSubProbeSubAgentSpec(cfg.Model))
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(process)

	turn, err := process.beginTurn(ctx, "PROBE-T1E6 Write a numbered list from 1 to 200, one item per line, in the form item-<n>. Use at least 200 lines and no tools.")
	if err != nil {
		return err
	}
	if _, err := process.pumpUntil(ctx, claudeSubProbeReadyTimeout, claudeSubProbeIsTextDelta); err != nil {
		return err
	}
	turn.Interrupted = true
	if err := process.interrupt(ctx); err != nil {
		return err
	}
	mark := process.timelineLen()
	if err := process.sendPrompt("PROBE-T2E6 Reply with exactly: queued-ok. Do not use tools."); err != nil {
		return err
	}
	if err := process.awaitResult(ctx); err != nil {
		return err
	}
	process.tail(ctx, 10*time.Second)
	results := claudeSubProbeResultsAfter(process, mark)
	exp.Findings = append(exp.Findings, fmt.Sprintf("E6 results after the queued message was written: %d", len(results)))
	exp.Findings = append(exp.Findings, fmt.Sprintf("E6 queued answer observed in streaming or echo text: %v", claudeSubProbeTextAfter(process, mark, "queued-ok")))
	return nil
}

// claudeSubProbeResultsAfter returns the terminal result entries recorded at or
// after index.
func claudeSubProbeResultsAfter(process *claudeSubProbeProcess, index int) []claudeSubProbeEntry {
	process.rec.mu.Lock()
	defer process.rec.mu.Unlock()
	timeline := process.rep.Timeline
	if index < 0 || index > len(timeline) {
		return nil
	}
	var out []claudeSubProbeEntry
	for _, entry := range timeline[index:] {
		if entry.Dir == "in" && entry.Type == "result" {
			out = append(out, entry)
		}
	}
	return out
}

// claudeSubProbeTextAfter reports whether any recorded text at or after index
// contains want, case-insensitively.
func claudeSubProbeTextAfter(process *claudeSubProbeProcess, index int, want string) bool {
	process.rec.mu.Lock()
	defer process.rec.mu.Unlock()
	timeline := process.rep.Timeline
	if index < 0 || index > len(timeline) {
		return false
	}
	want = strings.ToLower(want)
	for _, entry := range timeline[index:] {
		if strings.Contains(strings.ToLower(entry.Text), want) {
			return true
		}
	}
	return false
}
