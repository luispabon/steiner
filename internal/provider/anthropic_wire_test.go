package provider

import (
	"encoding/json"
	"testing"
)

func TestAnthropicUsageToUsageStats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		usage *anthropicUsage
		want  *UsageStats
	}{
		{
			name:  "nil",
			usage: nil,
			want:  nil,
		},
		{
			name: "no cache",
			usage: &anthropicUsage{
				InputTokens:  11,
				OutputTokens: 7,
			},
			want: &UsageStats{
				PromptTokens:     11,
				CompletionTokens: 7,
				TotalTokens:      18,
			},
		},
		{
			name: "cache creation",
			usage: &anthropicUsage{
				InputTokens:              11,
				OutputTokens:             7,
				CacheCreationInputTokens: 3,
			},
			want: &UsageStats{
				PromptTokens:             14,
				CompletionTokens:         7,
				TotalTokens:              21,
				CacheCreationInputTokens: 3,
			},
		},
		{
			name: "cache read",
			usage: &anthropicUsage{
				InputTokens:          11,
				OutputTokens:         7,
				CacheReadInputTokens: 4,
			},
			want: &UsageStats{
				PromptTokens:         15,
				CompletionTokens:     7,
				TotalTokens:          22,
				CacheReadInputTokens: 4,
			},
		},
		{
			name: "both cache fields",
			usage: &anthropicUsage{
				InputTokens:              11,
				OutputTokens:             7,
				CacheCreationInputTokens: 3,
				CacheReadInputTokens:     4,
			},
			want: &UsageStats{
				PromptTokens:             18,
				CompletionTokens:         7,
				TotalTokens:              25,
				CacheCreationInputTokens: 3,
				CacheReadInputTokens:     4,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.usage.toUsageStats()
			if tt.want == nil {
				if got != nil {
					t.Fatalf("toUsageStats() = %#v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("toUsageStats() = nil, want usage stats")
			}
			if got.PromptTokens != tt.want.PromptTokens {
				t.Fatalf("PromptTokens = %d, want %d", got.PromptTokens, tt.want.PromptTokens)
			}
			if got.CompletionTokens != tt.want.CompletionTokens {
				t.Fatalf("CompletionTokens = %d, want %d", got.CompletionTokens, tt.want.CompletionTokens)
			}
			if got.TotalTokens != tt.want.TotalTokens {
				t.Fatalf("TotalTokens = %d, want %d", got.TotalTokens, tt.want.TotalTokens)
			}
			if got.CacheCreationInputTokens != tt.want.CacheCreationInputTokens {
				t.Fatalf("CacheCreationInputTokens = %d, want %d", got.CacheCreationInputTokens, tt.want.CacheCreationInputTokens)
			}
			if got.CacheReadInputTokens != tt.want.CacheReadInputTokens {
				t.Fatalf("CacheReadInputTokens = %d, want %d", got.CacheReadInputTokens, tt.want.CacheReadInputTokens)
			}
		})
	}
}

func TestAnthropicMessage_UserWithImage(t *testing.T) {
	tests := []struct {
		name    string
		message Message
		want    struct {
			numBlocks int
			types     []string
		}
	}{
		{
			name: "user message with text and one image",
			message: Message{
				Role:    MessageRoleUser,
				Content: "What's in this image?",
				Images: []ImageBlock{{
					MediaType: "image/png",
					Data:      "iVBORw0KGgoAAAANS...",
					Width:     100,
					Height:    100,
					SizeBytes: 1024,
				}},
			},
			want: struct {
				numBlocks int
				types     []string
			}{
				numBlocks: 2,
				types:     []string{"text", "image"},
			},
		},
		{
			name: "user message with text and multiple images",
			message: Message{
				Role:    MessageRoleUser,
				Content: "Compare these images",
				Images: []ImageBlock{
					{
						MediaType: "image/jpeg",
						Data:      "AAECAwQFBgcICQoLDA==",
						Width:     200,
						Height:    200,
						SizeBytes: 2048,
					},
					{
						MediaType: "image/gif",
						Data:      "R0lGODlh...",
						Width:     150,
						Height:    150,
						SizeBytes: 1512,
					},
				},
			},
			want: struct {
				numBlocks int
				types     []string
			}{
				numBlocks: 3,
				types:     []string{"text", "image", "image"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toAnthropicMessage(tt.message)
			if result == nil {
				t.Fatal("toAnthropicMessage returned nil, want *anthropicMessage")
			}

			if got, want := result.Role, "user"; got != want {
				t.Fatalf("role = %q, want %q", got, want)
			}

			if got, want := len(result.Content), tt.want.numBlocks; got != want {
				t.Fatalf("number of blocks = %d, want %d", got, want)
			}

			for i, expectedType := range tt.want.types {
				if got, want := result.Content[i].Type, expectedType; got != want {
					t.Fatalf("block[%d].type = %q, want %q", i, got, want)
				}
			}

			// Verify text block content
			if tt.message.Content != "" {
				textBlock := result.Content[0]
				if got, want := textBlock.Text, tt.message.Content; got != want {
					t.Fatalf("text content = %q, want %q", got, want)
				}
			}

			// Verify image blocks have correct source structure
			for i, img := range tt.message.Images {
				blockIdx := 1 + i
				imageBlock := result.Content[blockIdx]
				if imageBlock.Source == nil {
					t.Fatalf("block[%d].Source = nil, want source", blockIdx)
				}
				if got, want := imageBlock.Source.Type, "base64"; got != want {
					t.Fatalf("block[%d].Source.Type = %q, want %q", blockIdx, got, want)
				}
				if got, want := imageBlock.Source.MediaType, img.MediaType; got != want {
					t.Fatalf("block[%d].Source.MediaType = %q, want %q", blockIdx, got, want)
				}
				if got, want := imageBlock.Source.Data, img.Data; got != want {
					t.Fatalf("block[%d].Source.Data = %q, want %q", blockIdx, got, want)
				}
			}

			// Verify JSON marshaling
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var parsed map[string]any
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			content, ok := parsed["content"].([]any)
			if !ok {
				t.Fatalf("content type = %T, want []any", parsed["content"])
			}

			// Verify image block JSON structure
			for i, img := range tt.message.Images {
				blockIdx := 1 + i
				block, ok := content[blockIdx].(map[string]any)
				if !ok {
					t.Fatalf("content[%d] type = %T, want map[string]any", blockIdx, content[blockIdx])
				}
				if got, want := block["type"], "image"; got != want {
					t.Fatalf("content[%d].type = %q, want %q", blockIdx, got, want)
				}

				source, ok := block["source"].(map[string]any)
				if !ok {
					t.Fatalf("content[%d].source type = %T, want map[string]any", blockIdx, block["source"])
				}
				if got, want := source["type"], "base64"; got != want {
					t.Fatalf("content[%d].source.type = %q, want %q", blockIdx, got, want)
				}
				if got, want := source["media_type"], img.MediaType; got != want {
					t.Fatalf("content[%d].source.media_type = %q, want %q", blockIdx, got, want)
				}
				if got, want := source["data"], img.Data; got != want {
					t.Fatalf("content[%d].source.data = %q, want %q", blockIdx, got, want)
				}
			}
		})
	}
}

func TestAnthropicMessage_UserOnlyImage(t *testing.T) {
	message := Message{
		Role: MessageRoleUser,
		Images: []ImageBlock{{
			MediaType: "image/webp",
			Data:      "UklGRiYAAABXQVZFZm10IBAAAAABAAEAQB==",
			Width:     64,
			Height:    64,
			SizeBytes: 512,
		}},
	}

	result := toAnthropicMessage(message)
	if result == nil {
		t.Fatal("toAnthropicMessage returned nil, want *anthropicMessage")
	}

	if got, want := result.Role, "user"; got != want {
		t.Fatalf("role = %q, want %q", got, want)
	}

	if got, want := len(result.Content), 1; got != want {
		t.Fatalf("number of blocks = %d, want %d", got, want)
	}

	imageBlock := result.Content[0]
	if got, want := imageBlock.Type, "image"; got != want {
		t.Fatalf("block.type = %q, want %q", got, want)
	}

	if imageBlock.Source == nil {
		t.Fatal("block.Source = nil, want source")
	}

	if got, want := imageBlock.Source.Type, "base64"; got != want {
		t.Fatalf("source.Type = %q, want %q", got, want)
	}
	if got, want := imageBlock.Source.MediaType, "image/webp"; got != want {
		t.Fatalf("source.MediaType = %q, want %q", got, want)
	}
}

func TestAnthropicMessage_UserNoContent(t *testing.T) {
	message := Message{
		Role: MessageRoleUser,
	}

	result := toAnthropicMessage(message)
	if result == nil {
		t.Fatal("toAnthropicMessage returned nil, want *anthropicMessage")
	}

	if got, want := len(result.Content), 1; got != want {
		t.Fatalf("number of blocks = %d, want %d", got, want)
	}

	if got, want := result.Content[0].Type, "text"; got != want {
		t.Fatalf("block.type = %q, want %q", got, want)
	}
	if got, want := result.Content[0].Text, ""; got != want {
		t.Fatalf("block.Text = %q, want empty string", got)
	}
}

func TestAnthropicMessage_ToolResultWithImage(t *testing.T) {
	tests := []struct {
		name    string
		message Message
		want    struct {
			numBlocks int
			types     []string
		}
	}{
		{
			name: "tool result with text and one image",
			message: Message{
				Role:       MessageRoleTool,
				ToolCallID: "toolu_123",
				Content:    `{"status": "success"}`,
				Images: []ImageBlock{{
					MediaType: "image/png",
					Data:      "iVBORw0KGgoAAAANS...",
					Width:     256,
					Height:    256,
					SizeBytes: 4096,
				}},
			},
			want: struct {
				numBlocks int
				types     []string
			}{
				numBlocks: 2,
				types:     []string{"tool_result", "image"},
			},
		},
		{
			name: "tool result with text and multiple images",
			message: Message{
				Role:       MessageRoleTool,
				ToolCallID: "toolu_456",
				Content:    "Here are the processed images",
				Images: []ImageBlock{
					{
						MediaType: "image/jpeg",
						Data:      "AABC...",
						Width:     300,
						Height:    300,
						SizeBytes: 3072,
					},
					{
						MediaType: "image/png",
						Data:      "iVBO...",
						Width:     300,
						Height:    300,
						SizeBytes: 3072,
					},
				},
			},
			want: struct {
				numBlocks int
				types     []string
			}{
				numBlocks: 3,
				types:     []string{"tool_result", "image", "image"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toAnthropicMessage(tt.message)
			if result == nil {
				t.Fatal("toAnthropicMessage returned nil, want *anthropicMessage")
			}

			if got, want := result.Role, "user"; got != want {
				t.Fatalf("role = %q, want %q", got, want)
			}

			if got, want := len(result.Content), tt.want.numBlocks; got != want {
				t.Fatalf("number of blocks = %d, want %d", got, want)
			}

			// Verify tool_result block
			toolResultBlock := result.Content[0]
			if got, want := toolResultBlock.Type, "tool_result"; got != want {
				t.Fatalf("block[0].type = %q, want %q", got, want)
			}
			if got, want := toolResultBlock.ToolUseID, tt.message.ToolCallID; got != want {
				t.Fatalf("block[0].ToolUseID = %q, want %q", got, want)
			}
			if got, want := toolResultBlock.Content, tt.message.Content; got != want {
				t.Fatalf("block[0].Content = %q, want %q", got, want)
			}

			// Verify image blocks
			for i, img := range tt.message.Images {
				blockIdx := 1 + i
				imageBlock := result.Content[blockIdx]
				if got, want := imageBlock.Type, "image"; got != want {
					t.Fatalf("block[%d].type = %q, want %q", blockIdx, got, want)
				}
				if imageBlock.Source == nil {
					t.Fatalf("block[%d].Source = nil, want source", blockIdx)
				}
				if got, want := imageBlock.Source.Type, "base64"; got != want {
					t.Fatalf("block[%d].Source.Type = %q, want %q", blockIdx, got, want)
				}
				if got, want := imageBlock.Source.MediaType, img.MediaType; got != want {
					t.Fatalf("block[%d].Source.MediaType = %q, want %q", blockIdx, got, want)
				}
				if got, want := imageBlock.Source.Data, img.Data; got != want {
					t.Fatalf("block[%d].Source.Data = %q, want %q", blockIdx, got, want)
				}
			}

			// Verify JSON marshaling
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var parsed map[string]any
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			content, ok := parsed["content"].([]any)
			if !ok {
				t.Fatalf("content type = %T, want []any", parsed["content"])
			}

			// Verify tool_result block in JSON
			toolBlock, ok := content[0].(map[string]any)
			if !ok {
				t.Fatalf("content[0] type = %T, want map[string]any", content[0])
			}
			if got, want := toolBlock["type"], "tool_result"; got != want {
				t.Fatalf("content[0].type = %q, want %q", got, want)
			}
		})
	}
}

func TestAnthropicMessage_NoImages_Regression(t *testing.T) {
	tests := []struct {
		name    string
		message Message
	}{
		{
			name: "user message without images",
			message: Message{
				Role:    MessageRoleUser,
				Content: "hello world",
			},
		},
		{
			name: "user message with empty images slice",
			message: Message{
				Role:    MessageRoleUser,
				Content: "test",
				Images:  []ImageBlock{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toAnthropicMessage(tt.message)
			if result == nil {
				t.Fatal("toAnthropicMessage returned nil, want *anthropicMessage")
			}

			if got, want := result.Role, "user"; got != want {
				t.Fatalf("role = %q, want %q", got, want)
			}

			if got, want := len(result.Content), 1; got != want {
				t.Fatalf("number of blocks = %d, want %d", got, want)
			}

			if got, want := result.Content[0].Type, "text"; got != want {
				t.Fatalf("block.type = %q, want %q", got, want)
			}

			if got, want := result.Content[0].Text, tt.message.Content; got != want {
				t.Fatalf("block.Text = %q, want %q", got, want)
			}

			// Verify no Source field is set
			if result.Content[0].Source != nil {
				t.Fatalf("block.Source = %#v, want nil", result.Content[0].Source)
			}
		})
	}
}

func TestAnthropicMessage_ToolResultNoImages_Regression(t *testing.T) {
	message := Message{
		Role:       MessageRoleTool,
		ToolCallID: "toolu_xyz",
		Content:    `{"result": "done"}`,
	}

	result := toAnthropicMessage(message)
	if result == nil {
		t.Fatal("toAnthropicMessage returned nil, want *anthropicMessage")
	}

	if got, want := result.Role, "user"; got != want {
		t.Fatalf("role = %q, want %q", got, want)
	}

	if got, want := len(result.Content), 1; got != want {
		t.Fatalf("number of blocks = %d, want %d", got, want)
	}

	if got, want := result.Content[0].Type, "tool_result"; got != want {
		t.Fatalf("block.type = %q, want %q", got, want)
	}

	if got, want := result.Content[0].ToolUseID, message.ToolCallID; got != want {
		t.Fatalf("block.ToolUseID = %q, want %q", got, want)
	}

	if got, want := result.Content[0].Content, message.Content; got != want {
		t.Fatalf("block.Content = %q, want %q", got, want)
	}

	// Verify no Source field is set
	if result.Content[0].Source != nil {
		t.Fatalf("block.Source = %#v, want nil", result.Content[0].Source)
	}
}

func TestAnthropicRequestWire_UserMessageWithImages(t *testing.T) {
	request := ChatRequest{
		Model: "claude-3-7-sonnet",
		Messages: []Message{
			{
				Role:    MessageRoleUser,
				Content: "Describe this image",
				Images: []ImageBlock{{
					MediaType: "image/png",
					Data:      "base64encodeddata",
					Width:     100,
					Height:    100,
					SizeBytes: 1024,
				}},
			},
		},
	}

	wire := anthropicRequestWire(request, "default-model", false)

	if got, want := len(wire.Messages), 1; got != want {
		t.Fatalf("number of messages = %d, want %d", got, want)
	}

	msg := wire.Messages[0]
	if got, want := msg.Role, "user"; got != want {
		t.Fatalf("message role = %q, want %q", got, want)
	}

	if got, want := len(msg.Content), 2; got != want {
		t.Fatalf("content blocks = %d, want %d", got, want)
	}

	// Verify text and image blocks are present
	if got, want := msg.Content[0].Type, "text"; got != want {
		t.Fatalf("block[0].type = %q, want %q", got, want)
	}
	if got, want := msg.Content[1].Type, "image"; got != want {
		t.Fatalf("block[1].type = %q, want %q", got, want)
	}

	// Verify JSON marshaling produces correct structure
	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	messages, ok := parsed["messages"].([]any)
	if !ok {
		t.Fatalf("messages type = %T, want []any", parsed["messages"])
	}

	msgMap, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("message type = %T, want map[string]any", messages[0])
	}

	content, ok := msgMap["content"].([]any)
	if !ok {
		t.Fatalf("content type = %T, want []any", msgMap["content"])
	}

	imageBlock, ok := content[1].(map[string]any)
	if !ok {
		t.Fatalf("image block type = %T, want map[string]any", content[1])
	}

	if got, want := imageBlock["type"], "image"; got != want {
		t.Fatalf("image block type = %q, want %q", got, want)
	}

	source, ok := imageBlock["source"].(map[string]any)
	if !ok {
		t.Fatalf("source type = %T, want map[string]any", imageBlock["source"])
	}

	if got, want := source["type"], "base64"; got != want {
		t.Fatalf("source type = %q, want %q", got, want)
	}
	if got, want := source["media_type"], "image/png"; got != want {
		t.Fatalf("media_type = %q, want %q", got, want)
	}
	if got, want := source["data"], "base64encodeddata"; got != want {
		t.Fatalf("data = %q, want %q", got, want)
	}
}

func TestAnthropicCacheControl_OnSystemBlock(t *testing.T) {
	request := ChatRequest{
		Model: "claude-3-5-sonnet",
		Messages: []Message{
			{
				Role:    MessageRoleSystem,
				Content: "You are a helpful assistant",
			},
			{
				Role:    MessageRoleUser,
				Content: "Hello",
			},
		},
	}

	wire := anthropicRequestWire(request, "default-model", false)

	if len(wire.System) != 1 {
		t.Fatalf("system blocks = %d, want 1", len(wire.System))
	}

	if wire.System[0].CacheControl == nil {
		t.Fatal("system block CacheControl = nil, want cache control marker")
	}
	if got, want := wire.System[0].CacheControl.Type, "ephemeral"; got != want {
		t.Fatalf("cache control type = %q, want %q", got, want)
	}

	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	system, ok := parsed["system"].([]any)
	if !ok || len(system) == 0 {
		t.Fatal("system array missing or empty")
	}

	systemBlock, ok := system[0].(map[string]any)
	if !ok {
		t.Fatalf("system block type = %T, want map[string]any", system[0])
	}

	cacheCtrl, ok := systemBlock["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("cache_control missing from JSON")
	}
	if got, want := cacheCtrl["type"], "ephemeral"; got != want {
		t.Fatalf("cache_control.type = %q, want %q", got, want)
	}
}

func TestAnthropicCacheControl_OnToolWhenNoSystem(t *testing.T) {
	request := ChatRequest{
		Model: "claude-3-5-sonnet",
		Tools: []ToolSpec{
			{
				Type: "function",
				Function: ToolFunctionSpec{
					Name:        "get_weather",
					Description: "Get weather",
					Parameters:  map[string]any{},
				},
			},
		},
		Messages: []Message{
			{
				Role:    MessageRoleUser,
				Content: "What's the weather?",
			},
		},
	}

	wire := anthropicRequestWire(request, "default-model", false)

	if len(wire.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(wire.Tools))
	}

	if wire.Tools[0].CacheControl == nil {
		t.Fatal("tool CacheControl = nil, want cache control marker")
	}
	if got, want := wire.Tools[0].CacheControl.Type, "ephemeral"; got != want {
		t.Fatalf("cache control type = %q, want %q", got, want)
	}

	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	tools, ok := parsed["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatal("tools array missing or empty")
	}

	toolBlock, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool type = %T, want map[string]any", tools[0])
	}

	cacheCtrl, ok := toolBlock["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("cache_control missing from JSON")
	}
	if got, want := cacheCtrl["type"], "ephemeral"; got != want {
		t.Fatalf("cache_control.type = %q, want %q", got, want)
	}
}

func TestAnthropicCacheControl_OnFinalMessage(t *testing.T) {
	request := ChatRequest{
		Model: "claude-3-5-sonnet",
		Messages: []Message{
			{
				Role:    MessageRoleUser,
				Content: "First message",
			},
			{
				Role:    MessageRoleUser,
				Content: "Second message",
			},
		},
	}

	wire := anthropicRequestWire(request, "default-model", false)

	if len(wire.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(wire.Messages))
	}

	finalMsg := wire.Messages[len(wire.Messages)-1]
	if len(finalMsg.Content) == 0 {
		t.Fatal("final message has no content blocks")
	}

	lastBlock := finalMsg.Content[len(finalMsg.Content)-1]
	if lastBlock.CacheControl == nil {
		t.Fatal("final message last block CacheControl = nil, want cache control marker")
	}
	if got, want := lastBlock.CacheControl.Type, "ephemeral"; got != want {
		t.Fatalf("cache control type = %q, want %q", got, want)
	}
}

