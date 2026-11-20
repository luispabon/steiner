package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func floatPtr(v float64) *float64 { return &v }

func TestOpenAIUsageToUsageStatsPresence(t *testing.T) {
	tests := []struct {
		name          string
		usage         string
		wantCache     bool
		wantRead      int
		wantCreation  int
		wantReasoning int
		wantCost      *float64
	}{
		{
			name:  "no cache fields is unreported",
			usage: `{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}`,
		},
		{
			name:      "prompt_tokens_details cached zero is reported",
			usage:     `{"prompt_tokens":10,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":0}}`,
			wantCache: true,
		},
		{
			name:      "cached tokens nonzero",
			usage:     `{"prompt_tokens":10,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":6}}`,
			wantCache: true,
			wantRead:  6,
		},
		{
			name:      "deepseek prompt_cache_hit_tokens zero is reported",
			usage:     `{"prompt_tokens":10,"completion_tokens":4,"prompt_cache_hit_tokens":0}`,
			wantCache: true,
		},
		{
			name:      "deepseek prompt_cache_hit_tokens nonzero",
			usage:     `{"prompt_tokens":10,"completion_tokens":4,"prompt_cache_hit_tokens":5}`,
			wantCache: true,
			wantRead:  5,
		},
		{
			name:      "cache write tokens zero is reported",
			usage:     `{"prompt_tokens":10,"completion_tokens":4,"prompt_tokens_details":{"cache_write_tokens":0}}`,
			wantCache: true,
		},
		{
			name:         "cache write tokens nonzero",
			usage:        `{"prompt_tokens":10,"completion_tokens":4,"prompt_tokens_details":{"cache_write_tokens":3}}`,
			wantCache:    true,
			wantCreation: 3,
		},
		{
			name:          "reasoning tokens present",
			usage:         `{"prompt_tokens":10,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":4}}`,
			wantReasoning: 4,
		},
		{
			name:          "negative reasoning tokens clamp to zero",
			usage:         `{"prompt_tokens":10,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":-2}}`,
			wantReasoning: 0,
		},
		{
			name:     "numeric cost is reported",
			usage:    `{"prompt_tokens":1,"completion_tokens":1,"cost":0.00042}`,
			wantCost: floatPtr(0.00042),
		},
		{
			name:     "zero cost is reported",
			usage:    `{"prompt_tokens":1,"completion_tokens":1,"cost":0}`,
			wantCost: floatPtr(0),
		},
		{
			name:  "object cost is ignored",
			usage: `{"prompt_tokens":1,"completion_tokens":1,"cost":{"total":0.5}}`,
		},
		{
			name:  "string cost is ignored",
			usage: `{"prompt_tokens":1,"completion_tokens":1,"cost":"0.5"}`,
		},
		{
			name:  "null cost is ignored",
			usage: `{"prompt_tokens":1,"completion_tokens":1,"cost":null}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := `{"choices":[{"message":{"role":"assistant"},"finish_reason":"stop"}],"usage":` + tt.usage + `}`
			var resp openAIResponse
			if err := json.Unmarshal([]byte(payload), &resp); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			got := resp.Usage
			if got == nil {
				t.Fatal("Usage = nil, want non-nil")
			}
			if got.CacheReported != tt.wantCache {
				t.Errorf("CacheReported = %v, want %v", got.CacheReported, tt.wantCache)
			}
			if got.CacheReadInputTokens != tt.wantRead {
				t.Errorf("CacheReadInputTokens = %d, want %d", got.CacheReadInputTokens, tt.wantRead)
			}
			if got.CacheCreationInputTokens != tt.wantCreation {
				t.Errorf("CacheCreationInputTokens = %d, want %d", got.CacheCreationInputTokens, tt.wantCreation)
			}
			if got.ReasoningTokens != tt.wantReasoning {
				t.Errorf("ReasoningTokens = %d, want %d", got.ReasoningTokens, tt.wantReasoning)
			}
			if !costPtrEqual(got.CostUSD, tt.wantCost) {
				t.Errorf("CostUSD = %v, want %v", got.CostUSD, tt.wantCost)
			}
		})
	}
}

func TestOpenAIStreamFinalChunkUsagePresence(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","content":"hi"}}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":6,"total_tokens":16,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":3},"cost":0.00042}}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	var chunks []ChatChunk
	if err := decodeChatStreamWithHandler(context.Background(), strings.NewReader(stream), func(chunk ChatChunk) error {
		chunks = append(chunks, chunk)
		return nil
	}); err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	final := chunks[len(chunks)-1]
	if !final.Done {
		t.Fatal("final chunk Done = false, want true")
	}
	if final.Usage == nil {
		t.Fatal("final usage = nil, want usage stats")
	}
	if !final.Usage.CacheReported {
		t.Error("CacheReported = false, want true for a present zero cached_tokens")
	}
	if got, want := final.Usage.ReasoningTokens, 3; got != want {
		t.Errorf("ReasoningTokens = %d, want %d", got, want)
	}
	if !costPtrEqual(final.Usage.CostUSD, floatPtr(0.00042)) {
		t.Errorf("CostUSD = %v, want 0.00042", final.Usage.CostUSD)
	}
}

func TestResponsesUsageToUsageStatsPresence(t *testing.T) {
	tests := []struct {
		name          string
		usage         string
		wantCache     bool
		wantRead      int
		wantCreation  int
		wantReasoning int
		wantCost      *float64
	}{
		{
			name:  "no cache fields is unreported",
			usage: `{"input_tokens":10,"output_tokens":4,"total_tokens":14}`,
		},
		{
			name:      "cached zero is reported",
			usage:     `{"input_tokens":10,"output_tokens":4,"input_tokens_details":{"cached_tokens":0}}`,
			wantCache: true,
		},
		{
			name:      "cached nonzero",
			usage:     `{"input_tokens":10,"output_tokens":4,"input_tokens_details":{"cached_tokens":8}}`,
			wantCache: true,
			wantRead:  8,
		},
		{
			name:         "cache write nonzero",
			usage:        `{"input_tokens":10,"output_tokens":4,"input_tokens_details":{"cache_write_tokens":2}}`,
			wantCache:    true,
			wantCreation: 2,
		},
		{
			name:          "reasoning tokens present",
			usage:         `{"input_tokens":10,"output_tokens":9,"output_tokens_details":{"reasoning_tokens":5}}`,
			wantReasoning: 5,
		},
		{
			name:     "numeric cost is reported",
			usage:    `{"input_tokens":1,"output_tokens":1,"cost":0.25}`,
			wantCost: floatPtr(0.25),
		},
		{
			name:  "object cost is ignored",
			usage: `{"input_tokens":1,"output_tokens":1,"cost":{"total":0.5}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var usage responsesUsage
			if err := json.Unmarshal([]byte(tt.usage), &usage); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			got := usage.toUsageStats()
			if got.CacheReported != tt.wantCache {
				t.Errorf("CacheReported = %v, want %v", got.CacheReported, tt.wantCache)
			}
			if got.CacheReadInputTokens != tt.wantRead {
				t.Errorf("CacheReadInputTokens = %d, want %d", got.CacheReadInputTokens, tt.wantRead)
			}
			if got.CacheCreationInputTokens != tt.wantCreation {
				t.Errorf("CacheCreationInputTokens = %d, want %d", got.CacheCreationInputTokens, tt.wantCreation)
			}
			if got.ReasoningTokens != tt.wantReasoning {
				t.Errorf("ReasoningTokens = %d, want %d", got.ReasoningTokens, tt.wantReasoning)
			}
			if !costPtrEqual(got.CostUSD, tt.wantCost) {
				t.Errorf("CostUSD = %v, want %v", got.CostUSD, tt.wantCost)
			}
		})
	}
}

