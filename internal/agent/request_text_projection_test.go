package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRequestTextNeverReachesProviders proves the stored request text stays
// local: provider messages project only Content, so the request text the
// compaction block reads is never part of a provider request.
func TestRequestTextNeverReachesProviders(t *testing.T) {
	t.Parallel()

	const hidden = "hidden user words only compaction reads"
	messages := []Message{
		{Role: MessageRoleUser, Content: "visible content", RequestText: hidden},
		{Role: MessageRoleSummary, Content: "summary", RequestText: hidden},
	}
	projected := ToProviderMessages(messages)
	if len(projected) != len(messages) {
		t.Fatalf("ToProviderMessages() returned %d messages, want %d", len(projected), len(messages))
	}
	for i, message := range projected {
		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatalf("marshal provider message %d: %v", i, err)
		}
		if strings.Contains(string(encoded), hidden) {
			t.Fatalf("provider message %d carries the request text: %s", i, encoded)
		}
	}
}