func TestAnthropicCacheControl_OnSecondToLastUserMessage(t *testing.T) {
	request := ChatRequest{
		Model: "claude-3-5-sonnet",
		Messages: []Message{
			{
				Role:    MessageRoleUser,
				Content: "First user message",
			},
			{
				Role:    MessageRoleAssistant,
				Content: "Response",
			},
			{
				Role:    MessageRoleUser,
				Content: "Second user message",
			},
		},
	}

	wire := anthropicRequestWire(request, "default-model", false)

	if len(wire.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(wire.Messages))
	}

	// Check second-to-last user message (index 0)
	if wire.Messages[0].Role != "user" {
		t.Fatalf("message[0].Role = %q, want user", wire.Messages[0].Role)
	}

	if len(wire.Messages[0].Content) == 0 {
		t.Fatal("second-to-last user message has no content blocks")
	}

	lastBlock := wire.Messages[0].Content[len(wire.Messages[0].Content)-1]
	if lastBlock.CacheControl == nil {
		t.Fatal("second-to-last user message last block CacheControl = nil, want cache control marker")
	}

	// Also verify final message has a marker
	finalMsg := wire.Messages[2]
	if len(finalMsg.Content) > 0 {
		finalBlock := finalMsg.Content[len(finalMsg.Content)-1]
		if finalBlock.CacheControl == nil {
			t.Fatal("final message last block CacheControl = nil, want cache control marker")
		}
	}
}

