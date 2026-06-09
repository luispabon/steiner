package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Wire types for OpenAI-compatible API requests and responses

type openAIRequest struct {
	Model         string               `json:"model"`
	Messages      []openAIMessage      `json:"messages"`
	MaxTokens     *int                 `json:"max_tokens,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
	Tools         []openAITool         `json:"tools,omitempty"`
	Params        map[string]any       `json:"-"` // Normalized generation params
	ExtraParams   map[string]any       `json:"-"` // Raw provider-specific passthrough
}

func (r openAIRequest) MarshalJSON() ([]byte, error) {
	// Merge order: base → Params → ExtraParams (later values win on collision)
	m := make(map[string]any, len(r.Params)+len(r.ExtraParams)+6)

	// Merge normalized params first
	for k, v := range r.Params {
		m[k] = v
	}

	// Merge extra params on top (override params on collision)
	for k, v := range r.ExtraParams {
		m[k] = v
	}

	// Standard fields (override anything from params/extra_params)
	m["model"] = r.Model
	m["messages"] = r.Messages
	if r.Stream {
		m["stream"] = true
	}
	if r.StreamOptions != nil {
		m["stream_options"] = r.StreamOptions
	}
	if r.MaxTokens != nil {
		m["max_tokens"] = *r.MaxTokens
	}
	if len(r.Tools) > 0 {
		m["tools"] = r.Tools
	}
	return json.Marshal(m)
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type openAIMessage struct {
	Role             string           `json:"role,omitempty"`
	Content          any              `json:"content,omitempty"`
	ReasoningContent *string          `json:"reasoning_content,omitempty"`
	ReasoningDetails any              `json:"reasoning_details,omitempty"`
	Name             string           `json:"name,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type openAIToolCall struct {
	ID       string                 `json:"id,omitempty"`
	Index    int                    `json:"index,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function openAIToolCallFunction `json:"function"`
}

type openAIToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
	Usage   *UsageStats    `json:"usage,omitempty"`
}

type openAIChoice struct {
	Message      openAIMessage `json:"message"`
	Delta        openAIMessage `json:"delta"`
	FinishReason string        `json:"finish_reason,omitempty"`
}

// Conversion functions

func toOpenAIMessage(message Message) (openAIMessage, error) {
	wire := openAIMessage{
		Role: string(message.Role),
		Name: message.Name,
	}
	if message.ReasoningContent != "" {
		wire.ReasoningContent = &message.ReasoningContent
	}
	switch message.Role {
	case MessageRoleAssistant, MessageRoleSystem, MessageRoleUser:
		if message.Content != "" {
			wire.Content = message.Content
		} else if message.Role == MessageRoleAssistant {
			wire.Content = nil
		}
	case MessageRoleTool:
		wire.Content = message.Content
		wire.ToolCallID = message.ToolCallID
	default:
		wire.Content = message.Content
	}
	if len(message.ToolCalls) > 0 {
		wire.ToolCalls = make([]openAIToolCall, 0, len(message.ToolCalls))
		for _, toolCall := range message.ToolCalls {
			args, err := json.Marshal(toolCall.Arguments)
			if err != nil {
				return openAIMessage{}, fmt.Errorf("encode tool call %q arguments: %w", toolCall.Name, err)
			}
			wire.ToolCalls = append(wire.ToolCalls, openAIToolCall{
				ID:   toolCall.ID,
				Type: "function",
				Function: openAIToolCallFunction{
					Name:      toolCall.Name,
					Arguments: string(args),
				},
			})
		}
	}
	return wire, nil
}

func normalizeChatResponse(payload *openAIResponse) (ChatResponse, error) {
	if payload == nil {
		return ChatResponse{}, fmt.Errorf("empty provider response")
	}
	if len(payload.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("provider response contained no choices")
	}
	choice := payload.Choices[0]
	message, err := normalizeMessage(choice.Message)
	if err != nil {
		return ChatResponse{}, err
	}
	return ChatResponse{
		Message:      message,
		Usage:        payload.Usage,
		FinishReason: choice.FinishReason,
	}, nil
}

func normalizeMessage(message openAIMessage) (Message, error) {
	out := Message{
		Role: MessageRole(message.Role),
	}
	if content := stringOrEmpty(message.Content); content != "" {
		out.Content = content
	}
	if message.ReasoningContent != nil {
		out.ReasoningContent = *message.ReasoningContent
	}
	out.ReasoningContent += extractReasoningDetailsText(message.ReasoningDetails)
	if message.Name != "" {
		out.Name = message.Name
	}
	if message.ToolCallID != "" {
		out.ToolCallID = message.ToolCallID
	}
	if len(message.ToolCalls) > 0 {
		toolCalls, err := normalizeToolCalls(message.ToolCalls)
		if err != nil {
			return Message{}, err
		}
		out.ToolCalls = toolCalls
	}
	return out, nil
}

func extractReasoningDetailsText(value any) string {
	var builder strings.Builder
	appendReasoningDetailsText(&builder, value)
	return builder.String()
}

func appendReasoningDetailsText(builder *strings.Builder, value any) {
	switch v := value.(type) {
	case nil:
		return
	case string:
		builder.WriteString(v)
	case []any:
		for _, item := range v {
			appendReasoningDetailsText(builder, item)
		}
	case map[string]any:
		appendReasoningDetailMapText(builder, v)
	}
}

func appendReasoningDetailMapText(builder *strings.Builder, detail map[string]any) {
	for _, key := range []string{"text", "thinking", "reasoning", "content", "value", "summary_text"} {
		value, ok := detail[key]
		if !ok {
			continue
		}
		appendReasoningDetailsText(builder, value)
		return
	}

	if summary, ok := detail["summary"].([]any); ok {
		for _, item := range summary {
			appendReasoningDetailsText(builder, item)
		}
	}
}

func normalizeToolCalls(toolCalls []openAIToolCall) ([]ToolCall, error) {
	out := make([]ToolCall, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		args := make(map[string]any)
		rawArgs := strings.TrimSpace(toolCall.Function.Arguments)
		if rawArgs != "" {
			rawArgs = sanitizeToolCallJSON(rawArgs)
			if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
				return nil, fmt.Errorf("decode tool call %q arguments: %w", toolCall.Function.Name, err)
			}
		}
		out = append(out, ToolCall{
			ID:        toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: args,
		})
	}
	return out, nil
}

// stringOrEmpty converts a value to a string, returning empty string for nil or non-string types
func stringOrEmpty(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case *string:
		if v == nil {
			return ""
		}
		return *v
	default:
		return ""
	}
}
