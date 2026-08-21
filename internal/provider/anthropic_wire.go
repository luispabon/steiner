package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

type anthropicRequest struct {
	Model       string                  `json:"model"`
	System      []anthropicContentBlock `json:"system,omitempty"`
	Messages    []anthropicMessage      `json:"messages"`
	MaxTokens   *int                    `json:"max_tokens,omitempty"`
	Stream      bool                    `json:"stream,omitempty"`
	Tools       []anthropicTool         `json:"tools,omitempty"`
	Params      map[string]any          `json:"-"`
	ExtraParams map[string]any          `json:"-"`
}

func (r anthropicRequest) MarshalJSON() ([]byte, error) {
	base := map[string]any{
		"model":    r.Model,
		"messages": r.Messages,
	}
	if len(r.System) > 0 {
		base["system"] = r.System
	}
	if r.MaxTokens != nil {
		base["max_tokens"] = *r.MaxTokens
	}
	if r.Stream {
		base["stream"] = true
	}
	if len(r.Tools) > 0 {
		base["tools"] = r.Tools
	}
	m := mergeRequestParams(base, r.Params, r.ExtraParams)
	return json.Marshal(m)
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// anthropicAdvisorCacheTTL is the extended ephemeral cache TTL used for
// advisor-shaped requests, in place of the provider's default 5m window.
const anthropicAdvisorCacheTTL = "1h"

// anthropicAdvisorBreakpointSpacing is the number of content blocks between
// intermediate cache breakpoints placed in the reusable conversation tail of
// an advisor-shaped request. It must stay at or below the provider's 20-block
// backward lookback so a later request's breakpoint can still find an
// earlier one's cached entry.
const anthropicAdvisorBreakpointSpacing = 15

type anthropicTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  map[string]any         `json:"input_schema,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"` // always "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicContentBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	Thinking     string                 `json:"thinking,omitempty"`
	Signature    string                 `json:"signature,omitempty"`
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        map[string]any         `json:"input,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	Content      string                 `json:"content,omitempty"`
	Source       *anthropicImageSource  `json:"source,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

func (b anthropicContentBlock) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"type": b.Type,
	}
	if b.Text != "" {
		m["text"] = b.Text
	}
	if b.Thinking != "" {
		m["thinking"] = b.Thinking
	}
	if b.Signature != "" {
		m["signature"] = b.Signature
	}
	if b.ID != "" {
		m["id"] = b.ID
	}
	if b.Name != "" {
		m["name"] = b.Name
	}
	if b.Input != nil {
		m["input"] = b.Input
	}
	if b.ToolUseID != "" {
		m["tool_use_id"] = b.ToolUseID
	}
	if b.Content != "" {
		m["content"] = b.Content
	}
	if b.Source != nil {
		m["source"] = b.Source
	}
	if b.CacheControl != nil {
		m["cache_control"] = b.CacheControl
	}
	return json.Marshal(m)
}

