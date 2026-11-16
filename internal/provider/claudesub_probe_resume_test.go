//go:build claudesub_probe

package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// E5 prompts. The parent, resume and fork processes all use a tool-free first
// prompt, so the init gate can use it as the gating turn without adding a turn
// to the persisted transcript.
const (
	claudeSubProbePromptE5T1 = "PROBE-T1E5 Reply with exactly: alpha. Do not use tools."
	claudeSubProbePromptE5T2 = "PROBE-T2E5 Reply with exactly: beta. Do not use tools."
	claudeSubProbePromptE5T3 = "PROBE-T3E5 Write a numbered list from 1 to 200, one item per line, in the form item-<n>. Use at least 200 lines and no tools."
	claudeSubProbePromptE5T4 = "PROBE-T4E5 List every PROBE marker in the earlier user messages you can see, in order, one per line and nothing else."
)

// claudeSubProbeE5 measures the persisted-session resume path: turns to
// completion, one interrupted turn, production teardown, then a new process
// resumed at an earlier assistant uuid and a forked variant. Each process's init
// gate uses its own first prompt, so no warmup turn enters the transcript.
func claudeSubProbeE5(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport, steps *claudeSubProbeSteps) error {
	configDir := claudeSubProbeConfigDir()
	spec := claudeSubProbeParentSpec(cfg.Model)
	process, err := claudeSubProbeStart(ctx, cfg, exp, "parent", "probe-e5", spec, claudeSubProbePromptE5T1)
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(process)

	if !process.gateConsumed {
		if err := process.runTurn(ctx, claudeSubProbePromptE5T1, steps.next()); err != nil {
			return err
		}
	}
	if err := process.runTurn(ctx, claudeSubProbePromptE5T2, steps.next()); err != nil {
		return err
	}
	resumeUUID := process.lastAssistantUUIDForPrompt("PROBE-T2E5")
	sessionID := process.sessionID()
	if resumeUUID == "" || sessionID == "" {
		return errors.New("probe E5 needs the PROBE-T2E5 assistant uuid and the CLI session id")
	}
	exp.Findings = append(exp.Findings, fmt.Sprintf("E5 CLI session %s, resume target %s (the last assistant uuid of the PROBE-T2E5 turn)", sessionID, resumeUUID))

	turn3, err := process.beginTurn(claudeSubProbePromptE5T3)
	if err != nil {
		return err
	}
	if _, err := process.pumpUntil(ctx, steps.next(), "first text delta", claudeSubProbeIsTextDelta); err != nil {
		return err
	}
	turn3.Interrupted = true
	if err := process.interrupt(ctx); err != nil {
		return err
	}
	if err := process.awaitInterruptedResult(ctx, steps.next()); err != nil {
		return err
	}
	process.tail(ctx, steps.next())

	// Production teardown of the persisted process, then the on-disk transcript.
	claudeSubProbeFinish(process)
	exp.Transcripts = append(exp.Transcripts, claudeSubProbeReadTranscript("after interrupted turn and exit", configDir, cfg.WorkDir, sessionID))

	// Resume at the PROBE-T2E5 turn's last assistant uuid.
	resumeSpec := spec
	resumeSpec.Resume = claudeSubResume{SessionID: sessionID, AtUUID: resumeUUID}
	resumed, err := claudeSubProbeStart(ctx, cfg, exp, "resume", "probe-e5-resume", resumeSpec, claudeSubProbePromptE5T4)
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(resumed)
	if !resumed.gateConsumed {
		if err := resumed.runTurn(ctx, claudeSubProbePromptE5T4, steps.next()); err != nil {
			return err
		}
	}
	exp.Transcripts = append(exp.Transcripts, claudeSubProbeReadTranscript("after the resumed turn, process live", configDir, cfg.WorkDir, sessionID))
	claudeSubProbeFinish(resumed)
	exp.Transcripts = append(exp.Transcripts, claudeSubProbeReadTranscript("after the resumed process exit", configDir, cfg.WorkDir, sessionID))

	// Forked variant: a new session id from the same resume target, with the same
	// first prompt as its gating turn.
	forkSpec := resumeSpec
	forkSpec.Resume.Fork = true
	forked, err := claudeSubProbeStart(ctx, cfg, exp, "resume-fork", "probe-e5-fork", forkSpec, claudeSubProbePromptE5T4)
	if err != nil {
		return err
	}
	defer claudeSubProbeFinish(forked)
	if !forked.gateConsumed {
		if err := forked.runTurn(ctx, claudeSubProbePromptE5T4, steps.next()); err != nil {
			return err
		}
	}
	forkSessionID := forked.sessionID()
	exp.Findings = append(exp.Findings, fmt.Sprintf("E5 fork session %q differs from the resumed session: %v", forkSessionID, forkSessionID != sessionID))
	claudeSubProbeFinish(forked)
	exp.Transcripts = append(exp.Transcripts, claudeSubProbeReadTranscript("after the forked process exit", configDir, cfg.WorkDir, forkSessionID))

	exp.Findings = append(exp.Findings, "E5 transcript line counts: "+claudeSubProbeTranscriptCounts(exp.Transcripts))
	return nil
}

// claudeSubProbeTranscriptCounts summarises transcript snapshots by line count,
// which is how the report distinguishes a truncated store from a hidden tail.
func claudeSubProbeTranscriptCounts(snapshots []claudeSubProbeTranscript) string {
	parts := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		parts = append(parts, fmt.Sprintf("%s=%d(lines=%v)", snapshot.Label, snapshot.Lines, snapshot.Exists))
	}
	return strings.Join(parts, " ")
}
