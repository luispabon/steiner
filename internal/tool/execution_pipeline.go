package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

type executionInput struct {
	ToolName string
	CallID   string
	Input    map[string]any
}

type executionContext struct {
	Def             ToolDef
	CallID          string
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

func (e *Executor) normalizeExecutionInput(ctx context.Context, def ToolDef, callID string, input map[string]any, policy PathPolicy) (map[string]any, *PathPolicy, error) {
	normalizedInput, err := policy.ValidateToolInput(def.Name, input)
	if err == nil {
		return normalizedInput, nil, nil
	}

	// Check if this is a promptable path policy violation (outside project root).
	var policyErr *PathPolicyError
	if errors.As(err, &policyErr) && policyErr.Promptable && e.approver != nil {
		req := ApprovalRequest{
			Tool:              def,
			CallID:            callID,
			Input:             input,
			Kind:              ApprovalKindPath,
			Reason:            policyErr.Reason,
			GrantInstructions: "Add a sandbox.host_mount in .steiner/config.yaml or re-run with --unsafe",
			Response:          make(chan ApprovalResponse, 1),
			Path: &PathApprovalDetails{
				WorkDir:    policy.Root(),
				Preview:    buildApprovalPreview(def.Name, input, policy),
				DeniedPath: policyErr.Path,
			},
		}
		if approvalErr := e.approver.RequestApproval(ctx, req); approvalErr != nil {
			return nil, nil, policyDeniedError(def.Name, err)
		}
		resp := <-req.Response
		if resp.Allow {
			relaxed := policy.WithoutRoot()
			relaxedInput, relaxedErr := relaxed.ValidateToolInput(def.Name, input)
			if relaxedErr != nil {
				return nil, nil, policyDeniedError(def.Name, relaxedErr)
			}
			return relaxedInput, &relaxed, nil
		}
	}

	return nil, nil, policyDeniedError(def.Name, err)
}

// isUnparsedToolInput recognizes only the reserved sentinel shape. It runs
// before policy, approval, handler and subprocess dispatch so malformed model
// input cannot cause side effects, including through MCP handlers.
func isUnparsedToolInput(input map[string]any) bool {
	if len(input) != 1 {
		return false
	}
	sentinel, ok := input[provider.UnparsedToolInputKey].(map[string]any)
	if !ok || len(sentinel) != 2 {
		return false
	}
	raw, rawOK := sentinel["raw"].(string)
	if !rawOK {
		return false
	}
	var length int
	switch value := sentinel["len"].(type) {
	case int:
		length = value
	case float64:
		if value < 0 || value != float64(int(value)) {
			return false
		}
		length = int(value)
	default:
		return false
	}
	return length >= 0 && length == len([]byte(raw))
}

func policyDeniedError(toolName string, err error) *ToolExecutionError {
	return &ToolExecutionError{
		Tool:    toolName,
		Kind:    "policy_denied",
		Message: err.Error(),
	}
}

func (e *Executor) runPipeline(ctx context.Context, in executionInput) (any, error) {
	if msg, ok := toolInputDecodeError(ctx); ok {
		return nil, &ToolExecutionError{
			Tool:    in.ToolName,
			Kind:    KindInvalidToolInput,
			Message: fmt.Sprintf("tool call arguments could not be parsed as JSON: %s; resend the call with valid JSON arguments", msg),
		}
	}
	if isUnparsedToolInput(in.Input) {
		return nil, &ToolExecutionError{
			Tool:    in.ToolName,
			Kind:    KindInvalidToolInput,
			Message: "tool input could not be parsed by the Claude CLI; resend the call with valid JSON arguments, reducing argument size if necessary",
		}
	}

	def, err := e.resolveDefinition(in)
	if err != nil {
		return nil, err
	}

	mode := config.ExecutionModeBuild
	if e.modeGetter != nil {
		mode = e.modeGetter()
	}

	// Compute the effective policy: apply plan mode restriction if needed.
	effectivePolicy := e.pathPolicy
	if mode == config.ExecutionModePlan {
		dirs := config.PlanModeWritableDirs()
		prefixes := make([]string, 0, len(dirs))
		for _, dir := range dirs {
			prefixes = append(prefixes, filepath.Join(e.pathPolicy.Root(), filepath.FromSlash(dir)))
		}
		effectivePolicy = e.pathPolicy.RestrictWritesTo(prefixes...)
	}

	normalizedInput, approvalPolicy, err := e.normalizeExecutionInput(ctx, def, in.CallID, in.Input, effectivePolicy)
	if err != nil {
		return nil, err
	}

	var toolCtx context.Context
	if approvalPolicy != nil {
		toolCtx = context.WithValue(ctx, EffectivePolicyKey{}, approvalPolicy)
	} else {
		toolCtx = context.WithValue(ctx, EffectivePolicyKey{}, &effectivePolicy)
	}

	if e.modeGetter != nil {
		toolCtx = context.WithValue(toolCtx, ExecutionModeKey{}, mode)
	}

	// Resolve the sandbox decision for this call once, so bash and
	// subprocess-backed tools apply the same read-only-project rule instead of
	// computing it independently.
	toolCtx = context.WithValue(toolCtx, SandboxWrapperKey{}, ResolvedSandbox{Wrapper: e.sandbox, ReadOnlyProject: readOnlyProjectFor(toolCtx)})

	ec := executionContext{
		Def:             def,
		CallID:          in.CallID,
		NormalizedInput: normalizedInput,
	}

	toolCtx = context.WithValue(toolCtx, ExecutionCallIDKey{}, ec.CallID)
	return e.executeTool(toolCtx, &ec)
}

// readOnlyProjectFor reports whether the project must stay mounted read-only
// for this call: a child forced read-only via BashReadOnlyProjectKey, or plan
// mode (carried as ExecutionModeKey). runPipeline and handleSandboxDenial both
// use it so an approval retry cannot escape enforcement the first run had.
func readOnlyProjectFor(ctx context.Context) bool {
	if ctx.Value(BashReadOnlyProjectKey{}) == true {
		return true
	}
	mode, ok := ctx.Value(ExecutionModeKey{}).(config.ExecutionMode)
	return ok && mode == config.ExecutionModePlan
}

// executeTool dispatches tool execution to the appropriate phase:
//   - handler execution, if the tool defines a Handler
//   - subprocess execution, with JSON input marshaling, per-tool timeout,
//     work-dir selection, and output decoding via decodeExecutionOutput
func (e *Executor) executeTool(ctx context.Context, ec *executionContext) (any, error) {
	if ec.Def.Handler != nil {
		// Handler tools (e.g. bash) receive the resolved sandbox decision via
		// SandboxWrapperKey in ctx, set once above by runPipeline, rather than
		// as a parameter here.
		result, err := ec.Def.Handler(ctx, ec.NormalizedInput)
		if err != nil {
			return result, err
		}
		if e.approver != nil && e.sandbox.Enabled() {
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

	// Fail closed: this tool was invoked outside runPipeline.
	resolved, ok := ResolvedSandboxFrom(ctx)
	if !ok {
		return nil, &ToolExecutionError{
			Tool:    ec.Def.Name,
			Kind:    "sandbox_wrapper_missing",
			Message: "sandbox wrapper not resolved; tool invoked outside execution pipeline",
		}
	}

	stdout, metadata, runErr := runSubprocess(execCtx, ec.Def, payload, workDir, e.outputLimit, resolved)
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

	if metadata.ExitCode != 0 {
		return nil, &ToolExecutionError{
			Tool:     toolName,
			Kind:     "nonzero_exit",
			Message:  fmt.Sprintf("tool exited with status %d despite reporting ok:true", metadata.ExitCode),
			ExitCode: metadata.ExitCode,
			Output:   metadata,
			Details:  outputDetails(metadata),
		}
	}

	value := envelope.Result
	if envelope.ModelResult != nil {
		value = envelope.ModelResult
	}
	if s, ok := value.(string); ok && strings.TrimSpace(s) == "" {
		value = "(no content)"
	}
	return ExecutionResult{
		Value:    value,
		Metadata: metadata,
	}, nil
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
