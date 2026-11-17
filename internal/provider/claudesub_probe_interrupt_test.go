//go:build claudesub_probe

package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Synthetic prompts. Every prompt carries a PROBE marker so the report can show
// which turn the model answered without recording any real content.
const (
	claudeSubProbeLateResultMarker = "PROBE-LATE-RESULT"
	claudeSubProbePromptBaseline   = "PROBE-T1 Reply with exactly: probe-ok. Do not use tools."
	claudeSubProbePromptLongList   = "PROBE-T2 Write a numbered list from 1 to 200, one item per line, in the form item-<n>. Use at least 200 lines and no tools."
	claudeSubProbePromptRecall     = "PROBE-T3 Quote the last user message you saw, then say whether your previous answer was cut off. Answer in under 40 words."
	claudeSubProbePromptToolE2     = "PROBE-T1E2 Call the probe_wait tool exactly once with tag E2. Wait for its result and report it. Do not use any other tool."
	claudeSubProbePromptToolE3     = "PROBE-T1E3 Call the probe_wait tool exactly once with tag E3. Wait for its result and report it. Do not use any other tool."
	claudeSubProbePromptLateResult = "PROBE-T3E2 Did you receive a tool result containing the marker PROBE-LATE-RESULT? Answer yes or no and nothing else."
	claudeSubProbePromptNextTurn   = "PROBE-T2 Reply with exactly: after-interrupt. Do not use tools."
)

// claudeSubProbeE1 interrupts a streaming text turn and reuses the process.
func claudeSubProbeE1(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport, steps *claudeSubProbeSteps) error {
	process, err := claudeSubProbeStart(ctx, cfg, exp, "sub-agent", "probe-e1", claudeSubProbeSubAgentSpec(cfg.Model), "")
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(process)

	if err := process.runTurn(ctx, claudeSubProbePromptBaseline, steps.next()); err != nil {
		return err
	}
	turn, err := process.beginTurn(claudeSubProbePromptLongList)
	if err != nil {
		return err
	}
	if _, err := process.pumpUntil(ctx, steps.next(), "first text delta", claudeSubProbeIsTextDelta); err != nil {
		return err
	}
	turn.Interrupted = true
	if err := process.interrupt(ctx); err != nil {
		return err
	}
	if err := process.awaitInterruptedResult(ctx, steps.next()); err != nil {
		return err
	}
	process.tail(ctx, steps.next())
	return process.runTurn(ctx, claudeSubProbePromptRecall, steps.next())
}

// claudeSubProbeE2 interrupts while the MCP tool call is parked in the host
// (window B), releases the handler late with a marked result, and asks the next
// turn whether that result was seen. The late release only reports what the host
// did with the call; whether the model received it comes from the next turn's
// inbound text.
func claudeSubProbeE2(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport, steps *claudeSubProbeSteps) error {
	process, err := claudeSubProbeStart(ctx, cfg, exp, "sub-agent", "probe-e2", claudeSubProbeSubAgentSpec(cfg.Model), "")
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(process)

	turn, err := process.beginTurn(claudeSubProbePromptToolE2)
	if err != nil {
		return err
	}
	entry, err := process.pumpUntil(ctx, steps.next(), "tool_use", claudeSubProbeIsToolUse)
	if err != nil {
		return err
	}
	exp.Findings = append(exp.Findings, fmt.Sprintf("E2 tool_use observed at %dms: type=%s stream_event=%s tool_use_id=%s", entry.AtMS, entry.Type, entry.StreamEvent, entry.ToolUseID))
	call := process.sess.beginPendingCall(entry.ToolUseID)
	if call == nil {
		return errors.New("probe MCP registration failed for the observed tool_use")
	}
	process.sampleMCP("tool_use observed")
	if err := claudeSubProbeAwaitMCPHandler(ctx, process, entry.ToolUseID, steps.next()); err != nil {
		return err
	}
	process.sampleMCP("tools/call handler parked")
	turn.Interrupted = true
	if err := process.interrupt(ctx); err != nil {
		return err
	}
	process.sampleMCP("after interrupt")
	if err := process.awaitInterruptedResult(ctx, steps.next()); err != nil {
		return err
	}
	process.sampleMCP("after interrupted result")
	process.tail(ctx, steps.next())

	// Release the handler late with a marked result. The host's own state is the
	// evidence here; delivery is checked below from inbound text only.
	process.sess.host.resolve(call, claudeSubToolResult{Text: claudeSubProbeLateResultMarker + " the probe released this tool call late"})
	process.sampleMCP("after late release")
	process.tail(ctx, steps.next())
	exp.Findings = append(exp.Findings, fmt.Sprintf("E2 MCP call state after the late release: %+v", process.hostCallState(entry.ToolUseID)))

	mark := process.timelineLen()
	if err := process.runTurn(ctx, claudeSubProbePromptLateResult, steps.next()); err != nil {
		return err
	}
	process.tail(ctx, steps.next())
	exp.Findings = append(exp.Findings, fmt.Sprintf("E2 inbound text naming %s in the next turn: %v", claudeSubProbeLateResultMarker, process.inboundTextAfter(mark, claudeSubProbeLateResultMarker)))
	return nil
}

