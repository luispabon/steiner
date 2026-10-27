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
	ExitCode  int    `json:"exit_code"`
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

			stdout, stdoutLoss := bashStreamForFormat(stdoutCap, in.RawOutput)
			stderr, stderrLoss := bashStreamForFormat(stderrCap, in.RawOutput)
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
				Truncated:      bashResultTruncated(preview.Complete, execErr, timedOut),
				TimedOut:       timedOut,
				Output:         out,
			}
			var parts []string
			if message != "" {
				parts = append(parts, message)
			}
			if !preview.Complete {
				parts = append(parts, bashPreviewOmissionNote(preview, in.RawOutput))
				parts = append(parts, saveBashResultArtifact(env.WorkDir, fmtIn, bashArtifactCause{
					stdoutLoss: stdoutLoss, stderrLoss: stderrLoss, raw: in.RawOutput,
					timedOut: timedOut, execErr: execErr != nil,
				}, res))
			}
			res.Message = strings.Join(parts, " ")
			return res, nil
		},
	}
}

// bashResultTruncated reports whether a bash result is incomplete: the preview
// omitted output, the session failed, or the command timed out.
func bashResultTruncated(previewComplete bool, execErr error, timedOut bool) bool {
	return !previewComplete || execErr != nil || timedOut
}

// bashErrorCause drops the captured stdout and stderr text that
// bashStreamReadError embeds in its message; the captures are rendered
// separately. Errors other than bashStreamError fall back to cutting the
// message at the capture markers.
func bashErrorCause(err error) string {
	var streamErr *bashStreamError
	if errors.As(err, &streamErr) {
		return streamErr.cause
	}
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
// independently with cut flags. Raw output bypasses the cleaner, so its
// capture gap is in representation units (Omitted). For cleaned output the
// capture gap and the cleaner's boundary skips are raw source bytes with no
// known rendered size (SourceOmitted), while cleaned text beyond the retained
// window (cleaning can expand input many times over) is dropped from the
// middle and counted in rendered bytes (Omitted). The head and the actual tail
// are kept; cuts never split a valid UTF-8 rune.
func bashStreamForFormat(c streamCapture, raw bool) (s bashOutStream, loss bashStreamLoss) {
	gap := max(c.TotalBytes-int64(len(c.Head))-int64(len(c.Tail)), 0)
	if raw {
		if gap == 0 {
			return bashOutStream{Head: append(append([]byte(nil), c.Head...), c.Tail...)}, loss
		}
		return bashOutStream{Head: c.Head, Tail: c.Tail, Omitted: gap}, bashStreamLoss{gap: gap}
	}
	if gap == 0 {
		text, skipped := cleanBashText(append(append([]byte(nil), c.Head...), c.Tail...), false, false)
		s = bashBoundCleaned(text, "", true)
		s.SourceOmitted = int64(skipped)
		return s, bashStreamLoss{skipped: int64(skipped)}
	}
	head, skippedHead := cleanBashText(c.Head, false, true)
	tail, skippedTail := cleanBashText(c.Tail, true, false)
	s = bashBoundCleaned(head, tail, false)
	loss = bashStreamLoss{gap: gap, skipped: int64(skippedHead + skippedTail)}
	s.SourceOmitted = loss.gap + loss.skipped
	return s, loss
}

// bashStreamLoss splits the raw source bytes of a cleaned stream that never
// reached the representation: dropped by capture, and skipped by the cleaner at
// cut boundaries.
type bashStreamLoss struct {
	gap, skipped int64
}

// bashBoundCleaned keeps at most bashArtifactMaxBytes of head and of tail
// (all the formatter can use) and counts the rendered bytes between in
// Omitted. With contiguous, head is one text (tail is empty) that is split
// only when longer than twice the bound.
func bashBoundCleaned(head, tail string, contiguous bool) bashOutStream {
	if contiguous {
		if len(head) <= 2*bashArtifactMaxBytes {
			return bashOutStream{Head: []byte(head)}
		}
		tail = head
		hc := bashOutHeadCut(head, bashArtifactMaxBytes)
		tc := bashOutTailCut(tail, len(tail)-bashArtifactMaxBytes)
		return bashOutStream{Head: []byte(head[:hc]), Tail: []byte(tail[tc:]), Omitted: int64(tc - hc)}
	}
	hc, tc := len(head), 0
	if len(head) > bashArtifactMaxBytes {
		hc = bashOutHeadCut(head, bashArtifactMaxBytes)
	}
	if len(tail) > bashArtifactMaxBytes {
		tc = bashOutTailCut(tail, len(tail)-bashArtifactMaxBytes)
	}
	return bashOutStream{Head: []byte(head[:hc]), Tail: []byte(tail[tc:]), Omitted: int64(len(head)-hc) + int64(tc)}
}

// bashPreviewOmissionNote states per-stream omissions; the inline marker may
// not fit a small budget.
// Rendered and raw source bytes are different units and are never summed.
func bashPreviewOmissionNote(p bashOutPreview, raw bool) string {
	unit := "rendered bytes"
	if raw {
		unit = "bytes"
	}
	var parts []string
	for _, s := range []struct {
		name             string
		rendered, source int64
	}{{"stdout", p.StdoutOmitted, p.StdoutSource}, {"stderr", p.StderrOmitted, p.StderrSource}} {
		if s.rendered > 0 {
			parts = append(parts, fmt.Sprintf("%s %d %s", s.name, s.rendered, unit))
		}
		if s.source > 0 {
			parts = append(parts, fmt.Sprintf("%s %d raw source bytes (rendered size unknown)", s.name, s.source))
		}
	}
	return "Output preview omits " + strings.Join(parts, ", ") + "."
}

type bashArtifactCause struct {
	stdoutLoss, stderrLoss bashStreamLoss
	raw                    bool
	timedOut               bool
	execErr                bool
}

// bashArtifactOmissions states what one stream lost from the artifact, one
// unit per item. Only raw mode, where all units are equal, gets a total.
// capCut is what the artifact size cap removed on top of what the input
// already lacked.
func bashArtifactOmissions(name string, raw bool, s bashOutStream, loss bashStreamLoss, capCut int64) string {
	var parts []string
	add := func(n int64, format string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf(format, n))
		}
	}
	if raw {
		add(s.Omitted, "capture gap %d bytes")
		add(capCut, "artifact size cap %d bytes")
		if s.Omitted > 0 && capCut > 0 {
			parts = append(parts, fmt.Sprintf("total %d bytes", s.Omitted+capCut))
		}
	} else {
		add(loss.gap, "capture gap %d raw source bytes")
		add(loss.skipped, "cleaner boundary skips %d raw source bytes")
		add(s.Omitted, "cleaned text beyond the retained window %d rendered bytes")
		add(capCut, "artifact size cap %d rendered bytes")
	}
	if len(parts) == 0 {
		return ""
	}
	return name + ": " + strings.Join(parts, ", ")
}

