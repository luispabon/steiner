package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type executionInput struct {
	ToolName string
	Input    map[string]any
}

type executionContext struct {
	Def             ToolDef
	NormalizedInput map[string]any
}

func (e *Executor) resolveDefinition(in executionInput) (ToolDef, error) {
	if e == nil || e.registry == nil {
		return ToolDef{}, fmt.Errorf("tool executor is not configured")
	}
	def, ok := e.registry.Get(in.ToolName)
	if !ok {
		return ToolDef{}, fmt.Errorf("tool %q is not registered", in.ToolName)
	}
	return def, nil
}

func (e *Executor) normalizeExecutionInput(ctx context.Context, def ToolDef, input map[string]any) (map[string]any, error) {
	normalizedInput, err := e.pathPolicy.ValidateToolInput(def.Name, input)
	if err == nil {
		return normalizedInput, nil
	}

	// Check if this is a promptable path policy violation (outside project root).
	var policyErr *PathPolicyError
	if errors.As(err, &policyErr) && policyErr.Promptable && e.approver != nil {
		req := ApprovalRequest{
			Tool:              def,
			Input:             input,
			WorkDir:           e.pathPolicy.Root(),
			Preview:           buildApprovalPreview(def.Name, input, e.pathPolicy),
			DeniedPath:        policyErr.Path,
			Reason:            policyErr.Reason,
			GrantInstructions: "Add a host_mount in .steiner/config.yaml or re-run with --unsafe",
			Response:          make(chan ApprovalResponse, 1),
		}
		if approvalErr := e.approver.RequestApproval(ctx, req); approvalErr != nil {
			return nil, &ToolExecutionError{
				Tool:    def.Name,
				Kind:    "policy_denied",
				Message: err.Error(),
			}
		}
		resp := <-req.Response
		if resp.Allow {
			relaxed := e.pathPolicy.WithoutRoot()
			relaxedInput, relaxedErr := relaxed.ValidateToolInput(def.Name, input)
			if relaxedErr != nil {
				return nil, &ToolExecutionError{
					Tool:    def.Name,
					Kind:    "policy_denied",
					Message: relaxedErr.Error(),
				}
			}
			return relaxedInput, nil
		}
	}

	return nil, &ToolExecutionError{
		Tool:    def.Name,
		Kind:    "policy_denied",
		Message: err.Error(),
	}
}

func (e *Executor) runPipeline(ctx context.Context, in executionInput) (any, error) {
	def, err := e.resolveDefinition(in)
	if err != nil {
		return nil, err
	}

	normalizedInput, err := e.normalizeExecutionInput(ctx, def, in.Input)
	if err != nil {
		return nil, err
	}

	ec := executionContext{
		Def:             def,
		NormalizedInput: normalizedInput,
	}

	return e.executeTool(ctx, &ec)
}

// isBashDenial reports whether output contains sandbox denial signals.
func isBashDenial(output string) bool {
	return strings.Contains(output, "Permission denied") ||
		strings.Contains(output, "Operation not permitted")
}

// isSSHConfigOwnershipDenial reports whether OpenSSH rejected a config file
// because the ownership or permissions were not acceptable.
func isSSHConfigOwnershipDenial(output string) bool {
	return strings.Contains(output, "Bad owner or permissions on ")
}

// extractDeniedPath attempts to extract a file path from sandbox denial output.
// Returns empty string if no path can be identified.
func extractDeniedPath(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Permission denied") || strings.Contains(line, "Operation not permitted") {
			// Heuristic: look for a word that looks like an absolute path.
			for _, word := range strings.Fields(line) {
				word = strings.TrimRight(word, ":,.")
				if strings.HasPrefix(word, "/") {
					return word
				}
			}
		}
	}
	return ""
}

// handleBashDenial checks whether a bash handler result represents a sandbox
// denial and, if so, prompts the user for approval and optionally retries
// without the sandbox wrapper. Returns the (possibly updated) result.
func (e *Executor) handleBashDenial(ctx context.Context, ec *executionContext, result any) (any, error) {
	br, ok := result.(BashDenialResult)
	if !ok || br.BashExitCode() == 0 {
		return result, nil
	}

	output := br.BashOutput()
	if isBashDenial(output) {
		const grantInstructions = "Add a host_mount in .steiner/config.yaml or re-run with --unsafe"
		req := ApprovalRequest{
			Tool:              ec.Def,
			Input:             ec.NormalizedInput,
			WorkDir:           e.pathPolicy.Root(),
			Preview:           buildApprovalPreview(ec.Def.Name, ec.NormalizedInput, e.pathPolicy),
			DeniedPath:        extractDeniedPath(output),
			Reason:            "command blocked by sandbox",
			GrantInstructions: grantInstructions,
			Response:          make(chan ApprovalResponse, 1),
		}
		if approvalErr := e.approver.RequestApproval(ctx, req); approvalErr == nil {
			resp := <-req.Response
			if resp.Allow {
				unsandboxedCtx := context.WithValue(ctx, BashUnsandboxedKey{}, true)
				return ec.Def.Handler(unsandboxedCtx, ec.NormalizedInput)
			}
		}
		// Denied or approval error — append grant instructions and return original.
		br.AppendOutput("\n" + grantInstructions)
		return result, nil
	}

	if isSSHConfigOwnershipDenial(output) {
		const grantInstructions = "Re-run outside the sandbox with --unsafe"
		req := ApprovalRequest{
			Tool:              ec.Def,
			Input:             ec.NormalizedInput,
			WorkDir:           e.pathPolicy.Root(),
			Preview:           buildApprovalPreview(ec.Def.Name, ec.NormalizedInput, e.pathPolicy),
			Reason:            "SSH config rejected inside sandbox",
			GrantInstructions: grantInstructions,
			Response:          make(chan ApprovalResponse, 1),
		}
		if approvalErr := e.approver.RequestApproval(ctx, req); approvalErr == nil {
			resp := <-req.Response
			if resp.Allow {
				unsandboxedCtx := context.WithValue(ctx, BashUnsandboxedKey{}, true)
				return ec.Def.Handler(unsandboxedCtx, ec.NormalizedInput)
			}
		}
		br.AppendOutput("\nSSH failed because OpenSSH rejected config ownership inside the sandbox.\nThe command was not rerun outside the sandbox.")
		return result, nil
	}

	return result, nil
}

