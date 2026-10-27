package builtin

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/tool"
)

// BashResult is the result from a bash tool call.
type BashResult struct {
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
				return &BashResult{ExitCode: 255, Output: "sandbox wrapper not resolved; bash invoked outside execution pipeline"}, nil
			}

			session := NewBashSession()
			session.CommandWrapper = resolved.Wrap
			session.ReleaseCommandResources = resolved.ReleaseCommandResources
			if err := session.Start(); err != nil {
				return nil, fmt.Errorf("bash: start session: %w", err)
			}
			defer func() { _ = session.Close() }()

			stdoutCap, stderrCap, exitCode, timedOut, execErr := session.execute(execCtx, command)
			if execErr != nil {
				// Session error: return as a result rather than a Go error so
				// the model receives the failure information.
				msg, _ := fitBashOutput("", execErr.Error(), in.MaxOutputBytes, 0)
				return &BashResult{ExitCode: -1, Output: msg}, nil
			}

			body, omitted := combineBashStreams(stdoutCap, stderrCap, in.RawOutput)
			var notice string
			if timedOut {
				// Timeout is a partial result: keep whatever output was captured.
				notice = fmt.Sprintf("[bash session: %v; output above is partial]", cmp.Or(execCtx.Err(), context.Canceled))
				if body != "" {
					notice = "\n" + notice
				}
			}
			out, cut := fitBashOutput(body, notice, in.MaxOutputBytes, omitted)

			return &BashResult{
				ExitCode:  exitCode,
				Truncated: omitted > 0 || cut,
				Output:    out,
			}, nil
		},
	}
}

// combineBashStreams renders stdout followed by labelled stderr. omitted counts
// the bytes capture or cleaning dropped (each drop is also labelled in the
// text). Stderr whitespace is preserved; one trailing newline of stdout is
// trimmed.
func combineBashStreams(stdout, stderr streamCapture, raw bool) (body string, omitted int64) {
	so, soOmitted := renderBashStream(stdout, raw)
	se, seOmitted := renderBashStream(stderr, raw)
	body = strings.TrimSuffix(so, "\n")
	if stderr.TotalBytes > 0 {
		if body != "" {
			body += "\n"
		}
		body += "[stderr]\n" + se
	}
	return body, soOmitted + seOmitted
}

func bashOmittedLabel(n int64) string {
	return fmt.Sprintf("\n[%d bytes omitted]\n", n)
}

// renderBashStream turns one capture into text. Head and tail are cleaned
// independently (unless raw) and a label reports the omitted middle plus any
// bytes cleaning discarded at the cut boundaries.
func renderBashStream(c streamCapture, raw bool) (text string, omitted int64) {
	gap := c.TotalBytes - int64(len(c.Head)) - int64(len(c.Tail))
	if gap <= 0 {
		joined := append(append([]byte(nil), c.Head...), c.Tail...)
		if raw {
			return string(joined), 0
		}
		text, _ = cleanBashText(joined, false, false)
		return text, 0
	}
	if raw {
		head := trimIncompleteUTF8Suffix(c.Head)
		tail := c.Tail
		for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
			tail = tail[1:]
		}
		dropped := gap + int64(len(c.Head)-len(head)) + int64(len(c.Tail)-len(tail))
		return string(head) + bashOmittedLabel(dropped) + string(tail), dropped
	}
	head, skippedHead := cleanBashText(c.Head, false, true)
	tail, skippedTail := cleanBashText(c.Tail, true, false)
	omitted = gap + int64(skippedHead) + int64(skippedTail)
	return head + bashOmittedLabel(omitted) + tail, omitted
}

// fitBashOutput joins body and suffix within budget bytes, marker included.
// An oversized body keeps a 30% head and 70% tail (the actual end of the
// output) around a truncation marker; cuts are UTF-8 safe. The suffix is kept
// whole unless it alone exceeds half the budget. omitted is the number of
// bytes already dropped before body, counted in the marker's total.
func fitBashOutput(body, suffix string, budget int, omitted int64) (out string, cut bool) {
	if budget <= 0 || len(body)+len(suffix) <= budget {
		return body + suffix, false
	}
	if len(suffix) > budget/2 {
		suffix = trimIncompleteUTF8SuffixString(suffix[:budget/2])
	}
	room := budget - len(suffix)
	// Digit counts of the final marker never exceed those of this one.
	maxMarker := "\n" + tool.TruncationMarker(len(body), len(body)+int(omitted)) + "\n"
	avail := room - len(maxMarker)
	if avail < 0 {
		return trimIncompleteUTF8SuffixString(maxMarker[:room]) + suffix, true
	}
	headN := avail * 3 / 10
	tailStart := len(body) - (avail - headN)
	for tailStart < len(body) && !utf8.RuneStart(body[tailStart]) {
		tailStart++
	}
	head := trimIncompleteUTF8SuffixString(body[:headN])
	tail := body[tailStart:]
	marker := "\n" + tool.TruncationMarker(len(head)+len(tail), len(body)+int(omitted)) + "\n"
	return head + marker + tail + suffix, true
}