func TestAssignCacheBreakpoints_MarksStaticPrefixAndLastTwoUserMessages(t *testing.T) {
	wire := anthropicRequest{
		Tools: []anthropicTool{
			{Name: "lookup"},
		},
		Messages: []anthropicMessage{
			{
				Role: "user",
				Content: []anthropicContentBlock{
					{Type: "text", Text: "first"},
				},
			},
			{
				Role: "assistant",
				Content: []anthropicContentBlock{
					{Type: "text", Text: "reply"},
				},
			},
			{
				Role: "user",
				Content: []anthropicContentBlock{
					{Type: "text", Text: "second"},
				},
			},
		},
	}

	assignCacheBreakpoints(&wire)

	if wire.Tools[0].CacheControl == nil {
		t.Fatal("tool CacheControl = nil, want cache control marker")
	}
	if got, want := wire.Tools[0].CacheControl.Type, "ephemeral"; got != want {
		t.Fatalf("tool cache control type = %q, want %q", got, want)
	}
	if wire.Messages[0].Content[0].CacheControl == nil {
		t.Fatal("first user message CacheControl = nil, want cache control marker")
	}
	if got, want := wire.Messages[0].Content[0].CacheControl.Type, "ephemeral"; got != want {
		t.Fatalf("first user cache control type = %q, want %q", got, want)
	}
	if wire.Messages[1].Content[0].CacheControl != nil {
		t.Fatal("assistant message CacheControl = non-nil, want nil")
	}
	if wire.Messages[2].Content[0].CacheControl == nil {
		t.Fatal("final user message CacheControl = nil, want cache control marker")
	}
	if got, want := wire.Messages[2].Content[0].CacheControl.Type, "ephemeral"; got != want {
		t.Fatalf("final user cache control type = %q, want %q", got, want)
	}
}