// claudeSubProbeE3 interrupts at the streamed tool_use block start (window A).
func claudeSubProbeE3(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport, steps *claudeSubProbeSteps) error {
	process, err := claudeSubProbeStart(ctx, cfg, exp, "sub-agent", "probe-e3", claudeSubProbeSubAgentSpec(cfg.Model), "")
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(process)

	turn, err := process.beginTurn(claudeSubProbePromptToolE3)
	if err != nil {
		return err
	}
	entry, err := process.pumpUntil(ctx, steps.next(), "stream_event content_block_start tool_use", claudeSubProbeIsToolUseStart)
	if err != nil {
		return err
	}
	exp.Findings = append(exp.Findings, fmt.Sprintf("E3 trigger event at %dms: type=%s stream_event=%s content_block=%s tool_use_id=%s", entry.AtMS, entry.Type, entry.StreamEvent, entry.ContentBlockType, entry.ToolUseID))
	call := process.sess.beginPendingCall(entry.ToolUseID)
	if call == nil {
		return errors.New("probe MCP registration failed at the tool_use block start")
	}
	process.sampleMCP("tool_use block start")
	turn.Interrupted = true
	if err := process.interrupt(ctx); err != nil {
		return err
	}
	process.sampleMCP("after interrupt")
	if err := process.awaitInterruptedResult(ctx, steps.next()); err != nil {
		return err
	}
	process.sampleMCP("after interrupted result")
	process.tail(ctx, steps.next())
	process.sess.host.resolve(call, claudeSubToolResult{Text: claudeSubProbeLateResultMarker + " the probe released this tool call late"})
	process.sampleMCP("after late release")
	process.tail(ctx, steps.next())
	exp.Findings = append(exp.Findings, fmt.Sprintf("E3 MCP call state after the late release: %+v", process.hostCallState(entry.ToolUseID)))
	return process.runTurn(ctx, claudeSubProbePromptNextTurn, steps.next())
}

// claudeSubProbeE4 interrupts immediately after a result and records whether a
// stray result arrives. A stray result is the measurement, not a failure.
func claudeSubProbeE4(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport, steps *claudeSubProbeSteps) error {
	process, err := claudeSubProbeStart(ctx, cfg, exp, "sub-agent", "probe-e4", claudeSubProbeSubAgentSpec(cfg.Model), "")
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(process)

	if err := process.runTurn(ctx, "PROBE-T1E4 Reply with exactly: race-a. Do not use tools.", steps.next()); err != nil {
		return err
	}
	mark := process.timelineLen()
	if err := process.interrupt(ctx); err != nil {
		return err
	}
	process.tail(ctx, steps.next())
	if err := process.runTurn(ctx, "PROBE-T2E4 Reply with exactly: race-b. Do not use tools.", steps.next()); err != nil {
		return err
	}
	process.tail(ctx, steps.next())
	results := process.resultsAfter(mark)
	exp.Findings = append(exp.Findings, fmt.Sprintf("E4 results from the interrupt onward: %d", len(results)))
	stray := 0
	for _, result := range results {
		if !containsSubstring(result.Text, "race-b") {
			stray++
		}
	}
	exp.Findings = append(exp.Findings, fmt.Sprintf("E4 results whose text does not carry the second turn's marker: %d", stray))
	return nil
}

// claudeSubProbeE6 queues a user message as soon as the interrupt request is
// written, without waiting for its control response, and records whether the
// queued message runs. The interrupt request is awaited on every return path.
func claudeSubProbeE6(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport, steps *claudeSubProbeSteps) error {
	process, err := claudeSubProbeStart(ctx, cfg, exp, "sub-agent", "probe-e6", claudeSubProbeSubAgentSpec(cfg.Model), "")
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(process)

	turn, err := process.beginTurn("PROBE-T1E6 Write a numbered list from 1 to 200, one item per line, in the form item-<n>. Use at least 200 lines and no tools.")
	if err != nil {
		return err
	}
	if _, err := process.pumpUntil(ctx, steps.next(), "first text delta", claudeSubProbeIsTextDelta); err != nil {
		return err
	}
	turn.Interrupted = true
	interruptDone := make(chan error, 1)
	interruptSettled := false
	since := process.timelineLen()
	go func() { interruptDone <- process.interrupt(ctx) }()
	defer func() {
		if interruptSettled {
			return
		}
		// Every return path waits for the interrupt request to settle, bounded by
		// the production control timeout, so the goroutine never outlives the
		// experiment.
		_ = claudeSubProbeAwaitInterrupt(interruptDone, claudeSubProbeInterruptAck)
	}()
	if err := process.awaitOutboundInterrupt(ctx, since, steps.next()); err != nil {
		return err
	}
	mark := process.timelineLen()
	queuedTurn, err := process.beginTurn("PROBE-T2E6 Reply with exactly: queued-ok. Do not use tools.")
	if err != nil {
		return err
	}
	if err := process.awaitInterruptedResult(ctx, steps.next()); err != nil {
		return err
	}
	if err := process.awaitResult(ctx, steps.next()); err != nil {
		exp.Findings = append(exp.Findings, "E6 the queued message produced no second result: "+claudeSubProbeErrorString(err))
	} else {
		exp.Findings = append(exp.Findings, "E6 the queued message produced a second result")
	}
	process.tail(ctx, steps.next())
	if err := claudeSubProbeAwaitInterrupt(interruptDone, claudeSubProbeInterruptAck); err != nil {
		return err
	}
	interruptSettled = true
	results := process.resultsAfter(mark)
	queuedResults := process.resultsWithTextAfter(mark, "queued-ok")
	exp.Findings = append(exp.Findings, fmt.Sprintf("E6 results after the queued prompt was written: %d, carrying queued-ok: %d", len(results), len(queuedResults)))
	exp.Findings = append(exp.Findings, fmt.Sprintf("E6 inbound assistant or result text carrying queued-ok: %v", process.inboundTextAfter(mark, "queued-ok")))
	exp.Findings = append(exp.Findings, fmt.Sprintf("E6 queued turn recorded %d events with its own result: %v", queuedTurn.Events, queuedTurn.Result != nil))
	return nil
}

// containsSubstring reports whether s contains want, case-insensitively.
func containsSubstring(s, want string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(want))
}
