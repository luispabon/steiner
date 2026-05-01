package tool

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

type ApprovalResponder interface {
	RequestApproval(ctx context.Context, req ApprovalRequest) error
}

type ApprovalResponderFunc func(ctx context.Context, req ApprovalRequest) error

func (f ApprovalResponderFunc) RequestApproval(ctx context.Context, req ApprovalRequest) error {
	return f(ctx, req)
}

type Executor struct {
	registry    *Registry
	approval    ApprovalResolver
	approver    ApprovalResponder
	workDir     string
	pathPolicy  PathPolicy
	outputLimit int
}

// NewExecutor creates a new tool executor with the given registry, config, approver, and working directory.
func NewExecutor(registry *Registry, cfg config.Config, approver ApprovalResponder, workDir string) *Executor {
	root := normalizeExecutionRoot(workDir)
	outputLimit := cfg.Limits.ToolOutputMaxBytes
	if outputLimit < 1 {
		outputLimit = 65536
	}
	return &Executor{
		registry:    registry,
		approval:    NewApprovalResolver(cfg),
		approver:    approver,
		workDir:     root,
		pathPolicy:  NewPathPolicy(root, cfg.Paths),
		outputLimit: outputLimit,
	}
}

func (e *Executor) Execute(ctx context.Context, toolName string, input map[string]any) (any, error) {
	return e.runPipeline(ctx, executionInput{ToolName: toolName, Input: input})
}

func normalizeExecutionRoot(workDir string) string {
	if strings.TrimSpace(workDir) == "" {
		return ""
	}
	root, err := filepath.Abs(workDir)
	if err != nil {
		return filepath.Clean(workDir)
	}
	return filepath.Clean(root)
}
