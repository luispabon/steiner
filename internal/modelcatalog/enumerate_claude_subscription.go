package modelcatalog

import "context"

// ClaudeSubscriptionDiscover discovers models from a signed-in Claude subscription.
type ClaudeSubscriptionDiscover func(context.Context) ([]DiscoveredModel, error)

// ClaudeSubscriptionEnumerator discovers models from the Claude subscription CLI.
type ClaudeSubscriptionEnumerator struct {
	discover ClaudeSubscriptionDiscover
}

// NewClaudeSubscriptionEnumerator creates a Claude subscription model enumerator.
func NewClaudeSubscriptionEnumerator(discover ClaudeSubscriptionDiscover) *ClaudeSubscriptionEnumerator {
	return &ClaudeSubscriptionEnumerator{discover: discover}
}

// Enumerate discovers Claude subscription models, falling back to the static
// catalog when discovery is unavailable or returns no usable models.
func (e *ClaudeSubscriptionEnumerator) Enumerate(ctx context.Context, ep Endpoint, _ EnumerationOptions) (EnumerationResult, error) {
	if e != nil && e.discover != nil {
		models, err := e.discover(ctx)
		if err == nil && len(models) > 0 {
			return EnumerationResult{Models: stampClaudeSubscriptionModels(ep, models)}, nil
		}
	}
	return EnumerationResult{Models: claudeSubscriptionFallback(ep)}, nil
}

func stampClaudeSubscriptionModels(ep Endpoint, discovered []DiscoveredModel) []DiscoveredModel {
	models := make([]DiscoveredModel, len(discovered))
	copy(models, discovered)
	for index := range models {
		models[index].ProviderAlias = ep.Alias
		models[index].ProviderType = ep.Type
	}
	return models
}

func claudeSubscriptionFallback(ep Endpoint) []DiscoveredModel {
	fallback := []struct {
		id          string
		displayName string
	}{
		{"claude-opus-5-5", "Opus 5.5"},
		{"claude-sonnet-5-5", "Sonnet 5.5"},
		{"claude-haiku-5-5", "Haiku 5.5"},
		{"claude-fable-5-1", "Fable 5.1"},
	}
	models := make([]DiscoveredModel, 0, len(fallback))
	for _, item := range fallback {
		models = append(models, DiscoveredModel{
			ProviderAlias:    ep.Alias,
			ProviderType:     ep.Type,
			ID:               item.id,
			DisplayName:      item.displayName,
			SupportedEfforts: []string{"low", "medium", "high", "xhigh", "max"},
		})
	}
	return models
}
