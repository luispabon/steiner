package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

// responsesRequest is serialized exclusively by its MarshalJSON below, which
// builds the wire object field by field. Struct tags would be dead decoration
// here, and worse, a trap: adding a tagged field emits nothing, so a change that
// looks correct silently sends an incomplete payload. Add new fields to
// MarshalJSON. This type is never unmarshalled.
type responsesRequest struct {
	Model          string
	Instructions   string
	Input          []responsesItem
	Store          *bool
	Stream         bool
	Tools          []responsesTool
	Reasoning      *ReasoningRequest
	PromptCacheKey string
	Params         map[string]any
	ExtraParams    map[string]any
}

// MarshalJSON emits the Codex Responses request body. Every field the backend
// sees is listed here; see the type comment before adding one.
func (r responsesRequest) MarshalJSON() ([]byte, error) {
	base := map[string]any{
		"model": r.Model,
		"input": r.Input,
	}
	if r.Instructions != "" {
		base["instructions"] = r.Instructions
	}
	if r.Store != nil {
		base["store"] = *r.Store
	}
	if r.Stream {
		base["stream"] = true
	}
	if len(r.Tools) > 0 {
		base["tools"] = r.Tools
	}
	if r.PromptCacheKey != "" {
		base["prompt_cache_key"] = r.PromptCacheKey
	}
	if r.Reasoning != nil {
		base["reasoning"] = reasoningWirePayload(r.Reasoning)
	}
	return json.Marshal(mergeRequestParams(base, r.Params, r.ExtraParams))
}

type responsesItem struct {
	Type    string                 `json:"type,omitempty"`
	Role    string                 `json:"role,omitempty"`
	Content []responsesContentPart `json:"content,omitempty"`
	ID      string                 `json:"id,omitempty"`
	CallID  string                 `json:"call_id,omitempty"`
	Name    string                 `json:"name,omitempty"`
	Args    string                 `json:"arguments,omitempty"`
	Output  string                 `json:"output,omitempty"`
}

type responsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type responsesTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
}

type responsesResponse struct {
	Output           []responsesItem `json:"output"`
	Usage            *responsesUsage `json:"usage,omitempty"`
	Status           string          `json:"status,omitempty"`
	IncompleteDetail *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details,omitempty"`
}

type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func (u *responsesUsage) toUsageStats() *UsageStats {
	if u == nil {
		return nil
	}
	return &UsageStats{
		PromptTokens:         u.InputTokens,
		CompletionTokens:     u.OutputTokens,
		TotalTokens:          u.TotalTokens,
		CacheReadInputTokens: u.InputTokensDetails.CachedTokens,
	}
}

func responsesRequestWire(request ChatRequest, defaultModel string, stream bool) (responsesRequest, error) {
	wire := responsesRequest{
		Model:       defaultModel,
		Input:       make([]responsesItem, 0, len(request.Messages)),
		Stream:      stream,
		Reasoning:   request.Reasoning,
		Params:      request.Params,
		ExtraParams: request.ExtraParams,
	}
	if strings.TrimSpace(request.Model) != "" {
		wire.Model = request.Model
	}
	if len(request.Tools) > 0 {
		wire.Tools = make([]responsesTool, 0, len(request.Tools))
		for _, tool := range request.Tools {
			wire.Tools = append(wire.Tools, responsesTool{
				Type:        tool.Type,
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  tool.Function.Parameters,
				Strict:      boolValuePtr(false),
			})
		}
	}

	var instructions []string
	for _, msg := range request.Messages {
		if msg.Role == MessageRoleSystem {
			if text := strings.TrimSpace(msg.Content); text != "" {
				instructions = append(instructions, text)
			}
			continue
		}
		items, err := messageToResponsesItems(msg)
		if err != nil {
			return responsesRequest{}, err
		}
		wire.Input = append(wire.Input, items...)
	}
	wire.Instructions = strings.Join(instructions, "\n\n")
	return wire, nil
}

func messageToResponsesItems(msg Message) ([]responsesItem, error) {
	switch msg.Role {
	case MessageRoleUser:
		return []responsesItem{messageItem("user", inputContentParts(msg))}, nil
	case MessageRoleAssistant:
		items := make([]responsesItem, 0, 1+len(msg.ToolCalls))
		if msg.Content != "" || len(msg.Images) > 0 {
			items = append(items, messageItem("assistant", outputContentParts(msg.Content)))
		}
		for _, call := range msg.ToolCalls {
			args := call.RawArguments
			if args == "" {
				data, err := json.Marshal(call.Arguments)
				if err != nil {
					return nil, fmt.Errorf("encode tool call %q arguments: %w", call.Name, err)
				}
				args = string(data)
			}
			items = append(items, responsesItem{
				Type:   "function_call",
				CallID: call.ID,
				Name:   call.Name,
				Args:   args,
			})
		}
		return items, nil
	case MessageRoleTool:
		items := []responsesItem{{
			Type:   "function_call_output",
			CallID: msg.ToolCallID,
			Output: msg.Content,
		}}
		if len(msg.Images) > 0 {
			items = append(items, messageItem("user", inputContentParts(Message{Images: msg.Images})))
		}
		return items, nil
	default:
		return []responsesItem{messageItem(string(msg.Role), inputContentParts(msg))}, nil
	}
}

func messageItem(role string, content []responsesContentPart) responsesItem {
	return responsesItem{Type: "message", Role: role, Content: content}
}

func inputContentParts(msg Message) []responsesContentPart {
	parts := make([]responsesContentPart, 0, 1+len(msg.Images))
	if msg.Content != "" {
		parts = append(parts, responsesContentPart{Type: "input_text", Text: msg.Content})
	}
	for _, img := range msg.Images {
		parts = append(parts, responsesContentPart{
			Type:     "input_image",
			ImageURL: fmt.Sprintf("data:%s;base64,%s", img.MediaType, img.Data),
		})
	}
	return parts
}

func outputContentParts(content string) []responsesContentPart {
	if content == "" {
		return nil
	}
	return []responsesContentPart{{Type: "output_text", Text: content}}
}

func normalizeResponsesResponse(payload responsesResponse) (ChatResponse, error) {
	message := Message{Role: MessageRoleAssistant}
	var content strings.Builder
	for _, item := range payload.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					content.WriteString(part.Text)
				}
			}
		case "function_call":
			call, err := responsesToolCall(item)
			if err != nil {
				return ChatResponse{}, err
			}
			message.ToolCalls = append(message.ToolCalls, call)
		}
	}
	message.Content = content.String()

	finish := "stop"
	if payload.Status == "incomplete" && payload.IncompleteDetail != nil {
		finish = payload.IncompleteDetail.Reason
	}
	return ChatResponse{
		Message:      message,
		Usage:        payload.Usage.toUsageStats(),
		FinishReason: finish,
	}, nil
}

func responsesToolCall(item responsesItem) (ToolCall, error) {
	args := make(map[string]any)
	rawArgs := strings.TrimSpace(item.Args)
	sanitizedRawArgs := ""
	if rawArgs != "" {
		sanitizedRawArgs = sanitizeToolCallJSON(rawArgs)
		if err := json.Unmarshal([]byte(sanitizedRawArgs), &args); err != nil {
			return ToolCall{}, fmt.Errorf("%w %q arguments: %w", errDecodeToolCallArguments, item.Name, err)
		}
	}
	id := item.CallID
	if id == "" {
		id = item.ID
	}
	call := ToolCall{
		ID:        id,
		Name:      item.Name,
		Arguments: args,
	}
	if sanitizedRawArgs != "" {
		call.RawArguments = sanitizedRawArgs
	}
	return call, nil
}
