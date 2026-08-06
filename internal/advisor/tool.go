package advisor

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/usagestats"
)

// ToolName is the registered provider-facing name of the advisor tool.
const ToolName = "advisor"

// BudgetExhaustedMessage returns the exact model-visible message used when the
// per-run advisor cap is exhausted.
func BudgetExhaustedMessage(used, max int) string {
	return fmt.Sprintf("advisor budget exhausted for this run (%d/%d); proceed on your own judgment", used, max)
}

// ToolDef returns the provider-facing advisor tool definition.
func ToolDef(handler func(context.Context, map[string]any) (any, error)) tool.ToolDef {
	return tool.ToolDef{
		Name:        ToolName,
		Description: "Ask a stronger-model steering advisor for concise strategic guidance. Advisory only: it does not mutate code, execute tools, or act as a sub-agent.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "What you want the advisor to judge. Free text.",
				},
				"files": map[string]any{
					"type":        "array",
					"description": "Workspace paths to include verbatim in the advisor's context. The advisor has no tool access and cannot read files itself, so pass the paths of any artifact you want it to review.",
					"items":       map[string]any{"type": "string"},
				},
			},
			"additionalProperties": false,
		},
		Handler: handler,
	}
}

// HandlerDeps holds the runtime dependencies for one advisor tool instance.
type HandlerDeps struct {
	Provider      provider.Provider
	Model         provider.ResolvedModel
	Events        output.EventSink
	Config        Config
	UsageRecorder *usagestats.Recorder
	// WorkDir is the working directory used to render workspace-relative
	// display paths for caller-supplied files.
	WorkDir string
	// PathPolicy resolves and constrains caller-supplied file paths using the
	// same policy the read tool enforces. Required only when a caller passes
	// files.
	PathPolicy *tool.PathPolicy
}

// Config configures the per-run advisor handler.
type Config struct {
	MaxUsesPerRun int
	MaxTokens     *int
}

// NewHandler returns a fresh per-run advisor handler.
func NewHandler(deps HandlerDeps) func(context.Context, map[string]any) (any, error) {
	cacheKey, _ := provider.NewPromptCacheKey()
	state := &handlerState{cacheKey: cacheKey}
	return func(ctx context.Context, input map[string]any) (any, error) {
		return state.handle(ctx, deps, input)
	}
}

type handlerState struct {
	mu       sync.Mutex
	uses     int
	cacheKey string
}

func (s *handlerState) handle(ctx context.Context, deps HandlerDeps, input map[string]any) (any, error) {
	in, err := decodeAdvisorInput(input)
	if err != nil {
		return nil, fmt.Errorf("advisor: %w", err)
	}
	files, err := loadAdvisorFiles(deps.WorkDir, deps.PathPolicy, in.Files)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	nextUse := s.uses + 1
	maxUses := deps.Config.MaxUsesPerRun
	if nextUse > maxUses {
		used := s.uses
		s.mu.Unlock()
		message := BudgetExhaustedMessage(used, maxUses)
		emitEvent(deps.Events, output.NewAdvisorBudgetExhaustedEvent(deps.Model.BackendModelID, used, maxUses, message))
		return message, nil
	}
	s.uses = nextUse
	s.mu.Unlock()

	snapshot, ok := agent.ConversationSnapshotFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("advisor: live conversation snapshot missing from context")
	}

	// Keep the provider-visible tool list stable for the whole run so prompt/KV
	// cache prefixes stay reusable. The per-run cap lives in handler state on
	// purpose, even though Anthropic guidance often suggests removing spent tools.
	emitEvent(deps.Events, output.NewAdvisorStartedEvent(deps.Model.BackendModelID, nextUse, maxUses))
	response, err := advise(ctx, deps.Provider, deps.Model, snapshot, in.Question, files, deps.Config.MaxTokens, deps.Events, s.cacheKey)
	if err != nil {
		emitEvent(deps.Events, output.NewAdvisorCompleteEvent(deps.Model.BackendModelID, nextUse, maxUses, "", false, err, 0, 0))
		return nil, err
	}

	recordAdvisorUsage(deps.UsageRecorder, deps.Model, response.Usage)

	note := strings.TrimSpace(response.Message.Content)
	truncated := response.FinishReason == "length"

	if truncated {
		note += "\n\n[advisor response truncated — raise advisor.max_tokens in config]"
	}
	if note == "" {
		note = "advisor returned no content"
	}

	var cacheReadTokens, cacheCreateTokens int
	if response.Usage != nil {
		cacheReadTokens = response.Usage.CacheReadInputTokens
		cacheCreateTokens = response.Usage.CacheCreationInputTokens
	}

	emitEvent(deps.Events, output.NewAdvisorCompleteEvent(deps.Model.BackendModelID, nextUse, maxUses, note, truncated, nil, cacheReadTokens, cacheCreateTokens))
	return note, nil
}

