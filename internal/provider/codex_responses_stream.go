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
	Type         string            `json:"type"`
	Delta        string            `json:"delta,omitempty"`
	Text         string            `json:"text,omitempty"`
	ItemID       string            `json:"item_id,omitempty"`
	OutputIndex  *int              `json:"output_index,omitempty"`
	ContentIndex *int              `json:"content_index,omitempty"`
	Response     responsesResponse `json:"response,omitempty"`
	Item         responsesItem     `json:"item,omitempty"`
	Error        *struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Code    json.RawMessage `json:"code"`
	} `json:"error,omitempty"`
	Status     json.RawMessage            `json:"status,omitempty"`
	StatusCode json.RawMessage            `json:"status_code,omitempty"`
	Headers    map[string]json.RawMessage `json:"headers,omitempty"`
}

type responsesLedgerEntry struct {
	kind        string
	itemID      string
	outputIndex *int
	callID      string
	phase       string
	parts       map[int]string
	partOrder   []int
	call        *ToolCall
	completed   bool
}

type responsesStreamState struct {
	content                  strings.Builder
	thinking                 strings.Builder
	usage                    *UsageStats
	finishReason             string
	reasoningID              string
	sawDone                  bool
	sawContent               bool
	sawToolCall              bool
	sawThinking              bool
	pendingThinkingSeparator bool
	ledger                   []responsesLedgerEntry
	current                  *int
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

//nolint:gocyclo // The event switch dispatches the shared SSE and WS protocol stream.
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
	case "response.output_item.added":
		if payload.Item.Type == "message" || payload.Item.Type == "function_call" {
			entry, err := state.resolve(payload.Item.Type, payload.Item.ID, payload.OutputIndex, payload.Item.CallID, payload.Item.Type == "function_call")
			if err != nil {
				return false, err
			}
			if payload.Item.Phase != "" {
				entry.phase = payload.Item.Phase
			}
		}
		return false, nil
	case "response.output_text.delta":
		return handleResponsesTextDelta(state, payload, emit)
	case "response.output_text.done":
		return handleResponsesTextDone(state, payload)
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return handleResponsesReasoningDelta(state, payload.Delta, emit)
	case "response.reasoning_summary_part.added", "response.reasoning_summary_text.done":
		state.pendingThinkingSeparator = true
		return false, nil
	case "response.output_item.done":
		return handleResponsesOutputItemDone(state, payload.Item, payload.OutputIndex)
	case "response.completed", "response.incomplete":
		return handleResponsesCompleted(state, payload.Response, emit)
	case "response.failed":
		return false, responsesFailedError(payload.Response)
	}
	return false, nil
}

func handleResponsesTextDelta(state *responsesStreamState, payload responsesStreamEvent, emit func(ChatChunk) error) (bool, error) {
	delta := payload.Delta
	if delta == "" {
		return false, nil
	}
	entry, err := state.resolve("message", payload.ItemID, payload.OutputIndex, "", false)
	if err != nil {
		return false, err
	}
	part := 0
	if payload.ContentIndex != nil {
		part = *payload.ContentIndex
	}
	entry.appendPart(part, delta)
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

//nolint:gocyclo // Item completion updates the ledger entry resolved by all aliases.
func handleResponsesOutputItemDone(state *responsesStreamState, item responsesItem, outputIndex *int) (bool, error) {
	if item.Type == "reasoning" {
		state.reasoningID = item.ID
		state.pendingThinkingSeparator = true
		state.current = nil
		return false, nil
	}
	if item.Type == "message" {
		entry, err := state.resolve("message", item.ID, outputIndex, "", true)
		if err != nil {
			return false, err
		}
		if item.Phase != "" {
			entry.phase = item.Phase
		}
		if item.Content != nil {
			entry.parts = make(map[int]string)
			entry.partOrder = nil
			for i, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					entry.parts[i] = part.Text
					entry.partOrder = append(entry.partOrder, i)
				}
			}
		}
		state.current = nil
		return false, nil
	}
	if item.Type != "function_call" {
		return false, nil
	}
	call, err := responsesToolCall(item)
	if err != nil {
		return false, err
	}
	entry, err := state.resolve("function_call", item.ID, outputIndex, item.CallID, true)
	if err != nil {
		return false, err
	}
	mergeResponsesToolCall(entry, call)
	entry.completed = true
	state.current = nil
	return false, nil
}

