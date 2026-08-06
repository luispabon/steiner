package advisor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestFlattenToolMessages(t *testing.T) {
	tests := []struct {
		name  string
		input []provider.Message
		check func(t *testing.T, got []provider.Message)
	}{
		{
			name:  "empty input",
			input: nil,
			check: func(t *testing.T, got []provider.Message) {
				if len(got) != 0 {
					t.Fatalf("len = %d, want 0", len(got))
				}
			},
		},
		{
			name: "plain messages pass through",
			input: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "hello"},
				{Role: provider.MessageRoleAssistant, Content: "hi"},
			},
			check: func(t *testing.T, got []provider.Message) {
				if len(got) != 2 {
					t.Fatalf("len = %d, want 2", len(got))
				}
				if got[0].Content != "hello" || got[1].Content != "hi" {
					t.Fatalf("unexpected content: %#v", got)
				}
				if len(got[0].ToolCalls) != 0 || len(got[1].ToolCalls) != 0 {
					t.Fatalf("unexpected tool calls: %#v", got)
				}
			},
		},
		{
			name: "assistant with text and tool calls",
			input: []provider.Message{
				{
					Role:    provider.MessageRoleAssistant,
					Content: "thinking",
					ToolCalls: []provider.ToolCall{{
						ID:        "id-1",
						Name:      "read",
						Arguments: map[string]any{"path": "main.go"},
					}},
				},
			},
			check: func(t *testing.T, got []provider.Message) {
				if len(got) != 1 {
					t.Fatalf("len = %d, want 1", len(got))
				}
				m := got[0]
				if m.Role != provider.MessageRoleAssistant {
					t.Fatalf("role = %q, want assistant", m.Role)
				}
				if !strings.Contains(m.Content, "thinking") {
					t.Fatalf("content missing original text: %q", m.Content)
				}
				if !strings.Contains(m.Content, "[tool_call: read") {
					t.Fatalf("content missing tool_call line: %q", m.Content)
				}
				if !strings.Contains(m.Content, `"main.go"`) {
					t.Fatalf("content missing argument: %q", m.Content)
				}
				if len(m.ToolCalls) != 0 {
					t.Fatalf("ToolCalls not cleared: %#v", m.ToolCalls)
				}
			},
		},
		{
			name: "assistant with only tool calls and no text",
			input: []provider.Message{
				{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{{
						ID:        "id-2",
						Name:      "bash",
						Arguments: map[string]any{"command": "ls"},
					}},
				},
			},
			check: func(t *testing.T, got []provider.Message) {
				if len(got) != 1 {
					t.Fatalf("len = %d, want 1", len(got))
				}
				m := got[0]
				if m.Role != provider.MessageRoleAssistant {
					t.Fatalf("role = %q, want assistant", m.Role)
				}
				if !strings.HasPrefix(m.Content, "[tool_call: bash") {
					t.Fatalf("content should start with tool_call line, got: %q", m.Content)
				}
				if len(m.ToolCalls) != 0 {
					t.Fatalf("ToolCalls not cleared: %#v", m.ToolCalls)
				}
			},
		},
		{
			name: "tool result becomes user message",
			input: []provider.Message{
				{
					Role:       provider.MessageRoleTool,
					Name:       "read",
					ToolCallID: "id-1",
					Content:    "package main",
				},
			},
			check: func(t *testing.T, got []provider.Message) {
				if len(got) != 1 {
					t.Fatalf("len = %d, want 1", len(got))
				}
				m := got[0]
				if m.Role != provider.MessageRoleUser {
					t.Fatalf("role = %q, want user", m.Role)
				}
				if !strings.HasPrefix(m.Content, "[tool_result: read]") {
					t.Fatalf("content missing prefix: %q", m.Content)
				}
				if !strings.Contains(m.Content, "package main") {
					t.Fatalf("content missing original: %q", m.Content)
				}
				if m.ToolCallID != "" {
					t.Fatalf("ToolCallID not cleared: %q", m.ToolCallID)
				}
				if m.Name != "" {
					t.Fatalf("Name not cleared: %q", m.Name)
				}
			},
		},
		{
			name: "empty tool result uses placeholder",
			input: []provider.Message{
				{
					Role:       provider.MessageRoleTool,
					Name:       "bash",
					ToolCallID: "id-3",
					Content:    "",
				},
			},
			check: func(t *testing.T, got []provider.Message) {
				if len(got) != 1 {
					t.Fatalf("len = %d, want 1", len(got))
				}
				if !strings.Contains(got[0].Content, "(empty)") {
					t.Fatalf("content missing (empty) placeholder: %q", got[0].Content)
				}
			},
		},
		{
			name: "assistant message with ReasoningContent and ProviderMetadata clears both",
			input: []provider.Message{
				{
					Role:             provider.MessageRoleAssistant,
					Content:          "my response",
					ReasoningContent: "my internal reasoning",
					ProviderMetadata: &provider.MessageProviderMetadata{
						Anthropic: &provider.AnthropicMessageMetadata{
							ThinkingSignature: "sig123",
						},
					},
				},
			},
			check: func(t *testing.T, got []provider.Message) {
				if len(got) != 1 {
					t.Fatalf("len = %d, want 1", len(got))
				}
				m := got[0]
				if m.Role != provider.MessageRoleAssistant {
					t.Fatalf("role = %q, want assistant", m.Role)
				}
				if m.Content != "my response" {
					t.Fatalf("content = %q, want 'my response'", m.Content)
				}
				if m.ReasoningContent != "" {
					t.Fatalf("ReasoningContent not cleared: %q", m.ReasoningContent)
				}
				if m.ProviderMetadata != nil {
					t.Fatalf("ProviderMetadata not cleared: %#v", m.ProviderMetadata)
				}
			},
		},
		{
			name: "mixed conversation",
			input: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "fix it"},
				{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{{
						ID: "id-1", Name: "read", Arguments: map[string]any{"path": "x.go"},
					}},
				},
				{
					Role: provider.MessageRoleTool, Name: "read", ToolCallID: "id-1",
					Content: "contents",
				},
				{Role: provider.MessageRoleAssistant, Content: "done"},
			},
			check: func(t *testing.T, got []provider.Message) {
				if len(got) != 4 {
					t.Fatalf("len = %d, want 4", len(got))
				}
				// user pass-through
				if got[0].Role != provider.MessageRoleUser || got[0].Content != "fix it" {
					t.Fatalf("msg[0] unexpected: %#v", got[0])
				}
				// flattened assistant
				if got[1].Role != provider.MessageRoleAssistant {
					t.Fatalf("msg[1] role = %q, want assistant", got[1].Role)
				}
				if len(got[1].ToolCalls) != 0 {
					t.Fatalf("msg[1] ToolCalls not cleared")
				}
				if !strings.Contains(got[1].Content, "[tool_call: read") {
					t.Fatalf("msg[1] missing tool_call line: %q", got[1].Content)
				}
				// flattened tool result
				if got[2].Role != provider.MessageRoleUser {
					t.Fatalf("msg[2] role = %q, want user", got[2].Role)
				}
				if !strings.HasPrefix(got[2].Content, "[tool_result: read]") {
					t.Fatalf("msg[2] missing tool_result prefix: %q", got[2].Content)
				}
				if got[2].ToolCallID != "" || got[2].Name != "" {
					t.Fatalf("msg[2] fields not cleared: ToolCallID=%q Name=%q", got[2].ToolCallID, got[2].Name)
				}
				// plain assistant pass-through
				if got[3].Role != provider.MessageRoleAssistant || got[3].Content != "done" {
					t.Fatalf("msg[3] unexpected: %#v", got[3])
				}
			},
		},
		{
			name: "advisor tool result uses prior-note framing",
			input: []provider.Message{
				{
					Role:       provider.MessageRoleTool,
					Name:       ToolName,
					ToolCallID: "id-1",
					Content:    "check the edge cases",
				},
			},
			check: func(t *testing.T, got []provider.Message) {
				if len(got) != 1 {
					t.Fatalf("len = %d, want 1", len(got))
				}
				m := got[0]
				if m.Role != provider.MessageRoleUser {
					t.Fatalf("role = %q, want user", m.Role)
				}
				if !strings.Contains(m.Content, "Your earlier note") {
					t.Fatalf("content missing prior-note framing: %q", m.Content)
				}
				if !strings.Contains(m.Content, "check the edge cases") {
					t.Fatalf("content missing original advisory text: %q", m.Content)
				}
				if strings.Contains(m.Content, "[tool_result: advisor]") {
					t.Fatalf("content should not use generic [tool_result] prefix: %q", m.Content)
				}
				if m.ToolCallID != "" || m.Name != "" {
					t.Fatalf("fields not cleared: ToolCallID=%q Name=%q", m.ToolCallID, m.Name)
				}
			},
		},
		{
			name: "non-advisor tool results use standard tool_result framing",
			input: []provider.Message{
				{
					Role:       provider.MessageRoleTool,
					Name:       "bash",
					ToolCallID: "id-1",
					Content:    "command output",
				},
			},
			check: func(t *testing.T, got []provider.Message) {
				if len(got) != 1 {
					t.Fatalf("len = %d, want 1", len(got))
				}
				m := got[0]
				if m.Role != provider.MessageRoleUser {
					t.Fatalf("role = %q, want user", m.Role)
				}
				if !strings.HasPrefix(m.Content, "[tool_result: bash]") {
					t.Fatalf("content missing [tool_result: bash] prefix: %q", m.Content)
				}
				if !strings.Contains(m.Content, "command output") {
					t.Fatalf("content missing original bash output: %q", m.Content)
				}
				if m.ToolCallID != "" || m.Name != "" {
					t.Fatalf("fields not cleared: ToolCallID=%q Name=%q", m.ToolCallID, m.Name)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flattenToolMessages(tt.input)
			tt.check(t, got)
		})
	}
}

