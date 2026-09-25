package provider

import (
	"strings"
)

func chatRequestWire(request ChatRequest, defaultModel string, stream bool) (openAIRequest, error) {
	wire := openAIRequest{
		Model:       defaultModel,
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
	wire.Tools = toOpenAITools(request.Tools)
	messages, err := toOpenAIWireMessages(request.Messages)
	if err != nil {
		return openAIRequest{}, err
	}
	wire.Messages = messages
	if request.IncludeEmptyReasoning {
		fillEmptyReasoningContent(wire.Messages)
	}
	return wire, nil
}

// toOpenAITools converts request tool specs to wire tools, or nil when there are
// none.
func toOpenAITools(tools []ToolSpec) []openAITool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openAITool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, openAITool{
			Type: tool.Type,
			Function: openAIToolFunction{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  tool.Function.Parameters,
			},
		})
	}
	return out
}

// toOpenAIWireMessages converts request messages to wire messages. OpenAI
// requires the tool messages answering an assistant turn's tool_calls to follow
// it contiguously. A tool result's image parts would break that run, so they are
// deferred and emitted as one user message once the run ends.
func toOpenAIWireMessages(messages []Message) ([]openAIMessage, error) {
	out := make([]openAIMessage, 0, len(messages))
	var pendingImageParts []openAIContentPart
	flushImages := func() {
		if len(pendingImageParts) == 0 {
			return
		}
		out = append(out, openAIMessage{Role: "user", Content: pendingImageParts})
		pendingImageParts = nil
	}
	for _, msg := range messages {
		if msg.Role != MessageRoleTool {
			flushImages()
		}
		wireMsgs, err := toOpenAIMessages(msg)
		if err != nil {
			return nil, err
		}
		if msg.Role == MessageRoleTool && len(wireMsgs) > 1 {
			out = append(out, wireMsgs[0])
			for _, extra := range wireMsgs[1:] {
				if parts, ok := extra.Content.([]openAIContentPart); ok {
					pendingImageParts = append(pendingImageParts, parts...)
				}
			}
			continue
		}
		out = append(out, wireMsgs...)
	}
	flushImages()
	return out, nil
}

// fillEmptyReasoningContent sets a non-nil empty reasoning_content on assistant
// messages that lack one, keeping the field present in the serialized request.
func fillEmptyReasoningContent(messages []openAIMessage) {
	empty := ""
	for i := range messages {
		if messages[i].Role == "assistant" && messages[i].ReasoningContent == nil {
			messages[i].ReasoningContent = &empty
		}
	}
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
