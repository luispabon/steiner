package interactive

import "github.com/luispabon/steiner/internal/provider"

func cloneProviderMessages(messages []provider.Message) []provider.Message {
	return provider.CloneMessages(messages)
}

func cloneProviderTools(tools []provider.ToolSpec) []provider.ToolSpec {
	return provider.CloneTools(tools)
}