func TestFlattenToolMessagesPreservesInput(t *testing.T) {
	original := []provider.Message{
		{
			Role:             provider.MessageRoleAssistant,
			Content:          "response",
			ReasoningContent: "reasoning",
			ProviderMetadata: &provider.MessageProviderMetadata{
				Anthropic: &provider.AnthropicMessageMetadata{
					ThinkingSignature: "sig123",
				},
			},
		},
		{
			Role:    provider.MessageRoleUser,
			Content: "user input",
		},
	}

	snapshot := make([]provider.Message, len(original))
	copy(snapshot, original)

	_ = flattenToolMessages(original)

	if len(original) != len(snapshot) {
		t.Fatalf("input length changed: %d vs %d", len(original), len(snapshot))
	}
	for i, msg := range original {
		if msg.Role != snapshot[i].Role {
			t.Fatalf("msg[%d] Role changed", i)
		}
		if msg.Content != snapshot[i].Content {
			t.Fatalf("msg[%d] Content changed", i)
		}
		if msg.ReasoningContent != snapshot[i].ReasoningContent {
			t.Fatalf("msg[%d] ReasoningContent changed", i)
		}
		if (msg.ProviderMetadata == nil) != (snapshot[i].ProviderMetadata == nil) {
			t.Fatalf("msg[%d] ProviderMetadata nil state changed", i)
		}
		if msg.ProviderMetadata != nil && snapshot[i].ProviderMetadata != nil {
			if msg.ProviderMetadata.Anthropic.ThinkingSignature != snapshot[i].ProviderMetadata.Anthropic.ThinkingSignature {
				t.Fatalf("msg[%d] ThinkingSignature changed", i)
			}
		}
	}
}

