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
	Refusal  string `json:"refusal,omitempty"`
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
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
	// Cache fields are pointers so a present zero is distinguishable from an
	// absent field. Cost is raw so a non-number shape cannot fail the decode.
	InputTokensDetails struct {
		CachedTokens     *int `json:"cached_tokens"`
		CacheWriteTokens *int `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
	Cost json.RawMessage `json:"cost"`
}

func (u *responsesUsage) toUsageStats() *UsageStats {
	if u == nil {
		return nil
	}
	stats := &UsageStats{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		ReasoningTokens:  clampReasoningTokens(u.OutputTokensDetails.ReasoningTokens),
		TotalTokens:      u.TotalTokens,
		CacheReported:    u.InputTokensDetails.CachedTokens != nil || u.InputTokensDetails.CacheWriteTokens != nil,
		CostUSD:          usageCostUSD(u.Cost),
	}
	if u.InputTokensDetails.CachedTokens != nil {
		stats.CacheReadInputTokens = *u.InputTokensDetails.CachedTokens
	}
	if u.InputTokensDetails.CacheWriteTokens != nil {
		stats.CacheCreationInputTokens = *u.InputTokensDetails.CacheWriteTokens
	}
	return stats
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
			if msg.Content != "" {
				items = append(items, messageItem("assistant", outputContentParts(msg.Content)))
			}
			for _, call := range msg.ToolCalls {
				item, err := responsesFunctionCallItem(call)
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
			return dropLoneReasoning(items), nil
		}
		callIndex := 0
		usedCalls := make([]bool, len(msg.ToolCalls))
		for _, block := range blocks {
			if block.Kind == "message" {
				// The API rejects message items without content, and the model can
				// emit empty ones (e.g. an empty final_answer after commentary).
				if block.Text != "" {
					items = append(items, responsesItem{Type: "message", Role: "assistant", Content: outputContentParts(block.Text), Phase: block.Phase})
				}
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
				item, err := responsesFunctionCallItem(msg.ToolCalls[found])
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
		}
		for i, call := range msg.ToolCalls {
			if !usedCalls[i] {
				item, err := responsesFunctionCallItem(call)
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
		}
		return dropLoneReasoning(items), nil
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

// dropLoneReasoning removes a reasoning item that nothing follows; the API
// requires reasoning to be followed by an output item.
func dropLoneReasoning(items []responsesItem) []responsesItem {
	if len(items) == 1 && items[0].Type == "reasoning" {
		return nil
	}
	return items
}

func responsesFunctionCallItem(call ToolCall) (responsesItem, error) {
	args := call.RawArguments
	if args == "" {
		data, err := json.Marshal(call.Arguments)
		if err != nil {
			return responsesItem{}, fmt.Errorf("marshal arguments for tool call %q: %w", call.Name, err)
		}
		args = string(data)
	}
	return responsesItem{Type: "function_call", CallID: call.ID, Name: call.Name, Args: args}, nil
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
			ImageURL: imageDataURL(img),
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

// responsesPartText returns the assistant text a message content part carries.
// A refusal is the model's text, so it is read from its refusal field.
func responsesPartText(part responsesContentPart) (string, bool) {
	switch part.Type {
	case "output_text", "text":
		return part.Text, true
	case "refusal":
		return part.Refusal, true
	}
	return "", false
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
				if text, ok := responsesPartText(part); ok {
					blockText.WriteString(text)
				}
			}
			block := CodexMessageBlock{Kind: "message", Phase: item.Phase, Text: blockText.String()}
			content.WriteString(codexMessageBoundary(content.String(), block.Text))
			content.WriteString(block.Text)
			blocks = append(blocks, block)
		case "function_call":
			call := responsesToolCall(item)
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
		finish = responsesIncompleteFinishReason(payload.IncompleteDetail.Reason)
	}
	return ChatResponse{
		Message:      message,
		Usage:        payload.Usage.toUsageStats(),
		FinishReason: finish,
	}, nil
}

// responsesIncompleteFinishReason maps an incomplete_details reason to the
// normalized finish reason. max_output_tokens is "length"; other reasons, such
// as content_filter, pass through unchanged.
func responsesIncompleteFinishReason(reason string) string {
	if reason == "max_output_tokens" {
		return "length"
	}
	return reason
}

func responsesToolCall(item responsesItem) ToolCall {
	id := item.CallID
	if id == "" {
		id = item.ID
	}
	args := make(map[string]any)
	rawArgs := strings.TrimSpace(item.Args)
	sanitizedRawArgs := ""
	if rawArgs != "" {
		sanitizedRawArgs = sanitizeToolCallJSON(rawArgs)
		if err := json.Unmarshal([]byte(sanitizedRawArgs), &args); err != nil {
			return markArgumentDecodeError(ToolCall{ID: id, Name: item.Name}, err)
		}
	}
	call := ToolCall{
		ID:        id,
		Name:      item.Name,
		Arguments: args,
	}
	if sanitizedRawArgs != "" {
		call.RawArguments = sanitizedRawArgs
	}
	return call
}
