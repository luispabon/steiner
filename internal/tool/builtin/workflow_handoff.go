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
	workflowHandoffMessageMaxRunes = 512
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

// workflowTarget describes a registered workflow target with validation constraints.
type workflowTarget struct {
	key               string   // enum value for "next" field
	label             string   // human-readable label
	allowedPathPrefix string   // required path prefix (e.g. ".steiner/plans")
	requiredFiles     []string // required files in target directory (e.g. ["overview.md", "plan.yaml"])
}

// workflowTargets is the registry of available workflow targets.
var workflowTargets = []workflowTarget{
	{
		key:               "implement",
		label:             "plan loop implementation",
		allowedPathPrefix: ".steiner/plans",
		requiredFiles:     []string{"overview.md", "plan.yaml"},
	},
	{
		key:               "review",
		label:             "plan loop review",
		allowedPathPrefix: ".steiner/plans",
		requiredFiles:     []string{"overview.md", "plan.yaml"},
	},
}

// WorkflowHandoffInput is the model-facing input for the workflow_handoff tool.
type WorkflowHandoffInput struct {
	Next    string `json:"next"`
	Target  string `json:"target"`
	Message string `json:"message,omitempty"`
}

// WorkflowHandoffSchema returns the JSON schema for the workflow_handoff tool.
func WorkflowHandoffSchema() map[string]any {
	nextEnum := make([]string, len(workflowTargets))
	for i, wt := range workflowTargets {
		nextEnum[i] = wt.key
	}

	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"next": map[string]any{
				"type":        "string",
				"description": "Required. The workflow target to hand off to.",
				"enum":        nextEnum,
			},
			"target": map[string]any{
				"type":        "string",
				"description": "Required. A safe relative directory with workflow artifacts.",
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
		Description:     "Create a workflow handoff request to transition to a different workflow with approved artifacts. Validates the target directory against configured workflow requirements.",
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

	wt := lookupWorkflowTarget(in.Next)
	if wt == nil {
		return nil, fmt.Errorf("workflow_handoff: unknown target %q", in.Next)
	}

	target, absTarget, err := normalizeWorkflowHandoffTarget(env.WorkDir, in.Target, wt.allowedPathPrefix)
	if err != nil {
		return nil, err
	}
	in.Target = target

	if err := validateWorkflowHandoffArtifacts(absTarget, wt.requiredFiles); err != nil {
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

// lookupWorkflowTarget finds a registered target by key.
func lookupWorkflowTarget(key string) *workflowTarget {
	for i := range workflowTargets {
		if workflowTargets[i].key == key {
			return &workflowTargets[i]
		}
	}
	return nil
}

func validateWorkflowHandoffNext(next string) error {
	if lookupWorkflowTarget(next) == nil {
		keys := make([]string, len(workflowTargets))
		for i, wt := range workflowTargets {
			keys[i] = wt.key
		}
		return fmt.Errorf("workflow_handoff: next must be one of %v", keys)
	}
	return nil
}

func normalizeWorkflowHandoffTarget(workDir, raw string, prefix string) (string, string, error) {
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
		return "", "", fmt.Errorf("workflow_handoff: target must be a relative %s/... directory", prefix)
	}

	cleaned := filepath.Clean(raw)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("workflow_handoff: target must stay under %s", prefix)
	}
	if cleaned != prefix && !strings.HasPrefix(cleaned, prefix+string(filepath.Separator)) {
		return "", "", fmt.Errorf("workflow_handoff: target must be under %s", prefix)
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

func validateWorkflowHandoffArtifacts(absTarget string, requiredFiles []string) error {
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

	for _, name := range requiredFiles {
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