// executeTool dispatches tool execution to the appropriate phase:
//   - handler execution, if the tool defines a Handler
//   - subprocess execution, with JSON input marshaling, per-tool timeout,
//     work-dir selection, and output decoding via decodeExecutionOutput
func (e *Executor) executeTool(ctx context.Context, ec *executionContext) (any, error) {
	if ec.Def.Handler != nil {
		result, err := ec.Def.Handler(ctx, ec.NormalizedInput)
		if err != nil {
			return result, err
		}
		if e.sandbox != nil && e.approver != nil {
			return e.handleBashDenial(ctx, ec, result)
		}
		return result, nil
	}

	payload, err := json.Marshal(CloneJSONMap(ec.NormalizedInput))
	if err != nil {
		return nil, fmt.Errorf("marshal tool input for %q: %w", ec.Def.Name, err)
	}

	execCtx := ctx
	var cancel context.CancelFunc
	if ec.Def.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, ec.Def.Timeout)
		defer cancel()
	}

	workDir := e.pathPolicy.Root()
	if ec.Def.Name == "bash" {
		if cwd, ok := ec.NormalizedInput["cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
			workDir = cwd
		}
	}

	stdout, _, metadata, runErr := runSubprocess(execCtx, ec.Def, payload, workDir, e.outputLimit, e.sandbox)
	if runErr != nil && !isExitStatusError(runErr) {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return nil, runErr
		}
		return nil, &ToolExecutionError{
			Tool:     ec.Def.Name,
			Kind:     "subprocess_failed",
			Message:  runErr.Error(),
			ExitCode: metadata.ExitCode,
			Output:   metadata,
			Details:  outputDetails(metadata),
		}
	}

	return decodeExecutionOutput(stdout, metadata, ec.Def.Name)
}

// decodeExecutionOutput decodes the JSON envelope from subprocess stdout
// and shapes the result into either an ExecutionResult or a structured error
// carrying execution metadata.
func decodeExecutionOutput(stdout []byte, metadata ExecutionMetadata, toolName string) (any, error) {
	var envelope JSONEnvelope
	if err := json.Unmarshal(stdout, &envelope); err != nil {
		return nil, &ToolExecutionError{
			Tool:     toolName,
			Kind:     "invalid_json",
			Message:  "tool output was not valid JSON",
			ExitCode: metadata.ExitCode,
			Output:   metadata,
			Details:  outputDetails(metadata),
		}
	}

	if !envelope.OK {
		if envelope.Error == nil {
			envelope.Error = &JSONEnvelopeError{
				Kind:    "tool_failed",
				Message: "tool reported a failure without an error payload",
			}
		}
		return nil, &ToolExecutionError{
			Tool:     toolName,
			Kind:     envelope.Error.Kind,
			Message:  envelope.Error.Message,
			ExitCode: metadata.ExitCode,
			Output:   metadata,
			Details:  envelope.Error.Details,
		}
	}

	return ExecutionResult{
		Value:    envelope.Result,
		Metadata: metadata,
	}, nil
}

func runSubprocess(ctx context.Context, def ToolDef, payload []byte, workDir string, limit int, sandbox SandboxWrapper) ([]byte, []byte, ExecutionMetadata, error) {
	if def.ExecPath == "" {
		return nil, nil, ExecutionMetadata{}, &ToolExecutionError{
			Tool:    def.Name,
			Kind:    "invalid_definition",
			Message: "tool exec path is empty",
		}
	}

	args := []string{}
	if def.Subcommand != "" {
		args = append(args, def.Subcommand)
	}

	cmd := exec.CommandContext(ctx, def.ExecPath, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if sandbox != nil {
		cmd = sandbox.WrapCommand(cmd)
	}
	cmd.Stdin = bytes.NewReader(payload)

	stdoutCapture := newBoundedCapture(limit)
	stderrCapture := newBoundedCapture(limit)
	cmd.Stdout = stdoutCapture
	cmd.Stderr = stderrCapture

	err := cmd.Run()
	metadata := ExecutionMetadata{
		ExitCode: exitCodeFromError(err),
		Stdout:   stdoutCapture.Capture(),
		Stderr:   stderrCapture.Capture(),
	}
	if err != nil && ctx.Err() != nil {
		return stdoutCapture.Bytes(), stderrCapture.Bytes(), metadata, ctx.Err()
	}

	return stdoutCapture.Bytes(), stderrCapture.Bytes(), metadata, err
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
		return exitErr.ExitCode()
	}
	return 1
}

func outputDetails(metadata ExecutionMetadata) map[string]any {
	details := map[string]any{
		"stdout": metadata.Stdout,
		"stderr": metadata.Stderr,
	}
	if metadata.ExitCode != 0 {
		details["exit_code"] = metadata.ExitCode
	}
	return details
}

func isExitStatusError(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}
