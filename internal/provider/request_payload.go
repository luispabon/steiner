package provider

import (
	"strings"
)

func chatRequestWire(request ChatRequest, defaultModel string, stream bool) (openAIRequest, error) {
	wire := openAIRequest{
		Model:       defaultModel,
		Messages:    make([]openAIMessage, 0, len(request.Messages)),
		MaxTokens:   request.MaxTokens,
		Stream:      stream,
		Reasoning:   request.Reasoning,
		Params:      request.Params,
		ExtraParams: request.ExtraParams,
	}
	if strings.TrimSpace(request.Model) != "" {
		wire.Model = request.Model
	}
	if stream {
		wire.StreamOptions = &openAIStreamOptions{IncludeUsage: true}
	}
	if len(request.Tools) > 0 {
		wire.Tools = make([]openAITool, 0, len(request.Tools))
		for _, tool := range request.Tools {
			wire.Tools = append(wire.Tools, openAITool{
				Type: tool.Type,
				Function: openAIToolFunction{
					Name:        tool.Function.Name,
					Description: tool.Function.Description,
					Parameters:  tool.Function.Parameters,
				},
			})
		}
	}
	for _, msg := range request.Messages {
		wireMsgs, err := toOpenAIMessages(msg)
		if err != nil {
			return openAIRequest{}, err
		}
		wire.Messages = append(wire.Messages, wireMsgs...)
	}
	if request.IncludeEmptyReasoning {
		empty := ""
		for i := range wire.Messages {
			if wire.Messages[i].Role == "assistant" && wire.Messages[i].ReasoningContent == nil {
				wire.Messages[i].ReasoningContent = &empty
			}
		}
	}
	return wire, nil
}

func normalizedTokenCount(usage *UsageStats) int {
	if usage == nil {
		return 0
	}
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	if usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
		return usage.PromptTokens + usage.CompletionTokens
	}
	return 0
}

// UsageCompletionTokenCount returns the completion/output token count from usage
// stats. This is the number of tokens the model generated, excluding the input
// prompt. Returns 0 when unavailable.
func UsageCompletionTokenCount(usage *UsageStats) int {
	if usage == nil {
		return 0
	}
	if usage.CompletionTokens > 0 {
		return usage.CompletionTokens
	}
	return 0
}

// NonCachedPromptTokens returns prompt tokens not covered by provider cache reads
// or cache creation. Negative results are clamped to zero.
func (u UsageStats) NonCachedPromptTokens() int {
	return max(0, u.PromptTokens-u.CacheReadInputTokens-u.CacheCreationInputTokens)
}