func TestAnthropicCacheControl_EmptyRequest(t *testing.T) {
	request := ChatRequest{
		Model:    "claude-3-5-sonnet",
		Messages: []Message{},
	}

	wire := anthropicRequestWire(request, "default-model", false)

	if len(wire.System) != 0 {
		t.Fatalf("system blocks = %d, want 0", len(wire.System))
	}
	if len(wire.Tools) != 0 {
		t.Fatalf("tools = %d, want 0", len(wire.Tools))
	}
	if len(wire.Messages) != 0 {
		t.Fatalf("messages = %d, want 0", len(wire.Messages))
	}
}

func TestAnthropicCacheControl_MaxBreakpointsNotExceeded(t *testing.T) {
	request := ChatRequest{
		Model: "claude-3-5-sonnet",
		Messages: []Message{
			{
				Role:    MessageRoleSystem,
				Content: "System prompt",
			},
			{
				Role:    MessageRoleUser,
				Content: "User 1",
			},
			{
				Role:    MessageRoleAssistant,
				Content: "Assistant 1",
			},
			{
				Role:    MessageRoleUser,
				Content: "User 2",
			},
			{
				Role:    MessageRoleAssistant,
				Content: "Assistant 2",
			},
			{
				Role:    MessageRoleUser,
				Content: "User 3",
			},
		},
	}

	wire := anthropicRequestWire(request, "default-model", false)

	countBreakpoints := 0
	if len(wire.System) > 0 && wire.System[len(wire.System)-1].CacheControl != nil {
		countBreakpoints++
	}
	for _, msg := range wire.Messages {
		if len(msg.Content) > 0 && msg.Content[len(msg.Content)-1].CacheControl != nil {
			countBreakpoints++
		}
	}

	if countBreakpoints > 4 {
		t.Fatalf("breakpoint count = %d, want <= 4", countBreakpoints)
	}
}