func TestFlattenToolMessagesCapsLargeToolArgStrings(t *testing.T) {
	largeContent := strings.Repeat("a", maxToolArgStringLen+500)
	input := []provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{{
				ID:   "id-1",
				Name: "mutate",
				Arguments: map[string]any{
					"operations": []any{
						map[string]any{
							"type":    "write",
							"path":    "main.go",
							"content": largeContent,
						},
					},
				},
			}},
		},
	}

	got := flattenToolMessages(input)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	content := got[0].Content
	marker := fmt.Sprintf("…[elided, %d bytes total]", len(largeContent))
	if !strings.Contains(content, marker) {
		t.Fatalf("content missing elision marker %q: %q", marker, content)
	}
	if strings.Contains(content, largeContent) {
		t.Fatalf("content still contains full untruncated string: %q", content)
	}
}

func TestFlattenToolMessagesCapsMultipleOperationsIndependently(t *testing.T) {
	large1 := strings.Repeat("x", maxToolArgStringLen+10)
	large2 := strings.Repeat("y", maxToolArgStringLen+20)
	input := []provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{{
				ID:   "id-1",
				Name: "mutate",
				Arguments: map[string]any{
					"operations": []any{
						map[string]any{"type": "write", "path": "a.go", "content": large1},
						map[string]any{"type": "write", "path": "b.go", "content": large2},
					},
				},
			}},
		},
	}

	got := flattenToolMessages(input)
	content := got[0].Content
	marker1 := fmt.Sprintf("…[elided, %d bytes total]", len(large1))
	marker2 := fmt.Sprintf("…[elided, %d bytes total]", len(large2))
	if !strings.Contains(content, marker1) {
		t.Fatalf("content missing marker for first operation %q: %q", marker1, content)
	}
	if !strings.Contains(content, marker2) {
		t.Fatalf("content missing marker for second operation %q: %q", marker2, content)
	}
	if strings.Contains(content, large1) || strings.Contains(content, large2) {
		t.Fatalf("content still contains full untruncated strings: %q", content)
	}
}

