package tool

import (
	"context"
	"strings"
)

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
		return e.handleSandboxDenial(ctx, ec, br, sandboxDenialParams{
			Output:            output,
			Reason:            "command blocked by sandbox",
			GrantInstructions: "Add a sandbox.host_mount in .steiner/config.yaml or re-run with --unsafe",
			DenialMessage:     "\nAdd a sandbox.host_mount in .steiner/config.yaml or re-run with --unsafe",
		})
	}

	if isSSHConfigOwnershipDenial(output) {
		return e.handleSandboxDenial(ctx, ec, br, sandboxDenialParams{
			Output:            output,
			Reason:            "SSH config rejected inside sandbox",
			GrantInstructions: "Re-run outside the sandbox with --unsafe",
			DenialMessage:     "\nSSH failed because OpenSSH rejected config ownership inside the sandbox.\nThe command was not rerun outside the sandbox.",
		})
	}

	return result, nil
}

// sandboxDenialParams holds the localized guidance shown to the user when a
// bash command is denied by the sandbox and re-execution requires approval.
type sandboxDenialParams struct {
	Output            string
	Reason            string
	GrantInstructions string
	DenialMessage     string
}

func (e *Executor) handleSandboxDenial(ctx context.Context, ec *executionContext, br BashDenialResult, params sandboxDenialParams) (any, error) {
	policy, ok := ctx.Value(EffectivePolicyKey{}).(*PathPolicy)
	if !ok || policy == nil {
		policy = &e.pathPolicy
	}
	req := ApprovalRequest{
		Tool:              ec.Def,
		CallID:            ec.CallID,
		Input:             ec.NormalizedInput,
		Kind:              ApprovalKindPath,
		Reason:            params.Reason,
		GrantInstructions: params.GrantInstructions,
		Response:          make(chan ApprovalResponse, 1),
		Path: &PathApprovalDetails{
			WorkDir:    policy.Root(),
			Preview:    buildApprovalPreview(ec.Def.Name, ec.NormalizedInput, *policy),
			DeniedPath: extractDeniedPath(params.Output),
		},
	}
	if approvalErr := e.approver.RequestApproval(ctx, req); approvalErr == nil {
		resp := <-req.Response
		if resp.Allow {
			// Retrying escapes the sandbox by default. A child forced read-only
			// (BashReadOnlyProjectKey) must not be able to reach an unsandboxed
			// retry via this approval escape hatch, so it keeps its read-only
			// wrapper instead of falling back to Unsandboxed{}.
			retry := ResolvedSandbox{Wrapper: Unsandboxed{}}
			if readOnlyProjectFor(ctx) {
				retry = ResolvedSandbox{Wrapper: e.sandbox, ReadOnlyProject: true}
			}
			retryCtx := context.WithValue(ctx, SandboxWrapperKey{}, retry)
			return ec.Def.Handler(retryCtx, ec.NormalizedInput)
		}
	}
	// Denied or approval error — append the localized denial guidance and return original.
	br.AppendOutput(params.DenialMessage)
	return br, nil
}