type anthropicResponse struct {
	Role       string                  `json:"role,omitempty"`
	Content    []anthropicContentBlock `json:"content,omitempty"`
	StopReason string                  `json:"stop_reason,omitempty"`
	Usage      *anthropicUsage         `json:"usage,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

func anthropicRequestWire(request ChatRequest, defaultModel string, stream bool) anthropicRequest {
	wire := anthropicRequest{
		Model:       defaultModel,
		Messages:    make([]anthropicMessage, 0, len(request.Messages)),
		Stream:      stream,
		Params:      request.Params,
		ExtraParams: request.ExtraParams,
	}
	if request.MaxTokens != nil {
		wire.MaxTokens = request.MaxTokens
	} else {
		defaultMax := 4096
		wire.MaxTokens = &defaultMax
	}
	if strings.TrimSpace(request.Model) != "" {
		wire.Model = request.Model
	}
	if len(request.Tools) > 0 {
		wire.Tools = make([]anthropicTool, 0, len(request.Tools))
		for _, tool := range request.Tools {
			wire.Tools = append(wire.Tools, anthropicTool{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				InputSchema: tool.Function.Parameters,
			})
		}
	}
	for _, msg := range request.Messages {
		switch msg.Role {
		case MessageRoleSystem:
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			wire.System = append(wire.System, anthropicContentBlock{
				Type: "text",
				Text: msg.Content,
			})
		default:
			wireMsg := toAnthropicMessage(msg)
			if wireMsg != nil {
				wire.Messages = append(wire.Messages, *wireMsg)
			}
		}
	}

	if request.AdvisorCacheProfile {
		assignAdvisorCacheBreakpoints(&wire)
	} else {
		assignCacheBreakpoints(&wire)
	}
	return wire
}

func assignCacheBreakpoints(wire *anthropicRequest) {
	cacheControl := &anthropicCacheControl{Type: "ephemeral"}
	numBreakpoints := 0

	// STATIC PREFIX: Mark last system block (caches tools+system), or last tool if no system.
	if len(wire.System) > 0 {
		wire.System[len(wire.System)-1].CacheControl = cacheControl
		numBreakpoints++
	} else if len(wire.Tools) > 0 {
		wire.Tools[len(wire.Tools)-1].CacheControl = cacheControl
		numBreakpoints++
	}

	// ROLLING CONVERSATION: Find last two user-turn boundaries and mark final message's last block.
	if len(wire.Messages) == 0 {
		return
	}

	// Mark the last content block of the final message.
	if len(wire.Messages[len(wire.Messages)-1].Content) > 0 && numBreakpoints < 4 {
		wire.Messages[len(wire.Messages)-1].Content[len(wire.Messages[len(wire.Messages)-1].Content)-1].CacheControl = cacheControl
		numBreakpoints++
	}

	// Find second-to-last user message and mark its last content block (avoid duplicating if it's the same as final message).
	userMsgIndices := lastNUserMsgIndices(wire.Messages, 2)
	if len(userMsgIndices) < 2 || numBreakpoints >= 4 {
		return
	}
	secondLastUserMsgIdx := userMsgIndices[0]
	if len(wire.Messages[secondLastUserMsgIdx].Content) > 0 {
		wire.Messages[secondLastUserMsgIdx].Content[len(wire.Messages[secondLastUserMsgIdx].Content)-1].CacheControl = cacheControl
	}
}

// assignAdvisorCacheBreakpoints places cache breakpoints for advisor-shaped
// requests: the last system block, then breakpoints spaced every
// anthropicAdvisorBreakpointSpacing content blocks walking backward from the
// end of the reusable conversation tail (i.e. excluding the final message,
// which is the per-call unique suffix and can never be read back). All
// breakpoints carry the extended anthropicAdvisorCacheTTL.
func assignAdvisorCacheBreakpoints(wire *anthropicRequest) {
	cacheControl := &anthropicCacheControl{Type: "ephemeral", TTL: anthropicAdvisorCacheTTL}
	numBreakpoints := 0

	if len(wire.System) > 0 {
		wire.System[len(wire.System)-1].CacheControl = cacheControl
		numBreakpoints++
	} else if len(wire.Tools) > 0 {
		wire.Tools[len(wire.Tools)-1].CacheControl = cacheControl
		numBreakpoints++
	}

	// Exclude the final message (the unique per-call suffix) from the
	// reusable tail eligible for breakpoints.
	if len(wire.Messages) <= 1 {
		return
	}
	tail := wire.Messages[:len(wire.Messages)-1]

	// distanceFromEnd counts content blocks walking backward from the last
	// block of the tail (distance 0). Breakpoints land at distance 0, 15,
	// 30, 45, ... until the 4-breakpoint budget is spent.
	distanceFromEnd := 0
	for i := len(tail) - 1; i >= 0 && numBreakpoints < 4; i-- {
		for j := len(tail[i].Content) - 1; j >= 0 && numBreakpoints < 4; j-- {
			if distanceFromEnd%anthropicAdvisorBreakpointSpacing == 0 {
				tail[i].Content[j].CacheControl = cacheControl
				numBreakpoints++
			}
			distanceFromEnd++
		}
	}
}

func lastNUserMsgIndices(messages []anthropicMessage, n int) []int {
	if n <= 0 || len(messages) == 0 {
		return nil
	}
	indices := make([]int, 0, n)
	for i := len(messages) - 1; i >= 0 && len(indices) < n; i-- {
		if messages[i].Role != "user" {
			continue
		}
		indices = append(indices, i)
	}
	for left, right := 0, len(indices)-1; left < right; left, right = left+1, right-1 {
		indices[left], indices[right] = indices[right], indices[left]
	}
	return indices
}

func toAnthropicMessage(message Message) *anthropicMessage {
	switch message.Role {
	case MessageRoleUser:
		return userMessageToAnthropic(message)
	case MessageRoleAssistant:
		return assistantMessageToAnthropic(message)
	case MessageRoleTool:
		return toolMessageToAnthropic(message)
	default:
		return genericMessageToAnthropic(message)
	}
}

func userMessageToAnthropic(message Message) *anthropicMessage {
	content := make([]anthropicContentBlock, 0, 1+len(message.Images))
	if message.Content != "" {
		content = append(content, anthropicContentBlock{Type: "text", Text: message.Content})
	}
	content = appendImageBlocks(content, message.Images)
	if len(content) == 0 {
		// keep at least empty text block to avoid empty content array
		content = append(content, anthropicContentBlock{Type: "text", Text: ""})
	}
	return &anthropicMessage{Role: "user", Content: content}
}

func assistantMessageToAnthropic(message Message) *anthropicMessage {
	content := make([]anthropicContentBlock, 0, 1+len(message.ToolCalls))
	if thinking := assistantThinkingBlock(message); thinking != nil {
		content = append(content, *thinking)
	}
	if message.Content != "" {
		content = append(content, anthropicContentBlock{
			Type: "text",
			Text: message.Content,
		})
	}
	for _, toolCall := range message.ToolCalls {
		content = append(content, anthropicContentBlock{
			Type:  "tool_use",
			ID:    toolCall.ID,
			Name:  toolCall.Name,
			Input: anthropicToolInput(toolCall.Arguments),
		})
	}
	if len(content) == 0 {
		return nil
	}
	return &anthropicMessage{Role: "assistant", Content: content}
}

func assistantThinkingBlock(message Message) *anthropicContentBlock {
	if message.ReasoningContent == "" {
		return nil
	}
	// The Anthropic Messages API rejects replayed thinking blocks that
	// lack a signature. Only emit the block when we captured one;
	// otherwise drop the reasoning text rather than send an invalid
	// request.
	var signature string
	if metadata := message.ProviderMetadata; metadata != nil && metadata.Anthropic != nil {
		signature = metadata.Anthropic.ThinkingSignature
	}
	if strings.TrimSpace(signature) == "" {
		return nil
	}
	return &anthropicContentBlock{
		Type:      "thinking",
		Thinking:  message.ReasoningContent,
		Signature: signature,
	}
}

func toolMessageToAnthropic(message Message) *anthropicMessage {
	content := []anthropicContentBlock{{
		Type:      "tool_result",
		ToolUseID: message.ToolCallID,
		Content:   message.Content,
	}}
	content = appendImageBlocks(content, message.Images)
	return &anthropicMessage{Role: "user", Content: content}
}

func genericMessageToAnthropic(message Message) *anthropicMessage {
	if strings.TrimSpace(message.Content) == "" {
		return nil
	}
	return &anthropicMessage{
		Role: string(message.Role),
		Content: []anthropicContentBlock{{
			Type: "text",
			Text: message.Content,
		}},
	}
}

func appendImageBlocks(content []anthropicContentBlock, images []ImageBlock) []anthropicContentBlock {
	for _, img := range images {
		content = append(content, anthropicContentBlock{
			Type: "image",
			Source: &anthropicImageSource{
				Type:      "base64",
				MediaType: img.MediaType,
				Data:      img.Data,
			},
		})
	}
	return content
}

func normalizeAnthropicChatResponse(payload *anthropicResponse) (ChatResponse, error) {
	if payload == nil {
		return ChatResponse{}, fmt.Errorf("empty provider response")
	}
	message, err := normalizeAnthropicMessage(payload.Role, payload.Content)
	if err != nil {
		return ChatResponse{}, err
	}
	return ChatResponse{
		Message:      message,
		Usage:        payload.Usage.toUsageStats(),
		FinishReason: normalizeAnthropicFinishReason(payload.StopReason),
	}, nil
}

func normalizeAnthropicMessage(role string, content []anthropicContentBlock) (Message, error) {
	out := Message{Role: MessageRoleAssistant}
	if strings.TrimSpace(role) != "" {
		out.Role = MessageRole(role)
	}
	for _, block := range content {
		switch block.Type {
		case "thinking":
			out.ReasoningContent += block.Thinking
			if strings.TrimSpace(block.Signature) != "" {
				if out.ProviderMetadata == nil {
					out.ProviderMetadata = &MessageProviderMetadata{}
				}
				if out.ProviderMetadata.Anthropic == nil {
					out.ProviderMetadata.Anthropic = &AnthropicMessageMetadata{}
				}
				out.ProviderMetadata.Anthropic.ThinkingSignature = block.Signature
			}
		case "text":
			out.Content += block.Text
		case "tool_use":
			input, err := normalizeAnthropicToolInput(block.Name, block.Input)
			if err != nil {
				return Message{}, err
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: input,
			})
		}
	}
	return out, nil
}

func normalizeAnthropicToolInput(name string, input map[string]any) (map[string]any, error) {
	if len(input) == 0 {
		return map[string]any{}, nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode tool call %q arguments: %w", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%w %q arguments: %w", errDecodeToolCallArguments, name, err)
	}
	return out, nil
}

func normalizeAnthropicFinishReason(reason string) string {
	switch reason {
	case "tool_use":
		return "tool_calls"
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	default:
		return reason
	}
}

func anthropicToolInput(arguments map[string]any) map[string]any {
	if arguments == nil {
		return nil
	}
	if len(arguments) == 0 {
		return map[string]any{}
	}
	return cloneToolArguments(arguments)
}

// cloneToolArguments deep-copies tool call arguments for echoing back to the
// model. Delegates to the shared, error-free implementation instead of a
// JSON round trip that can silently drop arguments on marshal failure.
func cloneToolArguments(input map[string]any) map[string]any {
	return CloneToolArguments(input)
}

func (u *anthropicUsage) toUsageStats() *UsageStats {
	if u == nil {
		return nil
	}
	promptTokens := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	usage := &UsageStats{
		PromptTokens:             promptTokens,
		CompletionTokens:         u.OutputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}
