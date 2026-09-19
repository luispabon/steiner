package delegation

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// newVisionHandler returns a handler for the vision agent type.
// It reads the image from the ImageStore, base64-encodes it, injects it into the
// Spec, and spawns a child agent. The result includes the agent_id so
// the caller can use follow_up for additional questions about the same image.
//
//nolint:gocyclo // handler lifecycle branches cover setup, gating, execution, and cleanup.
func newVisionHandler(deps SpecializedToolDeps) func(ctx context.Context, input map[string]any) (any, error) {
	if deps.ActiveController == nil {
		deps.ActiveController = NewActiveController()
	}
	return func(ctx context.Context, input map[string]any) (any, error) {
		brief, err := parseStructuredBrief(string(AgentTypeVision), input)
		if err != nil {
			return nil, err
		}

		task := assembleTaskContent(brief)

		imageID, _ := input["image_id"].(string)
		if imageID == "" {
			return nil, fmt.Errorf("vision: image_id is required")
		}

		imgBlock, err := loadVisionImageBlock(imageID, deps.ImageStore)
		if err != nil {
			return nil, err
		}

		agentID := generateAgentID()
		callID, _ := ctx.Value(tool.ExecutionCallIDKey{}).(string)
		spec := Spec{
			Task:         task,
			AgentType:    AgentTypeVision,
			SystemPrompt: AgentSystemPrompt(AgentTypeVision),
			ParentCallID: callID,
			AgentID:      agentID,
			Images:       []provider.ImageBlock{imgBlock},
		}

		allowedTools, resolvedProvider, resolvedModel, err := resolveToolsAndModel(AgentTypeVision, deps)
		if err != nil {
			emitDelegateFailed(deps.Events, spec, AgentTypeVision, err.Error())
			return nil, childSetupError(err)
		}

		req, limits, err := BuildChildRun(ctx, deps.SubAgentHandlerDeps, ChildBootstrapOverrides{
			AgentType:     AgentTypeVision,
			AllowedTools:  allowedTools,
			Provider:      resolvedProvider,
			ResolvedModel: resolvedModel,
			ProjectRoot:   deps.WorkDir,
		}, spec)
		if err != nil {
			err = fmt.Errorf("vision: build child run: %w", err)
			emitDelegateFailed(deps.Events, spec, AgentTypeVision, err.Error())
			return nil, childSetupError(err)
		}
		spec.Limits = limits
		result, err := runRegisteredDelegate(ctx, deps, spec, req, CodeWorktree{}, nil, nil, "vision", func(result tool.ExecutionResult) tool.ExecutionResult {
			return result
		})
		if err != nil {
			return nil, err
		}
		return result, nil
	}
}

// loadVisionImageBlock reads the image identified by imageID from the store,
// base64-encodes it, and returns a provider.ImageBlock ready for sub-agent injection.
func loadVisionImageBlock(imageID string, store *agent.ImageStore) (provider.ImageBlock, error) {
	if store == nil {
		return provider.ImageBlock{}, fmt.Errorf("vision: ImageStore is not configured")
	}
	ref, ok := store.Get(imageID)
	if !ok {
		return provider.ImageBlock{}, fmt.Errorf("vision: unknown image_id %q", imageID)
	}
	data, err := os.ReadFile(ref.FilePath)
	if err != nil {
		return provider.ImageBlock{}, fmt.Errorf("vision: read image: %w", err)
	}
	return provider.ImageBlock{
		MediaType: ref.MediaType,
		Data:      base64.StdEncoding.EncodeToString(data),
		Width:     ref.Width,
		Height:    ref.Height,
		SizeBytes: ref.SizeBytes,
	}, nil
}
