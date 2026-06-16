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
	m := make(map[string]any, len(r.Params)+len(r.ExtraParams)+6)
	for key, value := range r.Params {
		m[key] = value
	}
	for key, value := range r.ExtraParams {
		m[key] = value
	}
	m["model"] = r.Model
	m["messages"] = r.Messages
	if len(r.System) > 0 {
		m["system"] = r.System
	}
	if r.MaxTokens != nil {
		m["max_tokens"] = *r.MaxTokens
	}
	if r.Stream {
		m["stream"] = true
	}
	if len(r.Tools) > 0 {
		m["tools"] = r.Tools
	}
	return json.Marshal(m)
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"` // always "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicContentBlock struct {
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	Thinking  string                `json:"thinking,omitempty"`
	Signature string                `json:"signature,omitempty"`
	ID        string                `json:"id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Input     map[string]any        `json:"input,omitempty"`
	ToolUseID string                `json:"tool_use_id,omitempty"`
	Content   string                `json:"content,omitempty"`
	Source    *anthropicImageSource `json:"source,omitempty"`
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
	return wire
}

func toAnthropicMessage(message Message) *anthropicMessage {
	switch message.Role {
	case MessageRoleUser:
		content := make([]anthropicContentBlock, 0, 1+len(message.Images))
		if message.Content != "" {
			content = append(content, anthropicContentBlock{Type: "text", Text: message.Content})
		}
		for _, img := range message.Images {
			content = append(content, anthropicContentBlock{
				Type: "image",
				Source: &anthropicImageSource{
					Type:      "base64",
					MediaType: img.MediaType,
					Data:      img.Data,
				},
			})
		}
		if len(content) == 0 {
			// keep at least empty text block to avoid empty content array
			content = append(content, anthropicContentBlock{Type: "text", Text: ""})
		}
		return &anthropicMessage{Role: "user", Content: content}
	case MessageRoleAssistant:
		content := make([]anthropicContentBlock, 0, 1+len(message.ToolCalls))
		if message.ReasoningContent != "" {
			// The Anthropic Messages API rejects replayed thinking blocks that
			// lack a signature. Only emit the block when we captured one;
			// otherwise drop the reasoning text rather than send an invalid
			// request.
			var signature string
			if metadata := message.ProviderMetadata; metadata != nil && metadata.Anthropic != nil {
				signature = metadata.Anthropic.ThinkingSignature
			}
			if strings.TrimSpace(signature) != "" {
				content = append(content, anthropicContentBlock{
					Type:      "thinking",
					Thinking:  message.ReasoningContent,
					Signature: signature,
				})
			}
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
				Input: cloneToolArguments(toolCall.Arguments),
			})
		}
		if len(content) == 0 {
			return nil
		}
		return &anthropicMessage{Role: "assistant", Content: content}
	case MessageRoleTool:
		content := []anthropicContentBlock{{
			Type:      "tool_result",
			ToolUseID: message.ToolCallID,
			Content:   message.Content,
		}}
		for _, img := range message.Images {
			content = append(content, anthropicContentBlock{
				Type: "image",
				Source: &anthropicImageSource{
					Type:      "base64",
					MediaType: img.MediaType,
					Data:      img.Data,
				},
			})
		}
		return &anthropicMessage{Role: "user", Content: content}
	default:
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
		return nil, fmt.Errorf("decode tool call %q arguments: %w", name, err)
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

func cloneToolArguments(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
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