func mergeResponsesToolCall(entry *responsesLedgerEntry, call ToolCall) {
	if entry.call == nil {
		entry.call = &ToolCall{}
	}
	if call.ID != "" {
		entry.call.ID = call.ID
	}
	if call.Name != "" {
		entry.call.Name = call.Name
	}
	if call.RawArguments != "" {
		entry.call.RawArguments = call.RawArguments
		entry.call.Arguments = call.Arguments
	}
	if entry.call.ID != "" {
		entry.callID = entry.call.ID
	}
}

func (entry *responsesLedgerEntry) appendPart(index int, text string) {
	if entry.parts == nil {
		entry.parts = make(map[int]string)
	}
	if _, ok := entry.parts[index]; !ok {
		entry.partOrder = append(entry.partOrder, index)
	}
	entry.parts[index] += text
}

func handleResponsesTextDone(state *responsesStreamState, payload responsesStreamEvent) (bool, error) {
	entry, err := state.resolve("message", payload.ItemID, payload.OutputIndex, "", false)
	if err != nil {
		return false, err
	}
	index := 0
	if payload.ContentIndex != nil {
		index = *payload.ContentIndex
	}
	if entry.parts == nil {
		entry.parts = make(map[int]string)
	}
	if _, ok := entry.parts[index]; !ok {
		entry.partOrder = append(entry.partOrder, index)
	}
	text := payload.Text
	if text == "" {
		text = payload.Delta
	}
	entry.parts[index] = text
	return false, nil
}

//nolint:gocyclo // Resolve all aliases and validate every conflict before binding.
func (state *responsesStreamState) resolve(kind, id string, outputIndex *int, callID string, closeBoundary bool) (*responsesLedgerEntry, error) {
	matches, err := resolveAliasMatches(state, id, outputIndex, callID)
	if err != nil {
		return nil, err
	}
	idx := -1
	if len(matches) == 1 {
		idx = matches[0]
	}
	if idx < 0 {
		idx = state.currentCandidate(kind, id, outputIndex, callID)
	}
	if idx >= 0 {
		e := &state.ledger[idx]
		if e.kind != kind || (id != "" && e.itemID != "" && e.itemID != id) || (outputIndex != nil && e.outputIndex != nil && *e.outputIndex != *outputIndex) || (callID != "" && e.callID != "" && e.callID != callID) {
			return nil, fmt.Errorf("conflicting Codex item kind or identity")
		}
		if id != "" {
			e.itemID = id
		}
		if outputIndex != nil {
			v := *outputIndex
			e.outputIndex = &v
		}
		if callID != "" {
			e.callID = callID
		}
		v := idx
		state.current = &v
		if closeBoundary {
			state.current = nil
		}
		return e, nil
	}
	e := responsesLedgerEntry{kind: kind}
	if id != "" {
		e.itemID = id
	}
	if outputIndex != nil {
		v := *outputIndex
		e.outputIndex = &v
	}
	if callID != "" {
		e.callID = callID
	}
	state.ledger = append(state.ledger, e)
	idx = len(state.ledger) - 1
	v := idx
	state.current = &v
	if closeBoundary {
		state.current = nil
	}
	return &state.ledger[idx], nil
}

