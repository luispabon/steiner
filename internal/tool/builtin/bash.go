package builtin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/tool"
)

// BashResult is the result from a bash tool call.
type BashResult struct {
	// FilePath is the workDir-relative path of the saved full-output artifact.
	FilePath string `json:"file_path,omitempty"`
	// ArtifactStatus is none, complete, partial or failed.
	ArtifactStatus string `json:"artifact_status"`
	// ArtifactLimitHit reports that the artifact size cap dropped bytes the
	// capture had kept.
	ArtifactLimitHit bool `json:"artifact_limit_hit,omitempty"`
	TimedOut         bool `json:"timed_out,omitempty"`
	// Message states why the result is partial (timeout or session error),
	// per-stream preview omissions and the artifact outcome. It survives
	// output budgets too small to hold the inline notice.
	Message   string `json:"message,omitempty"`
	ExitCode  int    `json:"exit_code,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Output    string `json:"output"`
}

// BashExitCode returns the exit code. Implements tool.BashDenialResult.
func (r *BashResult) BashExitCode() int { return r.ExitCode }

// BashOutput returns the combined output. Implements tool.BashDenialResult.
func (r *BashResult) BashOutput() string { return r.Output }

// AppendOutput appends s to the output. Implements tool.BashDenialResult.
func (r *BashResult) AppendOutput(s string) { r.Output += s }

// NewBashTool creates a ToolDef for the bash tool backed by a local BashSession.
func NewBashTool(env Env) tool.ToolDef {
	maxTimeoutSeconds := resolveBashTimeoutCapSeconds(env.BashTimeoutCap)
	return tool.ToolDef{
		Name:            "bash",
		Description:     "Run a shell command in the workspace. Prefer targeted commands. Set cwd instead of running cd commands when needed. Output may be truncated.",
		ParameterSchema: BashSchema(maxTimeoutSeconds),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[BashInput](input)
			if err != nil {
				return nil, fmt.Errorf("bash: %w", err)
			}

			normalizeBash(&in, maxTimeoutSeconds)

			var cwd string
			if in.CWD != "" {
				cwd, err = env.PathPolicy.ResolveCWD(in.CWD)
				if err != nil {
					return nil, fmt.Errorf("bash: %w", err)
				}
			}

			// Build the command string, optionally prefixed with a cd.
			command := in.Command
			if cwd != "" {
				command = fmt.Sprintf("cd %q && %s", cwd, in.Command)
			}

			// Apply timeout via context.
			timeout := time.Duration(in.TimeoutSeconds) * time.Second
			execCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			resolved, ok := tool.ResolvedSandboxFrom(ctx)
			if !ok {
				// Fail closed: bash was invoked outside the execution pipeline.
				return &BashResult{ArtifactStatus: bashArtifactNone, ExitCode: 255, Output: "sandbox wrapper not resolved; bash invoked outside execution pipeline"}, nil
			}

			session := NewBashSession()
			session.CommandWrapper = resolved.Wrap
			session.ReleaseCommandResources = resolved.ReleaseCommandResources
			if err := session.Start(); err != nil {
				return nil, fmt.Errorf("bash: start session: %w", err)
			}
			defer func() { _ = session.Close() }()

			stdoutCap, stderrCap, exitCode, timedOut, execErr := session.execute(execCtx, command)

			// A timeout or session error is a partial result: keep whatever
			// output was captured and say why it is incomplete.
			var notice string
			switch {
			case execErr != nil:
				exitCode = -1
				notice = fmt.Sprintf("[bash session error: %s; output above is partial]", bashErrorCause(execErr))
			case timedOut:
				notice = fmt.Sprintf("[bash session: %v; output above is partial]", cmp.Or(execCtx.Err(), context.Canceled))
			}
			message := notice
			// The notice is dropped from the output (never from Message) when it
			// would take more than half the budget.
			suffix := ""
			if notice != "" && len(notice)+1 <= in.MaxOutputBytes/2 {
				suffix = "\n" + notice
			}

			stdout, stdoutGap := bashStreamForFormat(stdoutCap, in.RawOutput)
			stderr, stderrGap := bashStreamForFormat(stderrCap, in.RawOutput)
			fmtIn := bashOutInput{
				Stdout: stdout, Stderr: stderr, ExitCode: exitCode,
				InlineLimit: in.MaxOutputBytes - len(suffix),
			}
			preview := formatBashPreview(fmtIn)
			out := preview.Text
			if out == "" {
				suffix = strings.TrimPrefix(suffix, "\n")
			}
			out += suffix

			res := &BashResult{
				ArtifactStatus: bashArtifactNone,
				ExitCode:       exitCode,
				Truncated:      !preview.Complete || execErr != nil,
				TimedOut:       timedOut,
				Output:         out,
			}
			var parts []string
			if message != "" {
				parts = append(parts, message)
			}
			if !preview.Complete {
				parts = append(parts, bashPreviewOmissionNote(preview))
				parts = append(parts, saveBashResultArtifact(env.WorkDir, fmtIn, bashArtifactCause{
					captureGap: stdoutGap + stderrGap, timedOut: timedOut, execErr: execErr != nil,
				}, res))
			}
			res.Message = strings.Join(parts, " ")
			return res, nil
		},
	}
}

// bashErrorCause drops the captured stdout and stderr text that
// bashStreamReadError embeds in its message; the captures are rendered
// separately.
func bashErrorCause(err error) string {
	msg := err.Error()
	for _, sep := range []string{"; stderr: ", "; stdout: "} {
		if i := strings.Index(msg, sep); i >= 0 {
			msg = msg[:i]
		}
	}
	return msg
}

const (
	bashArtifactNone     = "none"
	bashArtifactComplete = "complete"
	bashArtifactPartial  = "partial"
	bashArtifactFailed   = "failed"
)

// bashStreamForFormat turns a capture into formatter input. A contiguous
// capture (no gap) is cleaned as one text; otherwise head and tail are cleaned
// independently with cut flags and the bytes the cleaner skipped join the
// omitted count. Raw output bypasses the cleaner. gap is the capture loss.
func bashStreamForFormat(c streamCapture, raw bool) (s bashOutStream, gap int64) {
	gap = max(c.TotalBytes-int64(len(c.Head))-int64(len(c.Tail)), 0)
	if gap == 0 {
		joined := append(append([]byte(nil), c.Head...), c.Tail...)
		if raw {
			return bashOutStream{Head: joined}, 0
		}
		text, skipped := cleanBashText(joined, false, false)
		return bashOutStream{Head: []byte(text), Omitted: int64(skipped)}, 0
	}
	if raw {
		return bashOutStream{Head: c.Head, Tail: c.Tail, Omitted: gap}, gap
	}
	head, skippedHead := cleanBashText(c.Head, false, true)
	tail, skippedTail := cleanBashText(c.Tail, true, false)
	return bashOutStream{Head: []byte(head), Tail: []byte(tail), Omitted: gap + int64(skippedHead) + int64(skippedTail)}, gap
}

// bashPreviewOmissionNote states per-stream omissions; the inline marker may
// not fit a small budget.
func bashPreviewOmissionNote(p bashOutPreview) string {
	var parts []string
	if p.StdoutOmitted > 0 {
		parts = append(parts, fmt.Sprintf("stdout %d bytes", p.StdoutOmitted))
	}
	if p.StderrOmitted > 0 {
		parts = append(parts, fmt.Sprintf("stderr %d bytes", p.StderrOmitted))
	}
	return "Output preview omits " + strings.Join(parts, ", ") + "."
}

type bashArtifactCause struct {
	captureGap int64
	timedOut   bool
	execErr    bool
}

// saveBashResultArtifact builds the artifact, saves it and records status,
// path and limit flag on res. It returns the message sentence. A save failure
// only sets the failed status; the command outcome is unchanged.
func saveBashResultArtifact(workDir string, in bashOutInput, cause bashArtifactCause, res *BashResult) string {
	art := formatBashArtifact(in)
	limitHit := art.StdoutOmitted+art.StderrOmitted > in.Stdout.Omitted+in.Stderr.Omitted
	var path string
	var err error
	if workDir == "" {
		err = errors.New("work directory is not set")
	} else {
		path, err = saveBashArtifact(workDir, []byte(art.Body))
	}
	if err != nil {
		res.ArtifactStatus = bashArtifactFailed
		return fmt.Sprintf("Full output was not saved (artifact_status failed): %v.", err)
	}
	res.FilePath = path
	res.ArtifactLimitHit = limitHit
	var reasons []string
	if cause.captureGap > 0 {
		reasons = append(reasons, fmt.Sprintf("capture dropped %d bytes", cause.captureGap))
	}
	if limitHit {
		reasons = append(reasons, fmt.Sprintf("artifact size cap of %d bytes cut output", bashArtifactMaxBytes))
	}
	if cause.timedOut || cause.execErr {
		reasons = append(reasons, "command did not finish normally")
	}
	clip := fmt.Sprintf("The read tool clips lines longer than %d characters, so very long lines cannot be fully read back.", readMaxLineRunes)
	if len(reasons) == 0 && art.Complete {
		res.ArtifactStatus = bashArtifactComplete
		return fmt.Sprintf("Full output saved to %s (artifact_status complete); page it with read offset/limit. %s", path, clip)
	}
	res.ArtifactStatus = bashArtifactPartial
	if len(reasons) == 0 {
		reasons = append(reasons, "artifact omits bytes")
	}
	return fmt.Sprintf("Partial output saved to %s (artifact_status partial: %s); the full output cannot be recovered. %s", path, strings.Join(reasons, "; "), clip)
}
