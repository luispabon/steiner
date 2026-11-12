package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStampMessageTimestamp(t *testing.T) {
	t.Parallel()
	historic := time.Date(2025, 3, 4, 5, 6, 7, 89, time.UTC)
	tests := []struct {
		name    string
		message Message
		want    time.Time
	}{
		{name: "zero stamped", message: Message{Role: MessageRoleUser, Content: "hi"}},
		{name: "non-zero preserved", message: Message{Role: MessageRoleUser, Content: "hi", Timestamp: historic}, want: historic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			before := time.Now().UTC()
			got := stampMessageTimestamp(tt.message)
			after := time.Now().UTC()
			if !tt.want.IsZero() {
				if !got.Timestamp.Equal(tt.want) {
					t.Fatalf("Timestamp = %v, want preserved %v", got.Timestamp, tt.want)
				}
				return
			}
			if got.Timestamp.IsZero() {
				t.Fatal("Timestamp is still zero")
			}
			if got.Timestamp.Before(before) || got.Timestamp.After(after) {
				t.Fatalf("Timestamp = %v, want within [%v, %v]", got.Timestamp, before, after)
			}
			if got.Timestamp.Location() != time.UTC {
				t.Fatalf("Timestamp location = %v, want UTC", got.Timestamp.Location())
			}
			if got.Content != tt.message.Content {
				t.Fatalf("Content = %q, want %q", got.Content, tt.message.Content)
			}
		})
	}
}

func TestLineageWithAppendedMessagesStampsTimestamps(t *testing.T) {
	t.Parallel()
	historic := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	lineage := ConversationLineage{}
	lineage = lineage.WithAppendedMessages([]Message{{Role: MessageRoleUser, Content: "keep me", Timestamp: historic}})
	lineage = lineage.WithAppendedMessages([]Message{{Role: MessageRoleAssistant, Content: "stamp me"}})

	messages := lineage.FullMessages()
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
	if !messages[0].Timestamp.Equal(historic) {
		t.Fatalf("first Timestamp = %v, want preserved %v", messages[0].Timestamp, historic)
	}
	if messages[1].Timestamp.IsZero() {
		t.Fatal("second Timestamp is zero, want stamped")
	}
	cloned := lineage.Clone().FullMessages()
	if !cloned[0].Timestamp.Equal(historic) || !cloned[1].Timestamp.Equal(messages[1].Timestamp) {
		t.Fatalf("cloned timestamps = %v/%v, want %v/%v", cloned[0].Timestamp, cloned[1].Timestamp, historic, messages[1].Timestamp)
	}
}

func TestConversationDriverAppendLockedStampsConversationAndLineage(t *testing.T) {
	t.Parallel()
	driver := NewConversationDriver(DriverOptions{}, nil, ConversationLineage{})
	driver.appendLocked(Message{Role: MessageRoleUser, Content: "hello"})

	if len(driver.conv) != 1 {
		t.Fatalf("conversation = %d messages, want 1", len(driver.conv))
	}
	lineageMessages := driver.lineage.FullMessages()
	if len(lineageMessages) != 1 {
		t.Fatalf("lineage = %d messages, want 1", len(lineageMessages))
	}
	if driver.conv[0].Timestamp.IsZero() {
		t.Fatal("conversation Timestamp is zero, want stamped")
	}
	if !driver.conv[0].Timestamp.Equal(lineageMessages[0].Timestamp) {
		t.Fatalf("conversation Timestamp = %v, lineage Timestamp = %v; want equal", driver.conv[0].Timestamp, lineageMessages[0].Timestamp)
	}
}

func TestMessageTimestampJSON(t *testing.T) {
	t.Parallel()
	historic := time.Date(2025, 3, 4, 5, 6, 7, 89, time.UTC)

	t.Run("round trip preserves timestamp", func(t *testing.T) {
		data, err := json.Marshal(Message{Role: MessageRoleUser, Content: "hi", Timestamp: historic})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got Message
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !got.Timestamp.Equal(historic) {
			t.Fatalf("Timestamp = %v, want %v", got.Timestamp, historic)
		}
	})

	t.Run("zero timestamp omitted", func(t *testing.T) {
		data, err := json.Marshal(Message{Role: MessageRoleUser, Content: "hi"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(data), "timestamp") {
			t.Fatalf("marshalled zero timestamp: %s", data)
		}
	})

	t.Run("legacy json loads with zero timestamp", func(t *testing.T) {
		var got Message
		if err := json.Unmarshal([]byte(`{"role":"user","content":"hi"}`), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !got.Timestamp.IsZero() {
			t.Fatalf("Timestamp = %v, want zero", got.Timestamp)
		}
	})
}

func TestSameMessagesIgnoresTimestamp(t *testing.T) {
	t.Parallel()
	first := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	tests := []struct {
		name string
		a    []Message
		b    []Message
		want bool
	}{
		{
			name: "same content different timestamps",
			a:    []Message{{Role: MessageRoleUser, Content: "hi", Timestamp: first}},
			b:    []Message{{Role: MessageRoleUser, Content: "hi"}},
			want: true,
		},
		{
			name: "different content",
			a:    []Message{{Role: MessageRoleUser, Content: "hi", Timestamp: first}},
			b:    []Message{{Role: MessageRoleUser, Content: "bye", Timestamp: first}},
		},
		{
			name: "different length",
			a:    []Message{{Role: MessageRoleUser, Content: "hi"}},
			b:    []Message{{Role: MessageRoleUser, Content: "hi"}, {Role: MessageRoleAssistant, Content: "ok"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := sameMessages(tt.a, tt.b); got != tt.want {
				t.Fatalf("sameMessages = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToProviderMessagesIgnoresTimestamp(t *testing.T) {
	t.Parallel()
	historic := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	stamped := []Message{
		{Role: MessageRoleUser, Content: "hi", Timestamp: historic},
		{Role: MessageRoleAssistant, Content: "hello", Turn: 1, Timestamp: historic.Add(time.Second)},
	}
	unstamped := cloneMessages(stamped)
	for i := range unstamped {
		unstamped[i].Timestamp = time.Time{}
	}
	if got, want := ToProviderMessages(stamped), ToProviderMessages(unstamped); !reflect.DeepEqual(got, want) {
		t.Fatalf("provider messages differ by timestamp:\n got %+v\nwant %+v", got, want)
	}
}
