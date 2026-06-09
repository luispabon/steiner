package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

type anthropicStreamState struct {
	content      strings.Builder
	thinking     strings.Builder
	toolUses     map[int]*anthropicToolUseAccumulator
	finishReason string
	usage        *UsageStats
	sawContent   bool
	sawThinking  bool
	sawToolUse   bool
}

type anthropicToolUseAccumulator struct {
	ID    string
	Name  string
	Input strings.Builder
}

type anthropicStreamEvent struct {
	Type         string                 `json:"type,omitempty"`
	Index        int                    `json:"index,omitempty"`
	Message      *anthropicResponse     `json:"message,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Delta        *anthropicStreamDelta  `json:"delta,omitempty"`
	Usage        *anthropicUsage        `json:"usage,omitempty"`
	Error        *anthropicStreamError  `json:"error,omitempty"`
}

type anthropicStreamDelta struct {
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Signature   string `json:"signature,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

type anthropicStreamError struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

func decodeAnthropicStreamWithHandler(_ context.Context, body io.Reader, emit func(ChatChunk) error) error {
	reader := bufio.NewReader(body)
	state := anthropicStreamState{
		toolUses: make(map[int]*anthropicToolUseAccumulator),
	}

	for {
		eventType, data, err := readAnthropicSSEEvent(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if strings.TrimSpace(data) == "" {
			continue
		}
		done, err := processAnthropicStreamEvent(&state, eventType, data, emit)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}

	return fmt.Errorf("stream completed without a final chunk: %w", io.ErrUnexpectedEOF)
}

func processAnthropicStreamEvent(state *anthropicStreamState, eventType, data string, emit func(ChatChunk) error) (bool, error) {
	var payload anthropicStreamEvent
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return false, fmt.Errorf("decode stream chunk: %w", err)
	}
	if eventType == "" {
		eventType = payload.Type
	}

	switch eventType {
	case "message_start":
		if payload.Message != nil {
			mergeAnthropicUsage(state, payload.Message.Usage)
		}
		return false, nil
	case "content_block_start":
		return false, handleAnthropicContentBlockStart(state, payload.Index, payload.ContentBlock, emit)
	case "content_block_delta":
		return false, handleAnthropicContentBlockDelta(state, payload.Index, payload.Delta, emit)
	case "content_block_stop":
		return false, nil
	case "message_delta":
		if payload.Delta != nil && payload.Delta.StopReason != "" {
			state.finishReason = normalizeAnthropicFinishReason(payload.Delta.StopReason)
		}
		mergeAnthropicUsage(state, payload.Usage)
		return false, nil
	case "message_stop":
		return true, flushAnthropicStreamState(emit, state)
	case "error":
		message := "anthropic stream error"
		if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
			message = payload.Error.Message
		}
		return true, emit(ChatChunk{
			Done:          true,
			Error:         message,
			OriginalError: fmt.Errorf("%s", message),
		})
	default:
		return false, nil
	}
}

func handleAnthropicContentBlockStart(state *anthropicStreamState, index int, block *anthropicContentBlock, emit func(ChatChunk) error) error {
	if block == nil {
		return nil
	}
	switch block.Type {
	case "text":
		return emitAnthropicTextDelta(state, block.Text, emit)
	case "thinking":
		return emitAnthropicThinkingDelta(state, block.Thinking, emit)
	case "tool_use":
		acc := state.toolUseAccumulator(index)
		acc.ID = block.ID
		acc.Name = block.Name
		if len(block.Input) > 0 {
			raw, err := json.Marshal(block.Input)
			if err != nil {
				return fmt.Errorf("encode tool call %q arguments: %w", block.Name, err)
			}
			acc.Input.Write(raw)
			state.sawToolUse = true
		}
	}
	return nil
}

