package output

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestNewAPIRequestEvent_EmptyMessagePreservesHash(t *testing.T) {
	event := NewAPIRequestEvent("model", []provider.Message{{}}, nil, nil, nil, prompt.ModelTokenBudget{}, 0, 0)
	payload := event.Payload.(APIRequestEvent)
	if got, want := payload.MessageHashes[0], "e3b0c442"; got != want {
		t.Fatalf("MessageHashes[0] = %q, want %q", got, want)
	}
}

// TestRequestMessageHashes_MatchAppendMessageHashInput pins the incremental
// hashing path to the AppendMessageHashInput bytes every other fingerprint
// consumer uses.
func TestRequestMessageHashes_MatchAppendMessageHashInput(t *testing.T) {
	messages := []provider.Message{
		{},
		{Role: provider.MessageRoleSystem, Content: "preamble"},
		{
			Role:             provider.MessageRoleAssistant,
			Content:          "answer",
			ReasoningContent: "thinking",
			ToolCalls: []provider.ToolCall{
				{Name: "read", Arguments: map[string]any{"path": "one", "nested": map[string]any{"limit": 1}}, RawArguments: `{"raw":true}`},
				{Name: "write", Arguments: map[string]any{"invalid": func() {}}, RawArguments: "raw"},
			},
		},
		{Role: provider.MessageRoleUser, Images: []provider.ImageBlock{{MediaType: "image/png", Data: "aaa"}, {MediaType: "image/jpeg", Data: "bbb"}}},
		{Role: provider.MessageRoleTool, ToolCallID: "call_1", Content: "héllo wörld"},
	}

	hashes, promptBytes := requestMessageHashes(messages)

	wantBytes := 0
	if len(hashes) != len(messages) {
		t.Fatalf("len(hashes) = %d, want %d", len(hashes), len(messages))
	}
	for i, msg := range messages {
		wantBytes += len(msg.Role) + len(msg.Content)
		sum := sha256.Sum256([]byte(string(provider.AppendMessageHashInput(nil, msg))))
		if want := hex.EncodeToString(sum[:4]); hashes[i] != want {
			t.Errorf("hashes[%d] = %q, want %q", i, hashes[i], want)
		}
	}
	if promptBytes != wantBytes {
		t.Errorf("promptBytes = %d, want %d", promptBytes, wantBytes)
	}
}

// TestNewAPIRequestEvent_MessageHashesLiteralDigests pins the event hashes for
// an assistant message carrying reasoning content and tool-call arguments and
// for a plain user message. Swapping the content and reasoning-content append
// order in AppendMessageHashInput changes the first digest's input and fails
// this test.
func TestNewAPIRequestEvent_MessageHashesLiteralDigests(t *testing.T) {
	messages := []provider.Message{
		{
			Role:             provider.MessageRoleAssistant,
			Content:          "answer",
			ReasoningContent: "thinking",
			ToolCalls:        []provider.ToolCall{{Name: "read", Arguments: map[string]any{"path": "one"}}},
		},
		{Role: provider.MessageRoleUser, Content: "hello"},
	}
	event := NewAPIRequestEvent("model", messages, nil, nil, nil, prompt.ModelTokenBudget{}, 0, 0)
	payload := event.Payload.(APIRequestEvent)
	// Digests of `assistantanswerthinkingread{"path":"one"}` and "userhello".
	want := []string{"961708b1", "59cb3723"}
	if !slices.Equal(payload.MessageHashes, want) {
		t.Fatalf("MessageHashes = %v, want %v", payload.MessageHashes, want)
	}
}