// recordAdvisorUsage records one observation for a usage-bearing advisor
// response. No-op when the recorder is unset or the response carried no
// usage, mirroring internal/agent's recordModelUsage.
func recordAdvisorUsage(recorder *usagestats.Recorder, model provider.ResolvedModel, usage *provider.UsageStats) {
	if recorder == nil || usage == nil {
		return
	}
	recorder.Record(usagestats.Observation{
		ProviderAlias:     model.ProviderAlias,
		ProviderType:      string(model.EffectiveProviderType),
		BackendModelID:    model.BackendModelID,
		PromptTokens:      usage.PromptTokens,
		CompletionTokens:  usage.CompletionTokens,
		CacheReadTokens:   usage.CacheReadInputTokens,
		CacheCreateTokens: usage.CacheCreationInputTokens,
	})
}

func emitEvent(sink output.EventSink, event output.Event) {
	if sink != nil {
		sink.Emit(event)
	}
}

func advise(ctx context.Context, prov provider.Provider, rm provider.ResolvedModel, conversation []provider.Message, question string, files []advisorFile, maxTokens *int, events output.EventSink, cacheKey string) (provider.ChatResponse, error) {
	if prov == nil {
		return provider.ChatResponse{}, fmt.Errorf("advisor: provider is required")
	}
	if strings.TrimSpace(rm.BackendModelID) == "" {
		return provider.ChatResponse{}, fmt.Errorf("advisor: model is required")
	}

	req := provider.ChatRequest{
		Model:          rm.BackendModelID,
		Messages:       buildMessages(conversation, question, files),
		MaxTokens:      maxTokens,
		Params:         rm.Params,
		ExtraParams:    rm.ExtraParams,
		PromptCacheKey: cacheKey,
	}
	if rm.ReasoningEffectiveEffort != "" {
		req.Reasoning = &provider.ReasoningRequest{Effort: rm.ReasoningEffectiveEffort}
	}

	if req.Reasoning != nil {
		// Streaming is required for reasoning models. Go directly to streaming
		// so the provider produces thinking chunks.
		stream, err := prov.StreamChatCompletion(ctx, req)
		if err != nil {
			return provider.ChatResponse{}, fmt.Errorf("advisor: %w", err)
		}
		resp, drainErr := streamWithEvents(stream, events)
		if drainErr != nil {
			return provider.ChatResponse{}, fmt.Errorf("advisor: %w", drainErr)
		}
		return resp, nil
	}

	response, err := prov.ChatCompletion(ctx, req)
	if err != nil {
		// Fall back to streaming if the provider rejects non-stream requests.
		if agent.IsStreamRequiredError(err) {
			stream, streamErr := prov.StreamChatCompletion(ctx, req)
			if streamErr != nil {
				return provider.ChatResponse{}, fmt.Errorf("advisor: %w", streamErr)
			}
			resp, drainErr := streamWithEvents(stream, events)
			if drainErr != nil {
				return provider.ChatResponse{}, fmt.Errorf("advisor: %w", drainErr)
			}
			return resp, nil
		}
		return provider.ChatResponse{}, fmt.Errorf("advisor: %w", err)
	}
	return response, nil
}
