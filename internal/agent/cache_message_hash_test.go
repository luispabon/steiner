package agent

import (
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestPerMessageHashes_EmptyMessagePreservesEmptyHash(t *testing.T) {
	if got := perMessageHashes([]provider.Message{{}})[0]; got != "" {
		t.Fatalf("perMessageHashes([]provider.Message{{}})[0] = %q, want empty", got)
	}
}

// TestPerMessageHashes_EmptyAfterNonEmptyPreservesEmptyHash pins the empty
// special case inside a single call: the buffer reused for the preceding
// message must be truncated for the empty one, or the empty message would
// hash the previous message's leftover bytes.
func TestPerMessageHashes_EmptyAfterNonEmptyPreservesEmptyHash(t *testing.T) {
	hashes := perMessageHashes([]provider.Message{
		{Role: provider.MessageRoleUser, Content: "first"},
		{},
		{Role: provider.MessageRoleUser, Content: "third"},
	})
	if hashes[1] != "" {
		t.Errorf("hashes[1] = %q, want empty for the message after a non-empty one", hashes[1])
	}
	if hashes[0] == "" || hashes[2] == "" {
		t.Errorf("non-empty neighbours hashed empty: %v", hashes)
	}
}

// TestPerMessageHashes_PinsLiteralDigests pins the reused-buffer hashing path
// to digests independent of provider.AppendMessageHashInput, so a change to
// either the appended bytes or the hashing step is caught.
func TestPerMessageHashes_PinsLiteralDigests(t *testing.T) {
	messages := []provider.Message{
		{},
		{Role: provider.MessageRoleAssistant, Content: "answer", ReasoningContent: "thinking"},
		{Role: provider.MessageRoleUser, Images: []provider.ImageBlock{
			{MediaType: "image/png", Data: "aaa"},
			{MediaType: "image/jpeg", Data: "bbb"},
		}},
		{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{{Name: "read", Arguments: map[string]any{"path": "one"}}}},
	}
	// Digests of "assistantanswerthinking", "userimage/pngaaaimage/jpegbbb" and
	// `assistantread{"path":"one"}` respectively.
	want := []string{"", "84e63999", "16696220", "9603d157"}

	got := perMessageHashes(messages)
	if len(got) != len(want) {
		t.Fatalf("len(perMessageHashes()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hashes[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
