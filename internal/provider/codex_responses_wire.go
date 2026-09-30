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
	return json.Marshal(responsesRequestMap(r))
}

func responsesRequestMap(r responsesRequest) map[string]any {
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
	return mergeRequestParams(base, r.Params, r.ExtraParams)
}

type responsesItem struct {
	Type    string                 `json:"type,omitempty"`
	Role    string                 `json:"role,omitempty"`
	Content []responsesContentPart `json:"content,omitempty"`
	Summary []responsesContentPart `json:"summary,omitempty"`
	ID      string                 `json:"id,omitempty"`
	CallID  string                 `json:"call_id,omitempty"`
	Name    string                 `json:"name,omitempty"`
	Args    string                 `json:"arguments,omitempty"`
	Output  string                 `json:"output,omitempty"`
	Phase   string                 `json:"phase,omitempty"`
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
	Error *struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
	} `json:"error,omitempty"`
}

type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
}

func (u *responsesUsage) toUsageStats() *UsageStats {
	if u == nil {
		return nil
	}
	return &UsageStats{
		PromptTokens:             u.InputTokens,
		CompletionTokens:         u.OutputTokens,
		TotalTokens:              u.TotalTokens,
		CacheCreationInputTokens: u.InputTokensDetails.CacheWriteTokens,
		CacheReadInputTokens:     u.InputTokensDetails.CachedTokens,
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
	// Codex Responses requires function_call_output items to follow the
	// assistant turn contiguously. A tool result's image parts would break that
	// run, so defer them and emit them as one user message once the run ends.
	// System messages are hoisted into instructions and must not flush the run.
	var pendingImageParts []responsesContentPart
	flushImages := func() {
		if len(pendingImageParts) == 0 {
			return
		}
		wire.Input = append(wire.Input, messageItem("user", pendingImageParts))
		pendingImageParts = nil
	}
	for _, msg := range request.Messages {
		if msg.Role == MessageRoleSystem {
			if text := strings.TrimSpace(msg.Content); text != "" {
				instructions = append(instructions, text)
			}
			continue
		}
		if msg.Role != MessageRoleTool {
			flushImages()
		}
		items, err := messageToResponsesItems(msg)
		if err != nil {
			return responsesRequest{}, err
		}
		if msg.Role == MessageRoleTool && len(items) > 1 {
			wire.Input = append(wire.Input, items[0])
			for _, extra := range items[1:] {
				pendingImageParts = append(pendingImageParts, extra.Content...)
			}
			continue
		}
		wire.Input = append(wire.Input, items...)
	}
	flushImages()
	wire.Instructions = strings.Join(instructions, "\n\n")
	return wire, nil
}

//nolint:gocyclo,unparam // Replay preserves ordered blocks; errors remain part of the existing converter contract.
func messageToResponsesItems(msg Message) ([]responsesItem, error) {
	switch msg.Role {
	case MessageRoleUser:
		return []responsesItem{messageItem("user", inputContentParts(msg))}, nil
	case MessageRoleAssistant:
		itemsCap := 1 + len(msg.ToolCalls)
		if msg.ReasoningContent != "" {
			itemsCap++
		}
		items := make([]responsesItem, 0, itemsCap)
		if msg.ReasoningContent != "" {
			var reasoningID string
			if msg.ProviderMetadata != nil && msg.ProviderMetadata.Codex != nil {
				reasoningID = msg.ProviderMetadata.Codex.ReasoningID
			}
			items = append(items, responsesItem{
				Type:    "reasoning",
				ID:      reasoningID,
				Summary: []responsesContentPart{{Type: "summary_text", Text: msg.ReasoningContent}},
			})
		}
		var blocks []CodexMessageBlock
		if msg.ProviderMetadata != nil && msg.ProviderMetadata.Codex != nil {
			blocks = msg.ProviderMetadata.Codex.Blocks
		}
		if len(blocks) == 0 {
			if msg.Content != "" || len(msg.Images) > 0 {
				items = append(items, messageItem("assistant", outputContentParts(msg.Content)))
			}
			for _, call := range msg.ToolCalls {
				items = append(items, responsesFunctionCallItem(call))
			}
			return items, nil
		}
		callIndex := 0
		usedCalls := make([]bool, len(msg.ToolCalls))
		for _, block := range blocks {
			if block.Kind == "message" {
				items = append(items, responsesItem{Type: "message", Role: "assistant", Content: outputContentParts(block.Text), Phase: block.Phase})
				continue
			}
			if block.Kind != "function_call" {
				continue
			}
			found := -1
			if block.CallID != "" {
				for i := range msg.ToolCalls {
					if !usedCalls[i] && msg.ToolCalls[i].ID == block.CallID {
						found = i
						break
					}
				}
			} else {
				for callIndex < len(msg.ToolCalls) && usedCalls[callIndex] {
					callIndex++
				}
				if callIndex < len(msg.ToolCalls) {
					found = callIndex
					callIndex++
				}
			}
			if found >= 0 {
				usedCalls[found] = true
				items = append(items, responsesFunctionCallItem(msg.ToolCalls[found]))
			}
		}
		for i, call := range msg.ToolCalls {
			if !usedCalls[i] {
				items = append(items, responsesFunctionCallItem(call))
			}
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

func responsesFunctionCallItem(call ToolCall) responsesItem {
	args := call.RawArguments
	if args == "" {
		data, _ := json.Marshal(call.Arguments)
		args = string(data)
	}
	return responsesItem{Type: "function_call", CallID: call.ID, Name: call.Name, Args: args}
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

//nolint:gocyclo // Response normalization builds ordered blocks and legacy flattened fields together.
func normalizeResponsesResponse(payload responsesResponse) (ChatResponse, error) {
	message := Message{Role: MessageRoleAssistant}
	var content strings.Builder
	var reasoning strings.Builder
	var reasoningID string
	var blocks []CodexMessageBlock
	for _, item := range payload.Output {
		switch item.Type {
		case "message":
			var blockText strings.Builder
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					content.WriteString(part.Text)
					blockText.WriteString(part.Text)
				}
			}
			blocks = append(blocks, CodexMessageBlock{Kind: "message", Phase: item.Phase, Text: blockText.String()})
		case "function_call":
			call, err := responsesToolCall(item)
			if err != nil {
				return ChatResponse{}, err
			}
			message.ToolCalls = append(message.ToolCalls, call)
			blocks = append(blocks, CodexMessageBlock{Kind: "function_call", CallID: call.ID})
		case "reasoning":
			for _, part := range item.Summary {
				if part.Type == "summary_text" {
					reasoning.WriteString(part.Text)
				}
			}
			reasoningID = item.ID
		}
	}
	message.Content = content.String()
	if len(blocks) > 0 {
		informative := len(blocks) > 1
		for _, block := range blocks {
			informative = informative || block.Phase != ""
		}
		if informative {
			message.ProviderMetadata = &MessageProviderMetadata{Codex: &CodexMessageMetadata{Blocks: blocks}}
		}
	}
	if reasoning.Len() > 0 {
		message.ReasoningContent = reasoning.String()
		if reasoningID != "" {
			if message.ProviderMetadata == nil {
				message.ProviderMetadata = &MessageProviderMetadata{}
			}
			if message.ProviderMetadata.Codex == nil {
				message.ProviderMetadata.Codex = &CodexMessageMetadata{}
			}
			message.ProviderMetadata.Codex.ReasoningID = reasoningID
		}
	}

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