func TestResponsesStreamFinalChunkUsagePresence(t *testing.T) {
	body := strings.NewReader(
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":6,\"total_tokens\":16,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens_details\":{\"reasoning_tokens\":4},\"cost\":0.5}}}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectResponsesStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeResponsesStreamWithHandler() error = %v", err)
	}
	final := chunks[len(chunks)-1]
	if !final.Done {
		t.Fatal("final chunk Done = false, want true")
	}
	if final.Usage == nil {
		t.Fatal("final usage = nil, want usage stats")
	}
	if !final.Usage.CacheReported {
		t.Error("CacheReported = false, want true for a present zero cached_tokens")
	}
	if got, want := final.Usage.ReasoningTokens, 4; got != want {
		t.Errorf("ReasoningTokens = %d, want %d", got, want)
	}
	if !costPtrEqual(final.Usage.CostUSD, floatPtr(0.5)) {
		t.Errorf("CostUSD = %v, want 0.5", final.Usage.CostUSD)
	}
}

func TestAnthropicResponseUsagePresence(t *testing.T) {
	tests := []struct {
		name          string
		usage         string
		wantPrompt    int
		wantCache     bool
		wantRead      int
		wantCreation  int
		wantReasoning int
	}{
		{
			name:       "no cache fields is unreported",
			usage:      `{"input_tokens":11,"output_tokens":7}`,
			wantPrompt: 11,
		},
		{
			name:       "cache fields at zero are reported",
			usage:      `{"input_tokens":11,"output_tokens":7,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}`,
			wantPrompt: 11,
			wantCache:  true,
		},
		{
			name:       "cache read nonzero",
			usage:      `{"input_tokens":11,"output_tokens":7,"cache_read_input_tokens":4}`,
			wantPrompt: 15,
			wantCache:  true,
			wantRead:   4,
		},
		{
			name:         "cache creation nonzero",
			usage:        `{"input_tokens":11,"output_tokens":7,"cache_creation_input_tokens":3}`,
			wantPrompt:   14,
			wantCache:    true,
			wantCreation: 3,
		},
		{
			name:          "thinking tokens present",
			usage:         `{"input_tokens":11,"output_tokens":7,"output_tokens_details":{"thinking_tokens":4}}`,
			wantPrompt:    11,
			wantReasoning: 4,
		},
		{
			name:       "thinking tokens absent",
			usage:      `{"input_tokens":11,"output_tokens":7,"output_tokens_details":{}}`,
			wantPrompt: 11,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := `{"role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":` + tt.usage + `}`
			var resp anthropicResponse
			if err := json.Unmarshal([]byte(payload), &resp); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			out, err := normalizeAnthropicChatResponse(&resp)
			if err != nil {
				t.Fatalf("normalizeAnthropicChatResponse() error = %v", err)
			}
			got := out.Usage
			if got == nil {
				t.Fatal("Usage = nil, want non-nil")
			}
			if got.PromptTokens != tt.wantPrompt {
				t.Errorf("PromptTokens = %d, want %d", got.PromptTokens, tt.wantPrompt)
			}
			if got.CacheReported != tt.wantCache {
				t.Errorf("CacheReported = %v, want %v", got.CacheReported, tt.wantCache)
			}
			if got.CacheReadInputTokens != tt.wantRead {
				t.Errorf("CacheReadInputTokens = %d, want %d", got.CacheReadInputTokens, tt.wantRead)
			}
			if got.CacheCreationInputTokens != tt.wantCreation {
				t.Errorf("CacheCreationInputTokens = %d, want %d", got.CacheCreationInputTokens, tt.wantCreation)
			}
			if got.ReasoningTokens != tt.wantReasoning {
				t.Errorf("ReasoningTokens = %d, want %d", got.ReasoningTokens, tt.wantReasoning)
			}
			if got.CostUSD != nil {
				t.Errorf("CostUSD = %v, want nil", got.CostUSD)
			}
		})
	}
}

