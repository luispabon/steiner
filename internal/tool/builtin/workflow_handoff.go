package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/luispabon/steiner/internal/tool"
)

const (
	workflowHandoffToolName        = "workflow_handoff"
	workflowHandoffNextImplement   = "implement"
	workflowHandoffNextReview      = "review"
	workflowHandoffMessageMaxRunes = 512
	workflowHandoffTargetPrefix    = ".steiner/plans"
)

var workflowHandoffTargetUnsafeRunes = map[rune]struct{}{
	'!':  {},
	'"':  {},
	'\'': {},
	'$':  {},
	'&':  {},
	'(':  {},
	')':  {},
	'*':  {},
	';':  {},
	'<':  {},
	'>':  {},
	'?':  {},
	'[':  {},
	'\\': {},
	']':  {},
	'`':  {},
	'{':  {},
	'|':  {},
	'}':  {},
}

// WorkflowHandoffInput is the model-facing input for the workflow_handoff tool.
type WorkflowHandoffInput struct {
	Next    string `json:"next"`
	Target  string `json:"target"`
	Message string `json:"message,omitempty"`
}

// WorkflowHandoffSchema returns the JSON schema for the workflow_handoff tool.
func WorkflowHandoffSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"next": map[string]any{
				"type":        "string",
				"description": "Required. The next workflow step to hand off to.",
				"enum":        []string{workflowHandoffNextImplement, workflowHandoffNextReview},
			},
			"target": map[string]any{
				"type":        "string",
				"description": "Required. A safe relative .steiner/plans/... directory that contains overview.md and plan.yaml.",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Optional handoff message. Trimmed and bounded to keep the request concise.",
				"maxLength":   workflowHandoffMessageMaxRunes,
			},
		},
		"required":             []string{"next", "target"},
		"additionalProperties": false,
	}
}

// NewWorkflowHandoffTool creates the workflow_handoff built-in tool.
func NewWorkflowHandoffTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            workflowHandoffToolName,
		Description:     "Create a workflow handoff request for an approved plan directory. The target must be a relative .steiner/plans/... directory with overview.md and plan.yaml.",
		ParameterSchema: WorkflowHandoffSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			return handleWorkflowHandoff(ctx, env, input)
		},
	}
}

func handleWorkflowHandoff(ctx context.Context, env Env, input map[string]any) (any, error) {
	in, err := decodeInput[WorkflowHandoffInput](input)
	if err != nil {
		return nil, fmt.Errorf("workflow_handoff: %w", err)
	}

	in.Next = strings.ToLower(strings.TrimSpace(in.Next))
	in.Target = strings.TrimSpace(in.Target)
	var truncated bool
	in.Message, truncated = normalizeWorkflowHandoffMessage(in.Message)

	if err := validateWorkflowHandoffNext(in.Next); err != nil {
		return nil, err
	}

	target, absTarget, err := normalizeWorkflowHandoffTarget(env.WorkDir, in.Target)
	if err != nil {
		return nil, err
	}
	in.Target = target

	if err := validateWorkflowHandoffArtifacts(absTarget); err != nil {
		return nil, err
	}

	result := &WorkflowHandoffResult{
		Next:             in.Next,
		Target:           in.Target,
		Message:          in.Message,
		MessageTruncated: truncated,
	}

	if !env.Interactive || env.EventSink == nil || env.WorkflowHandoffResponder == nil {
		result.Status = "unsupported"
		result.Reason = "workflow handoff decision handling is unavailable in non-interactive mode"
		return result, nil
	}

	response, err := env.WorkflowHandoffResponder.RequestWorkflowHandoff(ctx, tool.WorkflowHandoffRequest{
		Next:    in.Next,
		Target:  in.Target,
		Message: in.Message,
	})
	if err != nil {
		return nil, err
	}
	if response.Accepted {
		return tool.WorkflowHandoffAccepted{
			Transition: tool.WorkflowHandoffTransition{
				Next:    in.Next,
				Target:  in.Target,
				Message: in.Message,
			},
		}, nil
	}

	return struct {
		Status string `json:"status"`
	}{
		Status: "declined",
	}, nil
}

func validateWorkflowHandoffNext(next string) error {
	switch next {
	case workflowHandoffNextImplement, workflowHandoffNextReview:
		return nil
	default:
		return fmt.Errorf("workflow_handoff: next must be one of %s, %s", workflowHandoffNextImplement, workflowHandoffNextReview)
	}
}

func normalizeWorkflowHandoffTarget(workDir, raw string) (string, string, error) {
	if raw == "" {
		return "", "", fmt.Errorf("workflow_handoff: target is required")
	}
	if err := validateWorkflowHandoffTargetSafety(raw); err != nil {
		return "", "", err
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("workflow_handoff: target is required")
	}
	if filepath.IsAbs(raw) {
		return "", "", fmt.Errorf("workflow_handoff: target must be a relative %s/... directory", workflowHandoffTargetPrefix)
	}

	cleaned := filepath.Clean(raw)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("workflow_handoff: target must stay under %s", workflowHandoffTargetPrefix)
	}
	if cleaned != workflowHandoffTargetPrefix && !strings.HasPrefix(cleaned, workflowHandoffTargetPrefix+string(filepath.Separator)) {
		return "", "", fmt.Errorf("workflow_handoff: target must be under %s", workflowHandoffTargetPrefix)
	}

	baseDir := strings.TrimSpace(workDir)
	if baseDir == "" {
		var err error
		baseDir, err = os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("workflow_handoff: resolve working directory: %w", err)
		}
	}

	absTarget := filepath.Clean(filepath.Join(baseDir, cleaned))
	return cleaned, absTarget, nil
}

func validateWorkflowHandoffTargetSafety(raw string) error {
	for _, part := range strings.Split(raw, string(filepath.Separator)) {
		if part == ".." {
			return fmt.Errorf("workflow_handoff: target contains traversal")
		}
	}

	for _, r := range raw {
		if unicode.IsControl(r) {
			return fmt.Errorf("workflow_handoff: target contains control characters")
		}
		if _, ok := workflowHandoffTargetUnsafeRunes[r]; ok {
			return fmt.Errorf("workflow_handoff: target contains shell metacharacters")
		}
	}

	return nil
}

func validateWorkflowHandoffArtifacts(absTarget string) error {
	info, err := os.Stat(absTarget)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("workflow_handoff: target %q does not exist", filepath.Base(absTarget))
		}
		return fmt.Errorf("workflow_handoff: inspect target %q: %w", absTarget, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workflow_handoff: target %q must be a directory", absTarget)
	}

	for _, name := range []string{"overview.md", "plan.yaml"} {
		path := filepath.Join(absTarget, name)
		fileInfo, statErr := os.Stat(path)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return fmt.Errorf("workflow_handoff: target %q is missing %s", absTarget, name)
			}
			return fmt.Errorf("workflow_handoff: inspect %s: %w", path, statErr)
		}
		if fileInfo.IsDir() {
			return fmt.Errorf("workflow_handoff: target %q has invalid %s", absTarget, name)
		}
	}

	return nil
}

func normalizeWorkflowHandoffMessage(message string) (string, bool) {
	message = strings.TrimSpace(message)
	if message == "" {
		return "", false
	}
	runes := []rune(message)
	if len(runes) <= workflowHandoffMessageMaxRunes {
		return message, false
	}
	return string(runes[:workflowHandoffMessageMaxRunes]), true
}
