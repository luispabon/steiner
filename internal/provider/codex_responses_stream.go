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
	blocks                   []CodexMessageBlock
	currentBlock             int
	blockByID                map[string]int
	blockByOutputIndex       map[int]int
	seenCalls                map[string]bool
	blockIDs                 []string
	blockOutputIndexes       []*int
	anonymousTextBlock       int
	hasAnonymousTextBlock    bool
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
		switch payload.Item.Type {
		case "message":
			block := state.currentTextBlock(payload.Item.ID, payload.OutputIndex)
			if payload.Item.Phase != "" {
				state.blocks[block].Phase = payload.Item.Phase
			}
		case "function_call":
			state.currentCallBlock(payload.Item, payload.OutputIndex)
		}
		return false, nil
	case "response.output_text.delta":
		return handleResponsesTextDelta(state, payload, emit)
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
	state.content.WriteString(delta)
	state.sawContent = true
	block := state.currentTextBlock(payload.ItemID, payload.OutputIndex)
	state.blocks[block].Text += delta
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

//nolint:gocyclo // Item completion updates message or call identity records.
func handleResponsesOutputItemDone(state *responsesStreamState, item responsesItem, outputIndex *int) (bool, error) {
	if item.Type == "reasoning" {
		state.reasoningID = item.ID
		state.pendingThinkingSeparator = true
		return false, nil
	}
	if item.Type == "message" {
		block := state.currentTextBlock(item.ID, outputIndex)
		if item.Phase != "" {
			state.blocks[block].Phase = item.Phase
		}
		var text strings.Builder
		for _, part := range item.Content {
			if part.Type == "output_text" || part.Type == "text" {
				text.WriteString(part.Text)
			}
		}
		if text.Len() > 0 {
			state.blocks[block].Text = text.String()
		}
		return false, nil
	}
	if item.Type != "function_call" {
		return false, nil
	}
	call, err := responsesToolCall(item)
	if err != nil {
		return false, err
	}
	block := state.currentCallBlock(item, outputIndex)
	callIndex := 0
	for i := 0; i < block; i++ {
		if state.blocks[i].Kind == "function_call" {
			callIndex++
		}
	}
	if callIndex >= 0 && callIndex < len(state.toolCalls) {
		if call.ID != "" {
			state.toolCalls[callIndex].ID = call.ID
		}
		if call.Name != "" {
			state.toolCalls[callIndex].Name = call.Name
		}
		if call.RawArguments != "" {
			state.toolCalls[callIndex].RawArguments = call.RawArguments
			state.toolCalls[callIndex].Arguments = call.Arguments
		}
		state.blocks[block].CallID = state.toolCalls[callIndex].ID
		return false, nil
	}
	if call.ID != "" {
		if state.seenCalls == nil {
			state.seenCalls = make(map[string]bool)
		}
		if state.seenCalls[call.ID] {
			return false, nil
		}
		state.seenCalls[call.ID] = true
	}
	state.toolCalls = append(state.toolCalls, call)
	state.blocks[block].CallID = call.ID
	state.sawToolCall = true
	return false, nil
}

func (state *responsesStreamState) currentCallBlock(item responsesItem, outputIndex *int) int {
	if state.blockByID == nil {
		state.blockByID = make(map[string]int)
		state.blockByOutputIndex = make(map[int]int)
	}
	if item.ID != "" {
		if i, ok := state.blockByID[item.ID]; ok {
			return i
		}
	}
	if outputIndex != nil {
		if i, ok := state.blockByOutputIndex[*outputIndex]; ok {
			if item.ID != "" {
				state.blockByID[item.ID] = i
				state.blockIDs[i] = item.ID
			}
			return i
		}
	}
	block := len(state.blocks)
	state.blocks = append(state.blocks, CodexMessageBlock{Kind: "function_call", CallID: item.CallID})
	state.blockIDs = append(state.blockIDs, item.ID)
	state.blockOutputIndexes = append(state.blockOutputIndexes, outputIndex)
	if item.ID != "" {
		state.blockByID[item.ID] = block
	}
	if outputIndex != nil {
		state.blockByOutputIndex[*outputIndex] = block
	}
	return block
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

//nolint:gocyclo // Identity aliases must resolve to one output item across sparse event shapes.
func (state *responsesStreamState) currentTextBlock(id string, index *int) int {
	if state.blockByID == nil {
		state.blockByID = make(map[string]int)
		state.blockByOutputIndex = make(map[int]int)
	}
	block, found := -1, false
	if id != "" {
		block, found = state.blockByID[id]
	}
	if !found && index != nil {
		block, found = state.blockByOutputIndex[*index]
	}
	if !found && state.hasAnonymousTextBlock {
		candidate := state.anonymousTextBlock
		if candidate < len(state.blockIDs) && state.blockIDs[candidate] == "" {
			block, found = candidate, true
		}
	}
	if !found {
		block = len(state.blocks)
		state.blocks = append(state.blocks, CodexMessageBlock{Kind: "message"})
		state.blockIDs = append(state.blockIDs, "")
		state.blockOutputIndexes = append(state.blockOutputIndexes, nil)
	}
	if state.blocks[block].Kind == "message" && state.blockIDs[block] == "" {
		state.anonymousTextBlock, state.hasAnonymousTextBlock = block, true
	}
	if id != "" || index != nil {
		state.anonymousTextBlock, state.hasAnonymousTextBlock = block, true
	}
	if id != "" {
		state.blockByID[id] = block
		state.blockIDs[block] = id
	}
	if index != nil {
		state.blockByOutputIndex[*index] = block
		v := *index
		state.blockOutputIndexes[block] = &v
	}
	state.currentBlock = block
	return block
}

//nolint:gocyclo // Terminal response recovery combines authoritative text, calls, and metadata.
func handleResponsesCompleted(state *responsesStreamState, response responsesResponse, emit func(ChatChunk) error) (bool, error) {
	state.sawDone = true
	resp, err := normalizeResponsesResponse(response)
	if err != nil {
		return false, err
	}
	if len(response.Output) > 0 {
		blocks, calls, content, err := completedResponsesItems(response.Output, state.blocks, state.blockIDs, state.blockOutputIndexes)
		if err != nil {
			return false, err
		}
		streamed := state.content.String()
		if strings.HasPrefix(content, streamed) && len(content) > len(streamed) {
			if err := emit(ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: content[len(streamed):]}}); err != nil {
				return false, err
			}
		}
		state.content.Reset()
		state.content.WriteString(content)
		state.sawContent = content != ""
		state.blocks = blocks
		state.toolCalls = calls
	} else if resp.Message.Content != "" && !state.sawContent {
		state.content.WriteString(resp.Message.Content)
		state.sawContent = true
	}
	if len(response.Output) > 0 {
		state.sawToolCall = len(state.toolCalls) > 0
	} else if len(resp.Message.ToolCalls) > 0 && !state.sawToolCall {
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
	if state.sawContent {
		message.Content = state.content.String()
	}
	if state.sawThinking {
		message.ReasoningContent = state.thinking.String()
	}
	if state.reasoningID != "" || len(state.blocks) > 1 || hasCodexPhase(state.blocks) || len(state.toolCalls) > 0 {
		message.ProviderMetadata = &MessageProviderMetadata{Codex: &CodexMessageMetadata{ReasoningID: state.reasoningID, Blocks: state.blocks}}
	}
	if state.sawToolCall {
		message.ToolCalls = state.toolCalls
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