func TestAnthropicStreamUsageMergePresence(t *testing.T) {
	stream := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"role":"assistant","usage":{"input_tokens":9,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4,"output_tokens_details":{"thinking_tokens":2}}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	var chunks []ChatChunk
	if err := decodeAnthropicStreamWithHandler(context.Background(), strings.NewReader(stream), func(chunk ChatChunk) error {
		chunks = append(chunks, chunk)
		return nil
	}); err != nil {
		t.Fatalf("decodeAnthropicStreamWithHandler() error = %v", err)
	}
	final := chunks[len(chunks)-1]
	if !final.Done || final.Usage == nil {
		t.Fatalf("final chunk = %+v, want Done with usage", final)
	}
	if !final.Usage.CacheReported {
		t.Error("CacheReported = false, want true after message_start reported cache keys")
	}
	if got, want := final.Usage.CacheReadInputTokens, 3; got != want {
		t.Errorf("CacheReadInputTokens = %d, want %d", got, want)
	}
	if got, want := final.Usage.CacheCreationInputTokens, 2; got != want {
		t.Errorf("CacheCreationInputTokens = %d, want %d", got, want)
	}
	if got, want := final.Usage.ReasoningTokens, 2; got != want {
		t.Errorf("ReasoningTokens = %d, want %d", got, want)
	}
	if got, want := final.Usage.PromptTokens, 14; got != want {
		t.Errorf("PromptTokens = %d, want %d", got, want)
	}
	if got, want := final.Usage.CompletionTokens, 4; got != want {
		t.Errorf("CompletionTokens = %d, want %d", got, want)
	}
}

