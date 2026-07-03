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

type responsesStreamEvent struct {
	Type     string            `json:"type"`
	Delta    string            `json:"delta,omitempty"`
	Response responsesResponse `json:"response,omitempty"`
	Item     responsesItem     `json:"item,omitempty"`
	Error    *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type responsesStreamState struct {
	content      strings.Builder
	toolCalls    []ToolCall
	usage        *UsageStats
	finishReason string
	sawDone      bool
	sawContent   bool
	sawToolCall  bool
}

func decodeResponsesStreamWithHandler(_ context.Context, body io.Reader, emit func(ChatChunk) error) error {
	reader := bufio.NewReader(body)
	state := responsesStreamState{}

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
		done, err := processResponsesStreamEvent(&state, event, emit)
		if err != nil {
			return err
		}
		if done {
			break
		}
	}

	hadUsableFinalChunk := state.sawDone || state.finishReason != ""
	if err := flushResponsesStreamState(emit, state); err != nil {
		return err
	}
	if !hadUsableFinalChunk {
		return fmt.Errorf("stream completed without a final chunk: %w", io.ErrUnexpectedEOF)
	}
	return nil
}

func processResponsesStreamEvent(state *responsesStreamState, event string, emit func(ChatChunk) error) (bool, error) {
	if event == "[DONE]" {
		state.sawDone = true
		return true, nil
	}

	var payload responsesStreamEvent
	if err := json.Unmarshal([]byte(event), &payload); err != nil {
		if strings.Contains(err.Error(), "unexpected end of JSON input") {
			return false, fmt.Errorf("%w: %w", errDecodeStreamChunkUnexpected, err)
		}
		return false, fmt.Errorf("%w: %w", errDecodeStreamChunk, err)
	}
	if payload.Error != nil {
		if payload.Error.Message != "" {
			return false, fmt.Errorf("responses stream error: %s", payload.Error.Message)
		}
		return false, fmt.Errorf("responses stream error")
	}

	switch payload.Type {
	case "response.output_text.delta":
		return handleResponsesTextDelta(state, payload.Delta, emit)
	case "response.output_item.done":
		return handleResponsesOutputItemDone(state, payload.Item)
	case "response.completed":
		return handleResponsesCompleted(state, payload.Response)
	}
	return false, nil
}

func handleResponsesTextDelta(state *responsesStreamState, delta string, emit func(ChatChunk) error) (bool, error) {
	if delta == "" {
		return false, nil
	}
	state.content.WriteString(delta)
	state.sawContent = true
	return false, emit(ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: delta}})
}

func handleResponsesOutputItemDone(state *responsesStreamState, item responsesItem) (bool, error) {
	if item.Type != "function_call" {
		return false, nil
	}
	call, err := responsesToolCall(item)
	if err != nil {
		return false, err
	}
	state.toolCalls = append(state.toolCalls, call)
	state.sawToolCall = true
	return false, nil
}

func handleResponsesCompleted(state *responsesStreamState, response responsesResponse) (bool, error) {
	state.sawDone = true
	resp, err := normalizeResponsesResponse(response)
	if err != nil {
		return false, err
	}
	if resp.Message.Content != "" && !state.sawContent {
		state.content.WriteString(resp.Message.Content)
		state.sawContent = true
	}
	if len(resp.Message.ToolCalls) > 0 && !state.sawToolCall {
		state.toolCalls = append(state.toolCalls, resp.Message.ToolCalls...)
		state.sawToolCall = true
	}
	state.usage = resp.Usage
	if resp.Usage != nil {
		cacheDebug("usage input=%d cached=%d", resp.Usage.PromptTokens, resp.Usage.CacheReadInputTokens)
	}
	state.finishReason = resp.FinishReason
	return true, nil
}

func flushResponsesStreamState(emit func(ChatChunk) error, state responsesStreamState) error {
	if !state.sawContent && !state.sawToolCall && state.finishReason == "" && state.usage == nil {
		return nil
	}
	message := Message{Role: MessageRoleAssistant}
	if state.sawContent {
		message.Content = state.content.String()
	}
	if state.sawToolCall {
		message.ToolCalls = state.toolCalls
	}
	return emit(ChatChunk{
		Delta:        message,
		Usage:        state.usage,
		Done:         true,
		FinishReason: state.finishReason,
	})
}
