package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

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
