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
		task, _ := input["task"].(string)
		if task == "" {
			return nil, fmt.Errorf("vision: task is required")
		}
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
			return nil, err
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
			return nil, err
		}
		spec.Limits = limits
		worktree := CodeWorktree{}
		childCtx, err := deps.ActiveController.Register(agentID, ctx, AgentTypeVision, worktree)
		if err != nil {
			cleanupRegistrationWorktree(AgentTypeVision, deps.WorkDir, worktree)
			return nil, err
		}
		defer deps.ActiveController.Unregister(agentID)
		emitDelegateStarted(deps.Events, spec, req.ResolvedModel.Alias, AgentTypeVision)
		var gateRelease func()
		req.Events, gateRelease = applyDispatchGate(childCtx, deps.CacheKeyStore, req.PromptCacheKey, spec.AgentID, spec.ParentCallID, deps.Events, req.Events)
		defer gateRelease()
		if childCtx.Err() != nil {
			emitDelegateStopped(deps.Events, spec, AgentTypeVision)
			result := cancelledBeforeDispatchResult(spec.AgentID)
			applyFinalizeCancellation(deps.Events, deps.SessionStore, deps.ActiveController, deps.WorkDir, spec.AgentID, &result)
			return result, nil
		}

		result, state, runUsage, err := SpawnDelegate(childCtx, spec, req, deps.Runner, deps.Events, deps.TraceLogger, withChildDone(func() { deps.ActiveController.MarkComplete(spec.AgentID) }))
		if err == nil && deps.SessionStore != nil {
			saveChildSession(deps.SessionStore, spec, req, state, runUsage, nil)
		}
		applyFinalizeCancellation(deps.Events, deps.SessionStore, deps.ActiveController, deps.WorkDir, spec.AgentID, &result)
		if err != nil {
			if result != (tool.ExecutionResult{}) {
				return result, nil
			}
			return nil, fmt.Errorf("vision failed: %w", err)
		}

		if dr, ok := result.Value.(Result); ok {
			dr.Output += fmt.Sprintf("\n\nTo ask follow-up questions about this image, use follow_up with agent_id: %q", agentID)
			result.Value = dr
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