func TestClaudeSubUsageSnapshotMergePresence(t *testing.T) {
	tests := []struct {
		name      string
		snapshots []string
		want      UsageStats
	}{
		{
			name: "cache absent is unreported",
			snapshots: []string{
				`{"input_tokens":3,"output_tokens":2}`,
			},
			want: UsageStats{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
		},
		{
			name: "cache presence is sticky across snapshots",
			snapshots: []string{
				`{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}`,
				`{"output_tokens":4}`,
			},
			want: UsageStats{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14, CacheReported: true},
		},
		{
			name: "nonzero cache read retained when a later snapshot omits it",
			snapshots: []string{
				`{"input_tokens":3,"cache_read_input_tokens":7,"output_tokens":1}`,
				`{"output_tokens":4}`,
			},
			want: UsageStats{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14, CacheReadInputTokens: 7, CacheReported: true},
		},
		{
			name: "repeated cumulative snapshot does not double count",
			snapshots: []string{
				`{"input_tokens":3,"cache_read_input_tokens":7,"output_tokens":4}`,
				`{"input_tokens":3,"cache_read_input_tokens":7,"output_tokens":4}`,
			},
			want: UsageStats{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14, CacheReadInputTokens: 7, CacheReported: true},
		},
		{
			name: "thinking tokens reported",
			snapshots: []string{
				`{"input_tokens":3,"output_tokens":6,"output_tokens_details":{"thinking_tokens":2}}`,
			},
			want: UsageStats{PromptTokens: 3, CompletionTokens: 6, ReasoningTokens: 2, TotalTokens: 9},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newClaudeSubDecoder(claudeSubDecodeHooks{})
			var got *UsageStats
			for _, raw := range tt.snapshots {
				var snapshot claudeSubUsage
				if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
					t.Fatalf("Unmarshal(%s) error = %v", raw, err)
				}
				events := d.usageEvents(&snapshot)
				if len(events) != 1 {
					t.Fatalf("usageEvents() = %d events, want 1", len(events))
				}
				got = events[0].Usage
			}
			if got == nil {
				t.Fatal("usage = nil, want usage stats")
			}
			if *got != tt.want {
				t.Errorf("usage = %+v, want %+v", *got, tt.want)
			}
			if got.CostUSD != nil {
				t.Errorf("CostUSD = %v, want nil: claude_subscription must not forward its cumulative cost", got.CostUSD)
			}
		})
	}
}

func TestClaudeSubResultUsageNeverCarriesCost(t *testing.T) {
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	var out []claudeSubDecoded
	for _, raw := range []string{
		`{"type":"stream_event","event":{"type":"message_start","message":{"role":"assistant","usage":{"input_tokens":5,"cache_read_input_tokens":10}}}}`,
		`{"type":"stream_event","event":{"type":"message_stop"}}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"ok","total_cost_usd":0.25,"usage":{"input_tokens":8,"output_tokens":5}}`,
	} {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(raw), &head); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", raw, err)
		}
		decoded, err := d.decode(claudeSubEvent{Type: head.Type, Raw: json.RawMessage(raw)})
		if err != nil {
			t.Fatalf("decode(%s) error = %v", head.Type, err)
		}
		out = append(out, decoded...)
	}
	results := claudeSubDecodedOfKind(out, claudeSubDecodeResult)
	if len(results) != 1 || results[0].Result == nil || results[0].Result.Usage == nil {
		t.Fatalf("results = %+v, want one result with usage", results)
	}
	if got, want := results[0].Result.TotalCostUSD, 0.25; got != want {
		t.Errorf("TotalCostUSD = %v, want %v", got, want)
	}
	if results[0].Result.Usage.CostUSD != nil {
		t.Errorf("Usage.CostUSD = %v, want nil", results[0].Result.Usage.CostUSD)
	}
}

func costPtrEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
