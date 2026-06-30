package delegation

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"

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
		if deps.ImageStore == nil {
			return nil, fmt.Errorf("vision: ImageStore is not configured")
		}

		ref, ok := deps.ImageStore.Get(imageID)
		if !ok {
			return nil, fmt.Errorf("vision: unknown image_id %q", imageID)
		}

		data, err := os.ReadFile(ref.FilePath)
		if err != nil {
			return nil, fmt.Errorf("vision: read image: %w", err)
		}
		encoded := base64.StdEncoding.EncodeToString(data)

		agentID := generateAgentID()
		spec := DelegationSpec{
			Task:         task,
			SystemPrompt: AgentSystemPrompt(AgentTypeVision),
			AgentID:      agentID,
			Images: []provider.ImageBlock{{
				MediaType: ref.MediaType,
				Data:      encoded,
				Width:     ref.Width,
				Height:    ref.Height,
				SizeBytes: ref.SizeBytes,
			}},
		}

		// Build a modified SubAgentConfig with the vision tool allowlist.
		typedCfg := deps.SubAgentCfg
		typedCfg.AllowedTools = AgentAllowedTools(AgentTypeVision)

		// Resolve per-type model if configured.
		resolvedProvider := deps.Provider
		resolvedModel := deps.ResolvedModel
		if deps.ModelResolver != nil {
			if ac, ok := deps.SubAgentCfg.Agents[string(AgentTypeVision)]; ok && ac.Model != "" {
				p, rm, err := deps.ModelResolver(ac.Model)
				if err != nil {
					return nil, fmt.Errorf("vision: resolve model %q: %w", ac.Model, err)
				}
				resolvedProvider = p
				resolvedModel = rm
			}
		}

		req, limits, err := BuildChildRun(ctx, BootstrapDeps{
			Provider:             resolvedProvider,
			ParentReg:            deps.ParentReg,
			SubAgentCfg:          typedCfg,
			Events:               deps.Events,
			WorkDir:              deps.WorkDir,
			HomeDir:              deps.HomeDir,
			ProjectContextConfig: deps.ProjectContextConfig,
			ResolvedModel:        resolvedModel,
			MaxTokens:            deps.MaxTokens,
			StreamingPreferred:   deps.StreamingPreferred,
			CaveHuman:            deps.CaveHuman,
			Sandbox:              deps.Sandbox,
			UsageRecorder:        deps.UsageRecorder,
		}, spec)
		if err != nil {
			return nil, fmt.Errorf("vision: build child run: %w", err)
		}
		spec.Limits = limits

		result, state, err := SpawnDelegate(ctx, spec, req, deps.Runner, deps.Events, deps.TraceLogger)
		if err == nil && deps.SessionStore != nil {
			saveChildSession(deps.SessionStore, spec, req, state)
		}
		if err != nil {
			if result != (tool.ExecutionResult{}) {
				return result, nil
			}
			return nil, fmt.Errorf("vision failed: %w", err)
		}

		// Append follow-up guidance so the caller knows how to resume.
		if dr, ok := result.Value.(DelegationResult); ok {
			dr.Output += fmt.Sprintf("\n\nTo ask follow-up questions about this image, use follow_up with agent_id: %q", agentID)
			result.Value = dr
		}

		return result, nil
	}
}
