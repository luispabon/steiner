package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
)

func TestEstimateMessageTokensReturnsPositiveForContent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	msg := Message{
		Role:    MessageRoleUser,
		Content: "Hello, world! This is a test message with enough content to produce tokens.",
	}
	count, err := EstimateMessageTokens(ctx, "gpt-4o", msg)
	if err != nil {
		t.Fatalf("EstimateMessageTokens() error = %v", err)
	}
	if count <= messageOverheadTokens {
		t.Fatalf("count = %d, want > %d", count, messageOverheadTokens)
	}
}

func TestEstimateMessageTokensRespectsCancelledCtx(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	msg := Message{Role: MessageRoleUser, Content: "hello"}
	_, err := EstimateMessageTokens(ctx, "gpt-4o", msg)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestEstimateMessageTokensErrorsOnInvalidModel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	msg := Message{Role: MessageRoleUser, Content: "hello"}
	_, err := EstimateMessageTokens(ctx, "", msg)
	if err != nil {
		t.Fatalf("EstimateMessageTokens() with empty model should fallback, got error = %v", err)
	}
}

func TestEstimateToolSpecTokensReturnsPositive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tool := ToolSpec{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "get_weather",
			Description: "Get the current weather for a location",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]any{
						"type":        "string",
						"description": "City name",
					},
				},
			},
		},
	}
	count, err := EstimateToolSpecTokens(ctx, "gpt-4o", tool)
	if err != nil {
		t.Fatalf("EstimateToolSpecTokens() error = %v", err)
	}
	if count <= toolSpecOverheadTokens {
		t.Fatalf("count = %d, want > %d", count, toolSpecOverheadTokens)
	}
}

