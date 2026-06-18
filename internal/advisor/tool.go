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
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
		Handler: handler,
	}
}

// HandlerDeps holds the runtime dependencies for one advisor tool instance.
type HandlerDeps struct {
	Provider provider.Provider
	Model    provider.ResolvedModel
	Events   output.EventSink
	Config   Config
}

// Config configures the per-run advisor handler.
type Config struct {
	MaxUsesPerRun int
	MaxTokens     *int
}

// NewHandler returns a fresh per-run advisor handler.
func NewHandler(deps HandlerDeps) func(context.Context, map[string]any) (any, error) {
	state := &handlerState{}
	return func(ctx context.Context, _ map[string]any) (any, error) {
		return state.handle(ctx, deps)
	}
}

type handlerState struct {
	mu   sync.Mutex
	uses int
}

func (s *handlerState) handle(ctx context.Context, deps HandlerDeps) (any, error) {
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
	response, err := Advise(ctx, Request{
		Provider:     deps.Provider,
		Model:        deps.Model.BackendModelID,
		Conversation: snapshot,
		MaxTokens:    deps.Config.MaxTokens,
	})
	if err != nil {
		emitEvent(deps.Events, output.NewAdvisorCompleteEvent(deps.Model.BackendModelID, nextUse, maxUses, "", false, err))
		return nil, err
	}

	note := strings.TrimSpace(response.Message.Content)
	truncated := response.FinishReason == "length"

	if truncated {
		note += "\n\n[advisor response truncated — raise advisor.max_tokens in config]"
	}
	if note == "" {
		note = "advisor returned no content"
	}

	emitEvent(deps.Events, output.NewAdvisorCompleteEvent(deps.Model.BackendModelID, nextUse, maxUses, note, truncated, nil))
	return note, nil
}

func emitEvent(sink output.EventSink, event output.Event) {
	if sink != nil {
		sink.Emit(event)
	}
}