func handleAnthropicContentBlockDelta(state *anthropicStreamState, index int, delta *anthropicStreamDelta, emit func(ChatChunk) error) error {
	if delta == nil {
		return nil
	}
	switch delta.Type {
	case "text_delta":
		return emitAnthropicTextDelta(state, delta.Text, emit)
	case "thinking_delta":
		return emitAnthropicThinkingDelta(state, delta.Thinking, emit)
	case "input_json_delta":
		acc := state.toolUseAccumulator(index)
		acc.Input.WriteString(delta.PartialJSON)
		state.sawToolUse = true
	}
	return nil
}

func emitAnthropicTextDelta(state *anthropicStreamState, text string, emit func(ChatChunk) error) error {
	if text == "" {
		return nil
	}
	state.content.WriteString(text)
	state.sawContent = true
	return emit(ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: text}})
}

func emitAnthropicThinkingDelta(state *anthropicStreamState, thinking string, emit func(ChatChunk) error) error {
	if thinking == "" {
		return nil
	}
	state.thinking.WriteString(thinking)
	state.sawThinking = true
	return emit(ChatChunk{Thinking: thinking})
}

func (state *anthropicStreamState) toolUseAccumulator(index int) *anthropicToolUseAccumulator {
	acc := state.toolUses[index]
	if acc != nil {
		return acc
	}
	acc = &anthropicToolUseAccumulator{}
	state.toolUses[index] = acc
	return acc
}

func flushAnthropicStreamState(emit func(ChatChunk) error, state *anthropicStreamState) error {
	message := Message{Role: MessageRoleAssistant}
	if state.sawContent {
		message.Content = state.content.String()
	}
	if state.sawThinking {
		message.ReasoningContent = state.thinking.String()
	}
	if state.sawToolUse {
		toolCalls, err := finalizeAnthropicToolUses(state.toolUses)
		if err != nil {
			return err
		}
		message.ToolCalls = toolCalls
	}
	return emit(ChatChunk{
		Delta:        message,
		Usage:        state.usage,
		Done:         true,
		FinishReason: state.finishReason,
	})
}

func finalizeAnthropicToolUses(toolUses map[int]*anthropicToolUseAccumulator) ([]ToolCall, error) {
	if len(toolUses) == 0 {
		return nil, nil
	}
	calls := make([]ToolCall, 0, len(toolUses))
	indexes := make([]int, 0, len(toolUses))
	for index := range toolUses {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		acc := toolUses[index]
		arguments := make(map[string]any)
		raw := strings.TrimSpace(acc.Input.String())
		if raw != "" {
			raw = sanitizeToolCallJSON(raw)
			if err := json.Unmarshal([]byte(raw), &arguments); err != nil {
				return nil, fmt.Errorf("decode tool call %q arguments: %w", acc.Name, err)
			}
		}
		calls = append(calls, ToolCall{
			ID:        acc.ID,
			Name:      acc.Name,
			Arguments: arguments,
		})
	}
	return calls, nil
}

func mergeAnthropicUsage(state *anthropicStreamState, usage *anthropicUsage) {
	if usage == nil {
		return
	}
	if state.usage == nil {
		state.usage = &UsageStats{}
	}
	if usage.InputTokens > 0 {
		state.usage.PromptTokens = usage.InputTokens
	}
	if usage.OutputTokens > 0 {
		state.usage.CompletionTokens = usage.OutputTokens
	}
	if state.usage.PromptTokens > 0 || state.usage.CompletionTokens > 0 {
		state.usage.TotalTokens = state.usage.PromptTokens + state.usage.CompletionTokens
	}
}

func readAnthropicSSEEvent(reader *bufio.Reader) (string, string, error) {
	var data strings.Builder
	var eventType string
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if data.Len() > 0 || eventType != "" {
				return eventType, data.String(), nil
			}
			if err == io.EOF {
				return "", "", io.EOF
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if err == io.EOF {
			if data.Len() > 0 || eventType != "" {
				return eventType, data.String(), nil
			}
			return "", "", io.EOF
		}
	}
}