func TestEstimateToolSpecTokensRespectsCancelledCtx(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := EstimateToolSpecTokens(ctx, "gpt-4o", ToolSpec{Type: "function"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestRequestOverheadTokensReturnsConstant(t *testing.T) {
	if got := RequestOverheadTokens(); got != requestOverheadTokens {
		t.Fatalf("RequestOverheadTokens() = %d, want %d", got, requestOverheadTokens)
	}
}

func TestUsageTokenCountExtractsTokens(t *testing.T) {
	tests := []struct {
		name  string
		usage *UsageStats
		want  int
	}{
		{name: "nil", usage: nil, want: 0},
		{name: "total tokens", usage: &UsageStats{TotalTokens: 100}, want: 100},
		{name: "sum prompt+completion", usage: &UsageStats{PromptTokens: 30, CompletionTokens: 20}, want: 50},
		{name: "total takes precedence", usage: &UsageStats{TotalTokens: 200, PromptTokens: 30, CompletionTokens: 20}, want: 200},
		{name: "zero valued", usage: &UsageStats{}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UsageTokenCount(tt.usage); got != tt.want {
				t.Fatalf("UsageTokenCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEncodingNameForModelMapsCorrectly(t *testing.T) {
	tests := []struct {
		model string
		want  tokenizer.Encoding
	}{
		{model: "gpt-4.5-preview", want: tokenizer.O200kBase},
		{model: "gpt-4.1-nano", want: tokenizer.O200kBase},
		{model: "gpt-4o", want: tokenizer.O200kBase},
		{model: "o1-mini", want: tokenizer.O200kBase},
		{model: "o3-mini", want: tokenizer.O200kBase},
		{model: "gpt-4", want: tokenizer.Cl100kBase},
		{model: "gpt-4-turbo", want: tokenizer.Cl100kBase},
		{model: "gpt-3.5-turbo", want: tokenizer.Cl100kBase},
		{model: "text-embedding-ada-002", want: tokenizer.Cl100kBase},
		{model: "text-embedding-3-small", want: tokenizer.Cl100kBase},
		{model: "text-davinci-003", want: tokenizer.P50kBase},
		{model: "code-davinci-002", want: tokenizer.P50kBase},
		{model: "code-cushman-001", want: tokenizer.P50kBase},
		{model: "davinci", want: tokenizer.R50kBase},
		{model: "curie", want: tokenizer.R50kBase},
		{model: "babbage", want: tokenizer.R50kBase},
		{model: "ada", want: tokenizer.R50kBase},
		{model: "gpt2", want: tokenizer.R50kBase},
		{model: "unknown-model", want: tokenizer.Cl100kBase},
		{model: "", want: tokenizer.Cl100kBase},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := encodingNameForModel(tt.model); got != tt.want {
				t.Fatalf("encodingNameForModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestEstimateChatRequestTokensCountsToolHeavySemanticPayload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	toolHeavy := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: MessageRoleSystem, Content: "system"},
			{Role: MessageRoleUser, Content: "review"},
			{
				Role:    MessageRoleAssistant,
				Content: "call tools",
				ToolCalls: []ToolCall{
					{
						ID:   "call_1",
						Name: "inspect",
						Arguments: map[string]any{
							"path":   "notes.txt",
							"filter": strings.Repeat("x", 64),
						},
					},
				},
			},
			{Role: MessageRoleTool, ToolCallID: "call_1", Name: "inspect", Content: strings.Repeat("tool result ", 12)},
		},
		Tools: []ToolSpec{
			{
				Type: "function",
				Function: ToolFunctionSpec{
					Name:        "inspect",
					Description: strings.Repeat("describe the inspection tool ", 4),
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{
								"type":        "string",
								"description": strings.Repeat("filesystem path ", 4),
							},
							"filter": map[string]any{
								"type":        "string",
								"description": strings.Repeat("selector ", 6),
							},
						},
						"required": []any{"path"},
					},
				},
			},
		},
	}

	lean := toolHeavy
	lean.Tools = nil

	leanTokens, err := EstimateChatRequestTokens(ctx, lean)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens(lean) error = %v", err)
	}
	heavyTokens, err := EstimateChatRequestTokens(ctx, toolHeavy)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens(tool-heavy) error = %v", err)
	}
	if heavyTokens <= leanTokens {
		t.Fatalf("tool-heavy tokens = %d, want > lean tokens %d", heavyTokens, leanTokens)
	}
}

func TestEstimateChatRequestTokensDoesNotCountWireWrapperSyntax(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	request := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: MessageRoleSystem, Content: "s"},
			{Role: MessageRoleUser, Content: "u"},
			{
				Role:    MessageRoleAssistant,
				Content: "a",
				ToolCalls: []ToolCall{
					{
						ID:   "call_1",
						Name: "lookup",
						Arguments: map[string]any{
							"query": "x",
						},
					},
				},
			},
			{Role: MessageRoleTool, ToolCallID: "call_1", Name: "lookup", Content: "result"},
		},
		Tools: []ToolSpec{
			{
				Type: "function",
				Function: ToolFunctionSpec{
					Name:        "lookup",
					Description: "lookup docs",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{
								"type":        "string",
								"description": "query text",
							},
						},
					},
				},
			},
		},
	}

	semanticTokens, err := EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	wire, err := chatRequestWire(request, request.Model, request.Stream)
	if err != nil {
		t.Fatalf("chatRequestWire() error = %v", err)
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("json.Marshal(wire) error = %v", err)
	}
	enc, err := tokenizerForModel(wire.Model)
	if err != nil {
		t.Fatalf("tokenizerForModel() error = %v", err)
	}
	wireTokens, err := enc.Count(string(payload))
	if err != nil {
		t.Fatalf("Count(wire) error = %v", err)
	}

	if semanticTokens >= wireTokens {
		t.Fatalf("semantic tokens = %d, want less than wire JSON tokens %d", semanticTokens, wireTokens)
	}
	if diff := wireTokens - semanticTokens; diff < 10 {
		t.Fatalf("wire JSON tokens = %d, semantic tokens = %d, want wrapper overhead gap >= 10", wireTokens, semanticTokens)
	}
}
