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
// DelegationSpec, and spawns a child agent. The result includes the agent_id so
// the caller can use follow_up for additional questions about the same image.
func newVisionHandler(deps SpecializedToolDeps) func(ctx context.Context, input map[string]any) (any, error) {
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
		spec := DelegationSpec{
			Task:         task,
			SystemPrompt: AgentSystemPrompt(AgentTypeVision),
			ParentCallID: callID,
			AgentID:      agentID,
			Images:       []provider.ImageBlock{imgBlock},
		}

		resolvedProvider := deps.Provider
		resolvedModel := deps.ResolvedModel
		if deps.ModelResolver != nil {
			if alias, ok := deps.AgentModels[string(AgentTypeVision)]; ok && alias != "" {
				p, rm, err := deps.ModelResolver(alias)
				if err != nil {
					return nil, fmt.Errorf("vision: resolve model %q: %w", alias, err)
				}
				resolvedProvider = p
				resolvedModel = rm
			}
		}

		allowedTools := AgentAllowedTools(AgentTypeVision)
		if deps.ExtraAllowedTools != nil {
			allowedTools = mergedAllowedTools(allowedTools, deps.ExtraAllowedTools[AgentTypeVision])
		}

		req, limits, err := BuildChildRun(ctx, deps.SubAgentHandlerDeps, ChildBootstrapOverrides{
			AgentType:     AgentTypeVision,
			AllowedTools:  allowedTools,
			Provider:      resolvedProvider,
			ResolvedModel: resolvedModel,
		}, spec)
		if err != nil {
			return nil, fmt.Errorf("vision: build child run: %w", err)
		}
		var gateRelease func()
		req.Events, gateRelease = applyDispatchGate(ctx, deps.CacheKeyStore, req.PromptCacheKey, spec.AgentID, spec.ParentCallID, deps.Events, req.Events)
		defer gateRelease()
		spec.Limits = limits

		result, state, runUsage, err := SpawnDelegate(ctx, spec, req, deps.Runner, deps.Events, deps.TraceLogger)
		if err == nil && deps.SessionStore != nil {
			saveChildSession(deps.SessionStore, spec, req, state, runUsage, nil)
		}
		if err != nil {
			if result != (tool.ExecutionResult{}) {
				return result, nil
			}
			return nil, fmt.Errorf("vision failed: %w", err)
		}

		if dr, ok := result.Value.(DelegationResult); ok {
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