func TestFlattenToolMessagesSmallArgsUnchanged(t *testing.T) {
	input := []provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{{
				ID:        "id-1",
				Name:      "read",
				Arguments: map[string]any{"path": "main.go"},
			}},
		},
	}

	got := flattenToolMessages(input)
	if !strings.Contains(got[0].Content, `"path":"main.go"`) {
		t.Fatalf("small argument was altered: %q", got[0].Content)
	}
}

func TestFlattenToolMessagesCapDeterministic(t *testing.T) {
	input := []provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{{
				ID:   "id-1",
				Name: "mutate",
				Arguments: map[string]any{
					"content": strings.Repeat("z", maxToolArgStringLen+50),
				},
			}},
		},
	}

	got1 := flattenToolMessages(input)
	got2 := flattenToolMessages(input)
	if got1[0].Content != got2[0].Content {
		t.Fatalf("rendering not deterministic:\n%q\nvs\n%q", got1[0].Content, got2[0].Content)
	}
}

func TestFlattenToolMessagesDoesNotMutateOriginalArguments(t *testing.T) {
	large := strings.Repeat("w", maxToolArgStringLen+50)
	args := map[string]any{"content": large}
	input := []provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{{
				ID:        "id-1",
				Name:      "mutate",
				Arguments: args,
			}},
		},
	}

	_ = flattenToolMessages(input)

	if args["content"] != large {
		t.Fatalf("original Arguments map was mutated: %#v", args)
	}
}

