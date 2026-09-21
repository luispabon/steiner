package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type responsesStreamEvent struct {
	Type     string            `json:"type"`
	Delta    string            `json:"delta,omitempty"`
	Response responsesResponse `json:"response,omitempty"`
	Item     responsesItem     `json:"item,omitempty"`
	Error    *struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Code    json.RawMessage `json:"code"`
	} `json:"error,omitempty"`
	Status     json.RawMessage            `json:"status,omitempty"`
	StatusCode json.RawMessage            `json:"status_code,omitempty"`
	Headers    map[string]json.RawMessage `json:"headers,omitempty"`
}

type responsesStreamState struct {
	content                  strings.Builder
	thinking                 strings.Builder
	toolCalls                []ToolCall
	usage                    *UsageStats
	finishReason             string
	reasoningID              string
	sawDone                  bool
	sawContent               bool
	sawToolCall              bool
	sawThinking              bool
	pendingThinkingSeparator bool
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
		if httpErr := responsesFrameHTTPError(&payload, event); httpErr != nil {
			return false, httpErr
		}
		if payload.Error.Message != "" {
			return false, fmt.Errorf("responses stream error: %s", payload.Error.Message)
		}
		return false, fmt.Errorf("responses stream error")
	}

	switch payload.Type {
	case "response.output_text.delta":
		return handleResponsesTextDelta(state, payload.Delta, emit)
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return handleResponsesReasoningDelta(state, payload.Delta, emit)
	case "response.reasoning_summary_part.added", "response.reasoning_summary_text.done":
		state.pendingThinkingSeparator = true
		return false, nil
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

func handleResponsesReasoningDelta(state *responsesStreamState, delta string, emit func(ChatChunk) error) (bool, error) {
	if delta == "" {
		return false, nil
	}
	prefix := ""
	if state.pendingThinkingSeparator && state.thinking.Len() > 0 && !strings.HasSuffix(state.thinking.String(), "\n") {
		prefix = "\n"
	}
	state.pendingThinkingSeparator = false
	state.thinking.WriteString(prefix + delta)
	state.sawThinking = true
	return false, emit(ChatChunk{Thinking: prefix + delta})
}

func handleResponsesOutputItemDone(state *responsesStreamState, item responsesItem) (bool, error) {
	if item.Type == "reasoning" {
		state.reasoningID = item.ID
		state.pendingThinkingSeparator = true
		return false, nil
	}
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
	if resp.Message.ReasoningContent != "" && !state.sawThinking {
		state.thinking.WriteString(resp.Message.ReasoningContent)
		state.sawThinking = true
	}
	if resp.Message.ProviderMetadata != nil && resp.Message.ProviderMetadata.Codex != nil {
		if resp.Message.ProviderMetadata.Codex.ReasoningID != "" && state.reasoningID == "" {
			state.reasoningID = resp.Message.ProviderMetadata.Codex.ReasoningID
		}
	}
	state.usage = resp.Usage
	state.finishReason = resp.FinishReason
	return true, nil
}

func flushResponsesStreamState(emit func(ChatChunk) error, state responsesStreamState) error {
	if !state.sawContent && !state.sawToolCall && !state.sawThinking && state.finishReason == "" && state.usage == nil {
		return nil
	}
	return emit(responsesStreamStateToChatChunk(state))
}

func responsesStreamStateToChatChunk(state responsesStreamState) ChatChunk {
	message := Message{Role: MessageRoleAssistant}
	if state.sawContent {
		message.Content = state.content.String()
	}
	if state.sawThinking {
		message.ReasoningContent = state.thinking.String()
	}
	if state.reasoningID != "" {
		message.ProviderMetadata = &MessageProviderMetadata{
			Codex: &CodexMessageMetadata{ReasoningID: state.reasoningID},
		}
	}
	if state.sawToolCall {
		message.ToolCalls = state.toolCalls
	}
	return ChatChunk{
		Delta:        message,
		Usage:        state.usage,
		Done:         true,
		FinishReason: state.finishReason,
	}
}

// wsRetryableErrorCodes keep the plain-error path so the WebSocket provider can
// reconnect or resend instead of surfacing an HTTP-style failure.
var wsRetryableErrorCodes = map[string]struct{}{
	"websocket_connection_limit_reached": {},
	"previous_response_not_found":        {},
}

// frameStatus parses a frame's status field leniently: absent, null, a bare
// integer, or a quoted integer string. It returns (0, false) when raw does
// not decode to an integer.
func frameStatus(raw json.RawMessage) (int, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0, false
	}
	s = strings.Trim(s, `"`)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// responsesFrameHTTPError converts a status-bearing error frame (status >= 400)
// into an *HTTPError so it can be classified like an HTTP response. It returns
// nil for frames without a status or with a WebSocket-retryable code.
func responsesFrameHTTPError(payload *responsesStreamEvent, frame string) *HTTPError {
	status, ok := frameStatus(payload.Status)
	if !ok {
		status, ok = frameStatus(payload.StatusCode)
	}
	if !ok || status < 400 {
		return nil
	}
	code := strings.TrimSpace(string(payload.Error.Code))
	if code == "null" {
		code = ""
	}
	code = strings.Trim(code, `"`)
	if _, ok := wsRetryableErrorCodes[code]; ok {
		return nil
	}
	header := http.Header{}
	for k, raw := range payload.Headers {
		header.Set(k, strings.Trim(string(raw), `"`))
	}
	return &HTTPError{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Body:       frame,
		Header:     header,
	}
}
