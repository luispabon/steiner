package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type openAIToolCallAccumulator struct {
	ID        string
	Type      string
	Name      string
	Arguments strings.Builder
}

type openAIStreamState struct {
	content       strings.Builder
	thinking      strings.Builder
	toolCalls     map[int]*openAIToolCallAccumulator
	finishReason  string
	usage         *UsageStats
	sawContent    bool
	sawToolCall   bool
	sawThinking   bool
	assistantRole bool
	sawDone       bool
}

func decodeChatStream(ctx context.Context, body io.Reader, out chan<- ChatChunk) error {
	return decodeChatStreamWithHandler(ctx, body, func(chunk ChatChunk) error {
		select {
		case out <- chunk:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}

func decodeChatStreamWithHandler(ctx context.Context, body io.Reader, emit func(ChatChunk) error) error {
	reader := bufio.NewReader(body)
	state := openAIStreamState{
		toolCalls: make(map[int]*openAIToolCallAccumulator),
	}

	for {
		event, err := readSSEEvent(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if len(event) == 0 {
			continue
		}
		done, err := processStreamEvent(&state, event, emit)
		if err != nil {
			return err
		}
		if done {
			break
		}
	}

	if err := flushStreamState(emit, state); err != nil {
		return err
	}
	if !state.sawDone {
		return fmt.Errorf("stream completed without a final chunk: %w", io.ErrUnexpectedEOF)
	}
	return nil
}

func processStreamEvent(state *openAIStreamState, event string, emit func(ChatChunk) error) (bool, error) {
	if event == "[DONE]" {
		state.sawDone = true
		return true, nil
	}

	var payload openAIResponse
	if err := json.Unmarshal([]byte(event), &payload); err != nil {
		return false, fmt.Errorf("decode stream chunk: %w", err)
	}
	if payload.Usage != nil {
		state.usage = payload.Usage
	}
	if len(payload.Choices) == 0 {
		return false, nil
	}
	return false, handleStreamChoice(state, payload.Choices[0], emit)
}

func handleStreamChoice(state *openAIStreamState, choice openAIChoice, emit func(ChatChunk) error) error {
	if choice.FinishReason != "" {
		state.finishReason = choice.FinishReason
	}
	if choice.Delta.Role == "assistant" {
		state.assistantRole = true
	}
	if err := handleStreamChoiceContent(state, choice.Delta.Content, emit); err != nil {
		return err
	}
	if err := handleStreamChoiceReasoning(state, choice.Delta.ReasoningContent, emit); err != nil {
		return err
	}
	if err := handleStreamChoiceToolCalls(state, choice.Delta.ToolCalls); err != nil {
		return err
	}
	return nil
}

func handleStreamChoiceContent(state *openAIStreamState, content any, emit func(ChatChunk) error) error {
	if thinking := extractThinkingDelta(content); thinking != "" {
		state.thinking.WriteString(thinking)
		state.sawThinking = true
		return emit(ChatChunk{Thinking: thinking})
	}
	if text := stringOrEmpty(content); text != "" {
		state.content.WriteString(text)
		state.sawContent = true
		return emit(ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: text}})
	}
	return nil
}

func handleStreamChoiceReasoning(state *openAIStreamState, reasoning string, emit func(ChatChunk) error) error {
	if reasoning == "" {
		return nil
	}
	state.thinking.WriteString(reasoning)
	state.sawThinking = true
	return emit(ChatChunk{Thinking: reasoning})
}

func handleStreamChoiceToolCalls(state *openAIStreamState, toolCalls []openAIToolCall) error {
	if len(toolCalls) == 0 {
		return nil
	}
	state.sawToolCall = true
	for _, toolCall := range toolCalls {
		acc := state.toolCallAccumulator(toolCall.Index)
		if toolCall.ID != "" {
			acc.ID = toolCall.ID
		}
		if toolCall.Type != "" {
			acc.Type = toolCall.Type
		}
		if toolCall.Function.Name != "" {
			acc.Name = toolCall.Function.Name
		}
		if toolCall.Function.Arguments != "" {
			acc.Arguments.WriteString(toolCall.Function.Arguments)
		}
	}
	return nil
}

func (state *openAIStreamState) toolCallAccumulator(index int) *openAIToolCallAccumulator {
	acc := state.toolCalls[index]
	if acc != nil {
		return acc
	}
	acc = &openAIToolCallAccumulator{}
	state.toolCalls[index] = acc
	return acc
}

func flushStreamState(emit func(ChatChunk) error, state openAIStreamState) error {
	if !state.sawContent && !state.sawToolCall && !state.sawThinking && state.finishReason == "" && state.usage == nil {
		return nil
	}

	message := Message{Role: MessageRoleAssistant}
	if state.sawContent {
		message.Content = state.content.String()
	}
	if state.sawThinking {
		message.ReasoningContent = state.thinking.String()
	}
	if state.sawToolCall {
		toolCalls, err := finalizeToolCalls(state.toolCalls)
		if err != nil {
			return err
		}
		message.ToolCalls = toolCalls
	}

	chunk := ChatChunk{
		Delta:        message,
		Usage:        state.usage,
		Done:         true,
		FinishReason: state.finishReason,
	}
	return emit(chunk)
}

func finalizeToolCalls(toolCalls map[int]*openAIToolCallAccumulator) ([]ToolCall, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}
	calls := make([]ToolCall, 0, len(toolCalls))
	for i := 0; i < len(toolCalls); i++ {
		acc, ok := toolCalls[i]
		if !ok || acc == nil {
			continue
		}
		arguments := make(map[string]any)
		rawArgs := strings.TrimSpace(acc.Arguments.String())
		if rawArgs != "" {
			if err := json.Unmarshal([]byte(rawArgs), &arguments); err != nil {
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

// extractThinkingDelta returns thinking text if the content value is a structured
// content array containing a thinking or thinking_delta block (Anthropic-style).
// Returns "" for plain string content so the caller falls through to stringOrEmpty.
func extractThinkingDelta(value any) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		if t != "thinking" && t != "thinking_delta" {
			continue
		}
		if text, ok := m["thinking"].(string); ok {
			sb.WriteString(text)
		}
	}
	return sb.String()
}

func readSSEEvent(reader *bufio.Reader) (string, error) {
	var builder strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if builder.Len() > 0 {
				return builder.String(), nil
			}
			if err == io.EOF {
				return "", io.EOF
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if err == io.EOF {
			if builder.Len() > 0 {
				return builder.String(), nil
			}
			return "", io.EOF
		}
	}
}