func TestBuildMessagesEmptyFilesAndQuestionMatchesLegacyOutput(t *testing.T) {
	snapshot := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "fix it"},
	}

	got := buildMessages(snapshot, "", nil)
	want := []provider.Message{
		{Role: provider.MessageRoleSystem, Content: advisorSystemPrompt},
		{Role: provider.MessageRoleUser, Content: "fix it"},
		{Role: provider.MessageRoleUser, Content: advisorUserPrompt},
	}
	if len(got) != len(want) {
		t.Fatalf("len(messages) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Fatalf("messages[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestBuildMessagesOrdersFilesAndQuestionAfterSnapshot(t *testing.T) {
	snapshot := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "fix it"},
		{Role: provider.MessageRoleAssistant, Content: "on it"},
	}
	files := []advisorFile{
		{DisplayPath: ".steiner/plans/x/overview.md", Content: "## Overview\ndo the thing"},
		{DisplayPath: ".steiner/plans/x/plan.yaml", Content: "steps: []"},
	}

	got := buildMessages(snapshot, "is this sound?", files)

	// system prompt, then snapshot (2 msgs), then the single trailing suffix message.
	wantLen := 1 + len(snapshot) + 1
	if len(got) != wantLen {
		t.Fatalf("len(messages) = %d, want %d", len(got), wantLen)
	}
	if got[0].Role != provider.MessageRoleSystem {
		t.Fatalf("messages[0].Role = %q, want system", got[0].Role)
	}
	if got[1].Content != "fix it" || got[2].Content != "on it" {
		t.Fatalf("snapshot messages not preserved in order: %#v", got[1:3])
	}

	last := got[len(got)-1]
	if last.Role != provider.MessageRoleUser {
		t.Fatalf("last message role = %q, want user", last.Role)
	}
	if !strings.Contains(last.Content, "overview.md") || !strings.Contains(last.Content, "do the thing") {
		t.Fatalf("last message missing first file content: %q", last.Content)
	}
	if !strings.Contains(last.Content, "plan.yaml") || !strings.Contains(last.Content, "steps: []") {
		t.Fatalf("last message missing second file content: %q", last.Content)
	}
	if !strings.Contains(last.Content, "is this sound?") {
		t.Fatalf("last message missing question: %q", last.Content)
	}
	if !strings.Contains(last.Content, advisorUserPrompt) {
		t.Fatalf("last message missing closing instruction: %q", last.Content)
	}
	// files must precede the question, which must precede the closing instruction.
	filesIdx := strings.Index(last.Content, "do the thing")
	questionIdx := strings.Index(last.Content, "is this sound?")
	closingIdx := strings.Index(last.Content, advisorUserPrompt)
	if filesIdx >= questionIdx || questionIdx >= closingIdx {
		t.Fatalf("suffix ordering wrong: filesIdx=%d questionIdx=%d closingIdx=%d", filesIdx, questionIdx, closingIdx)
	}
}

func TestBuildMessagesTruncatesOversizedFile(t *testing.T) {
	large := strings.Repeat("a", maxAdvisorFileBytes+500)
	files := []advisorFile{{DisplayPath: "big.txt", Content: large}}

	got := buildMessages(nil, "", files)
	last := got[len(got)-1]
	marker := elide(large, maxAdvisorFileBytes)
	if !strings.Contains(last.Content, "…[elided,") {
		t.Fatalf("content missing elision marker: %q", last.Content)
	}
	if strings.Contains(last.Content, large) {
		t.Fatal("content still contains full untruncated file")
	}
	if !strings.Contains(last.Content, marker) {
		t.Fatalf("content missing expected truncated prefix: %q", last.Content)
	}
}

func TestBuildMessagesTruncatesAggregateAcrossFiles(t *testing.T) {
	// Four files individually at the per-file cap but collectively over the
	// aggregate cap (4 * maxAdvisorFileBytes > maxAdvisorFilesTotalBytes).
	each := strings.Repeat("b", maxAdvisorFileBytes)
	files := []advisorFile{
		{DisplayPath: "one.txt", Content: each},
		{DisplayPath: "two.txt", Content: each},
		{DisplayPath: "three.txt", Content: each},
		{DisplayPath: "four.txt", Content: each},
	}

	got := buildMessages(nil, "", files)
	last := got[len(got)-1]
	if !strings.Contains(last.Content, "…[elided,") {
		t.Fatalf("content missing elision marker for aggregate cap: %q", last.Content)
	}
}

func TestBuildMessagesTruncatesQuestion(t *testing.T) {
	longQuestion := strings.Repeat("q", maxAdvisorQuestionBytes+200)

	got := buildMessages(nil, longQuestion, nil)
	last := got[len(got)-1]
	if strings.Contains(last.Content, longQuestion) {
		t.Fatal("content still contains full untruncated question")
	}
	marker := elide(longQuestion, maxAdvisorQuestionBytes)
	if !strings.Contains(last.Content, marker) {
		t.Fatalf("content missing truncated question prefix: %q", last.Content)
	}
}

func TestFlattenToolMessagesDeterminism(t *testing.T) {
	input := []provider.Message{
		{
			Role:             provider.MessageRoleAssistant,
			Content:          "response",
			ReasoningContent: "reasoning",
			ProviderMetadata: &provider.MessageProviderMetadata{
				Anthropic: &provider.AnthropicMessageMetadata{
					ThinkingSignature: "sig123",
				},
			},
		},
	}

	result1 := flattenToolMessages(input)
	result2 := flattenToolMessages(input)

	if len(result1) != len(result2) {
		t.Fatalf("results differ in length: %d vs %d", len(result1), len(result2))
	}

	for i := range result1 {
		if result1[i].Role != result2[i].Role {
			t.Fatalf("msg[%d] Role differs", i)
		}
		if result1[i].Content != result2[i].Content {
			t.Fatalf("msg[%d] Content differs", i)
		}
		if result1[i].ReasoningContent != result2[i].ReasoningContent {
			t.Fatalf("msg[%d] ReasoningContent differs", i)
		}
		if (result1[i].ProviderMetadata == nil) != (result2[i].ProviderMetadata == nil) {
			t.Fatalf("msg[%d] ProviderMetadata nil state differs", i)
		}
	}
}
