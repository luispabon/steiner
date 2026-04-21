package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var defaultHTTPClient = &http.Client{}

type OpenAICompatConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
	Scheduler  *Scheduler
}

type OpenAICompat struct {
	baseURL    *url.URL
	apiKey     string
	model      string
	httpClient *http.Client
	scheduler  *Scheduler
}

func NewOpenAICompat(cfg OpenAICompatConfig) (*OpenAICompat, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if cfg.Scheduler == nil {
		return nil, fmt.Errorf("scheduler is required")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = defaultHTTPClient
	}
	return &OpenAICompat{
		baseURL:    parsed,
		apiKey:     cfg.APIKey,
		model:      cfg.Model,
		httpClient: client,
		scheduler:  cfg.Scheduler,
	}, nil
}

func (p *OpenAICompat) SupportsUsageStats() bool {
	return p != nil
}

func (p *OpenAICompat) ChatCompletion(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	if p == nil {
		return ChatResponse{}, fmt.Errorf("provider is not initialized")
	}
	if err := p.acquire(ctx); err != nil {
		return ChatResponse{}, err
	}
	defer p.release()

	payload, err := p.doChatCompletion(ctx, request, false)
	if err != nil {
		return ChatResponse{}, err
	}
	return normalizeChatResponse(payload)
}

func (p *OpenAICompat) StreamChatCompletion(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error) {
	if p == nil {
		return nil, fmt.Errorf("provider is not initialized")
	}
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}

	out := make(chan ChatChunk)
	go func() {
		defer close(out)
		defer p.release()

		if err := p.streamChatCompletion(ctx, request, out); err != nil {
			select {
			case out <- ChatChunk{Done: true}:
			case <-ctx.Done():
			}
		}
	}()

	return out, nil
}

func (p *OpenAICompat) acquire(ctx context.Context) error {
	if p.scheduler == nil {
		return nil
	}
	return p.scheduler.Acquire(ctx)
}

func (p *OpenAICompat) release() {
	if p.scheduler == nil {
		return
	}
	p.scheduler.Release()
}

func (p *OpenAICompat) doChatCompletion(ctx context.Context, request ChatRequest, stream bool) (*openAIResponse, error) {
	body, err := p.marshalRequest(request, stream)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.chatCompletionsURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, p.readErrorResponse(resp)
	}

	var payload openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode chat completion response: %w", err)
	}
	return &payload, nil
}