// saveBashResultArtifact builds the artifact, saves it and records status,
// path and limit flag on res. It returns the message sentence. A save failure
// only sets the failed status; the command outcome is unchanged.
func saveBashResultArtifact(workDir string, in bashOutInput, cause bashArtifactCause, res *BashResult) string {
	art := formatBashArtifact(in)
	cutOut, cutErr := max(art.StdoutOmitted-in.Stdout.Omitted, 0), max(art.StderrOmitted-in.Stderr.Omitted, 0)
	limitHit := cutOut > 0 || cutErr > 0
	var path string
	var err error
	if workDir == "" {
		err = errors.New("work directory is not set")
	} else {
		path, err = saveBashArtifact(workDir, []byte(art.Body))
	}
	if err != nil {
		res.ArtifactStatus = bashArtifactFailed
		return fmt.Sprintf("Full output was not saved (artifact_status %s): %v.", bashArtifactFailed, err)
	}
	res.FilePath = path
	res.ArtifactLimitHit = limitHit
	clip := fmt.Sprintf("The read tool rejects files with a NUL byte in the first %d bytes as binary and clips lines longer than %d characters, so such content cannot be read back.", binaryProbeBytes, readMaxLineRunes)
	if art.Complete && !cause.timedOut && !cause.execErr {
		res.ArtifactStatus = bashArtifactComplete
		return fmt.Sprintf("Full output saved to %s (artifact_status %s); page it with read offset/limit. %s", path, bashArtifactComplete, clip)
	}
	res.ArtifactStatus = bashArtifactPartial
	var parts []string
	for _, o := range []string{
		bashArtifactOmissions("stdout", cause.raw, in.Stdout, cause.stdoutLoss, cutOut),
		bashArtifactOmissions("stderr", cause.raw, in.Stderr, cause.stderrLoss, cutErr),
	} {
		if o != "" {
			parts = append(parts, o)
		}
	}
	if cause.timedOut || cause.execErr {
		parts = append(parts, "command did not finish normally")
	}
	if limitHit {
		parts = append(parts, fmt.Sprintf("artifact size cap is %d bytes", bashArtifactMaxBytes))
	}
	return fmt.Sprintf("Partial output saved to %s (artifact_status %s; %s); the full output cannot be recovered. %s", path, bashArtifactPartial, strings.Join(parts, "; "), clip)
}