func responsesFailedError(response responsesResponse) error {
	msg, code := "", ""
	if response.Error != nil {
		msg = response.Error.Message
		code = strings.Trim(strings.TrimSpace(string(response.Error.Code)), `"`)
		if code == "null" {
			code = ""
		}
	}
	sentinel := errResponsesStreamFailed
	if transientResponsesFailureCodes[code] {
		sentinel = errResponsesStreamFailedTransient
	}
	switch {
	case msg != "" && code != "":
		return fmt.Errorf("%w: %s (code %s)", sentinel, msg, code)
	case msg != "":
		return fmt.Errorf("%w: %s", sentinel, msg)
	case code != "":
		return fmt.Errorf("%w: code %s", sentinel, code)
	}
	return sentinel
}

// transientResponsesFailureCodes are response.failed error codes that reflect
// server load rather than a verdict on the request, so a resend may succeed.
var transientResponsesFailureCodes = map[string]bool{
	"server_error":        true,
	"rate_limit_exceeded": true,
}

//nolint:gocyclo // Terminal recovery preserves response and streamed state semantics.
func handleResponsesCompleted(state *responsesStreamState, response responsesResponse, emit func(ChatChunk) error) (bool, error) {
	state.sawDone = true
	resp, err := normalizeResponsesResponse(response)
	if err != nil {
		return false, err
	}
	if len(response.Output) > 0 {
		ledger, err := completedResponsesItems(response.Output, state.ledger)
		if err != nil {
			return false, err
		}
		_, calls, text := (&responsesStreamState{ledger: ledger}).projected()
		streamed := state.content.String()
		if strings.HasPrefix(text, streamed) && len(text) > len(streamed) {
			if err := emit(ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: text[len(streamed):]}}); err != nil {
				return false, err
			}
		}
		state.content.Reset()
		state.content.WriteString(text)
		state.sawContent = text != ""
		state.ledger = ledger
		state.sawToolCall = len(calls) > 0
	} else {
		_, _, text := state.projected()
		state.content.Reset()
		state.content.WriteString(text)
		state.sawContent = text != ""
		if text == "" && resp.Message.Content != "" {
			state.content.WriteString(resp.Message.Content)
			state.sawContent = true
		}
		if len(resp.Message.ToolCalls) > 0 {
			for _, call := range resp.Message.ToolCalls {
				e := responsesLedgerEntry{kind: "function_call", call: &call, callID: call.ID, completed: true}
				state.ledger = append(state.ledger, e)
			}
			state.sawToolCall = true
		}
	}
	if resp.Message.ReasoningContent != "" && !state.sawThinking {
		state.thinking.WriteString(resp.Message.ReasoningContent)
		state.sawThinking = true
	}
	if resp.Message.ProviderMetadata != nil && resp.Message.ProviderMetadata.Codex != nil && resp.Message.ProviderMetadata.Codex.ReasoningID != "" && state.reasoningID == "" {
		state.reasoningID = resp.Message.ProviderMetadata.Codex.ReasoningID
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

func hasCodexPhase(blocks []CodexMessageBlock) bool {
	for _, block := range blocks {
		if block.Phase != "" {
			return true
		}
	}
	return false
}

func responsesStreamStateToChatChunk(state responsesStreamState) ChatChunk {
	message := Message{Role: MessageRoleAssistant}
	blocks, calls, content := state.projected()
	if state.sawContent {
		message.Content = content
	}
	if state.sawThinking {
		message.ReasoningContent = state.thinking.String()
	}
	if state.reasoningID != "" || len(blocks) > 1 || hasCodexPhase(blocks) || len(calls) > 0 {
		message.ProviderMetadata = &MessageProviderMetadata{Codex: &CodexMessageMetadata{ReasoningID: state.reasoningID, Blocks: blocks}}
	}
	if len(calls) > 0 {
		message.ToolCalls = calls
	}
	return ChatChunk{
		Delta:           message,
		ContentSnapshot: true,
		Usage:           state.usage,
		Done:            true,
		FinishReason:    state.finishReason,
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