func (p *OpenAICompat) streamChatCompletion(ctx context.Context, request ChatRequest, out chan<- ChatChunk) error {
	body, err := p.marshalRequest(request, true)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.chatCompletionsURL(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if strings.TrimSpace(p.apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return p.readErrorResponse(resp)
	}

	return decodeChatStream(ctx, resp.Body, out)
}

func (p *OpenAICompat) chatCompletionsURL() string {
	base := *p.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/chat/completions"
	return base.String()
}

func (p *OpenAICompat) marshalRequest(request ChatRequest, stream bool) ([]byte, error) {
	wire := openAIRequest{
		Model:       p.model,
		Messages:    make([]openAIMessage, 0, len(request.Messages)),
		Temperature: request.Temperature,
		MaxTokens:   request.MaxTokens,
		TopP:        request.TopP,
		Stream:      stream,
	}
	if strings.TrimSpace(request.Model) != "" {
		wire.Model = request.Model
	}
	if stream {
		wire.StreamOptions = &openAIStreamOptions{IncludeUsage: true}
	}
	if len(request.Tools) > 0 {
		wire.Tools = make([]openAITool, 0, len(request.Tools))
		for _, tool := range request.Tools {
			wire.Tools = append(wire.Tools, openAITool{
				Type: tool.Type,
				Function: openAIToolFunction{
					Name:        tool.Function.Name,
					Description: tool.Function.Description,
					Parameters:  tool.Function.Parameters,
				},
			})
		}
	}
	for _, msg := range request.Messages {
		wireMsg, err := toOpenAIMessage(msg)
		if err != nil {
			return nil, err
		}
		wire.Messages = append(wire.Messages, wireMsg)
	}
	return json.Marshal(wire)
}

func (p *OpenAICompat) readErrorResponse(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if len(body) == 0 {
		return fmt.Errorf("chat completions request failed: %s", resp.Status)
	}
	return fmt.Errorf("chat completions request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
}

type openAIRequest struct {
	Model         string               `json:"model"`
	Messages      []openAIMessage      `json:"messages"`
	Temperature   *float64             `json:"temperature,omitempty"`
	MaxTokens     *int                 `json:"max_tokens,omitempty"`
	TopP          *float64             `json:"top_p,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
	Tools         []openAITool         `json:"tools,omitempty"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role,omitempty"`
	Content    any              `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type openAIToolCall struct {
	ID       string                 `json:"id,omitempty"`
	Index    int                    `json:"index,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function openAIToolCallFunction `json:"function"`
}

type openAIToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
	Usage   *UsageStats    `json:"usage,omitempty"`
}

type openAIChoice struct {
	Message      openAIMessage `json:"message"`
	Delta        openAIMessage `json:"delta"`
	FinishReason string        `json:"finish_reason,omitempty"`
}

type openAIToolCallAccumulator struct {
	ID        string
	Type      string
	Name      string
	Arguments strings.Builder
}

type openAIStreamState struct {
	content       strings.Builder
	toolCalls     map[int]*openAIToolCallAccumulator
	finishReason  string
	usage         *UsageStats
	sawContent    bool
	sawToolCall   bool
	assistantRole bool
}

func normalizeChatResponse(payload *openAIResponse) (ChatResponse, error) {
	if payload == nil {
		return ChatResponse{}, fmt.Errorf("empty provider response")
	}
	if len(payload.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("provider response contained no choices")
	}
	choice := payload.Choices[0]
	message, err := normalizeMessage(choice.Message)
	if err != nil {
		return ChatResponse{}, err
	}
	return ChatResponse{
		Message:      message,
		Usage:        payload.Usage,
		FinishReason: choice.FinishReason,
	}, nil
}

func decodeChatStream(ctx context.Context, body io.Reader, out chan<- ChatChunk) error {
	reader := bufio.NewReader(body)
	state := openAIStreamState{
		toolCalls: make(map[int]*openAIToolCallAccumulator),
	}

	for {
		event, err := readSSEEvent(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		if len(event) == 0 {
			continue
		}
		if event == "[DONE]" {
			break
		}

		var payload openAIResponse
		if err := json.Unmarshal([]byte(event), &payload); err != nil {
			return fmt.Errorf("decode stream chunk: %w", err)
		}
		if payload.Usage != nil {
			state.usage = payload.Usage
		}
		if len(payload.Choices) == 0 {
			continue
		}
		choice := payload.Choices[0]
		if choice.FinishReason != "" {
			state.finishReason = choice.FinishReason
		}

		if choice.Delta.Role == "assistant" {
			state.assistantRole = true
		}
		if content := stringOrEmpty(choice.Delta.Content); content != "" {
			state.content.WriteString(content)
			state.sawContent = true
			select {
			case out <- ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: content}}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if len(choice.Delta.ToolCalls) > 0 {
			state.sawToolCall = true
			for _, toolCall := range choice.Delta.ToolCalls {
				idx := toolCall.Index
				acc := state.toolCalls[idx]
				if acc == nil {
					acc = &openAIToolCallAccumulator{}
					state.toolCalls[idx] = acc
				}
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
		}
	}

	return flushStreamState(ctx, out, state)
}

func flushStreamState(ctx context.Context, out chan<- ChatChunk, state openAIStreamState) error {
	if !state.sawContent && !state.sawToolCall && state.finishReason == "" && state.usage == nil {
		return nil
	}

	message := Message{Role: MessageRoleAssistant}
	if state.sawContent {
		message.Content = state.content.String()
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
	select {
	case out <- chunk:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

func normalizeMessage(message openAIMessage) (Message, error) {
	out := Message{
		Role: MessageRole(message.Role),
	}
	if content := stringOrEmpty(message.Content); content != "" {
		out.Content = content
	}
	if message.Name != "" {
		out.Name = message.Name
	}
	if message.ToolCallID != "" {
		out.ToolCallID = message.ToolCallID
	}
	if len(message.ToolCalls) > 0 {
		toolCalls, err := normalizeToolCalls(message.ToolCalls)
		if err != nil {
			return Message{}, err
		}
		out.ToolCalls = toolCalls
	}
	return out, nil
}

func normalizeToolCalls(toolCalls []openAIToolCall) ([]ToolCall, error) {
	out := make([]ToolCall, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		args := make(map[string]any)
		rawArgs := strings.TrimSpace(toolCall.Function.Arguments)
		if rawArgs != "" {
			if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
				return nil, fmt.Errorf("decode tool call %q arguments: %w", toolCall.Function.Name, err)
			}
		}
		out = append(out, ToolCall{
			ID:        toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: args,
		})
	}
	return out, nil
}

func toOpenAIMessage(message Message) (openAIMessage, error) {
	wire := openAIMessage{
		Role: string(message.Role),
		Name: message.Name,
	}
	switch message.Role {
	case MessageRoleAssistant, MessageRoleSystem, MessageRoleUser:
		if message.Content != "" {
			wire.Content = message.Content
		} else if message.Role == MessageRoleAssistant {
			wire.Content = nil
		}
	case MessageRoleTool:
		wire.Content = message.Content
		wire.ToolCallID = message.ToolCallID
	default:
		wire.Content = message.Content
	}
	if len(message.ToolCalls) > 0 {
		wire.ToolCalls = make([]openAIToolCall, 0, len(message.ToolCalls))
		for _, toolCall := range message.ToolCalls {
			args, err := json.Marshal(toolCall.Arguments)
			if err != nil {
				return openAIMessage{}, fmt.Errorf("encode tool call %q arguments: %w", toolCall.Name, err)
			}
			wire.ToolCalls = append(wire.ToolCalls, openAIToolCall{
				ID:   toolCall.ID,
				Type: "function",
				Function: openAIToolCallFunction{
					Name:      toolCall.Name,
					Arguments: string(args),
				},
			})
		}
	}
	return wire, nil
}

func stringOrEmpty(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case *string:
		if v == nil {
			return ""
		}
		return *v
	default:
		return ""
	}
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
