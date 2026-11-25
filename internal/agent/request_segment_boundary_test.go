package agent

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// cacheBoundaryIndexes returns the positions of the messages carrying
// provider.Message.CacheBoundary.
func cacheBoundaryIndexes(messages []provider.Message) []int {
	var indexes []int
	for i, message := range messages {
		if message.CacheBoundary {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

// segmentBoundaryConversation holds two immutable segments, the mutable tail
// summary and one live turn.
func segmentBoundaryConversation() []Message {
	return []Message{
		{Role: MessageRoleSummary, Content: "Segment 1. first", Segment: &SummarySegment{Sequence: 1, SourceMessageCount: 4}},
		{Role: MessageRoleSummary, Content: "Segment 2. second", Segment: &SummarySegment{Sequence: 2, SourceMessageCount: 3}},
		{Role: MessageRoleSummary, Content: "Segment 3. tail"},
		{Role: MessageRoleUser, Content: "current request"},
	}
}

// segmentBoundaryRequest is a minimal RunRequest that assembles and fits a
// prompt without a provider call. Segmented compaction is enabled so the
// request path may mark a valid chain.
func segmentBoundaryRequest() RunRequest {
	return segmentBoundaryRequestWithPolicy(SegmentedCompactionPolicy{Enabled: true})
}

// segmentBoundaryRequestWithPolicy is segmentBoundaryRequest with an explicit
// segmented compaction policy.
func segmentBoundaryRequestWithPolicy(policy SegmentedCompactionPolicy) RunRequest {
	return RunRequest{
		ResolvedModel:       provider.ResolvedModel{BackendModelID: "test-model"},
		SegmentedCompaction: policy,
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         100000,
			MaxCompletionTokens: 256,
			SummaryMaxTokens:    128,
		},
		Events: output.NoopSink{},
	}
}

// withoutSegmentMetadata returns a copy of messages with every Segment cleared,
// the shape a conversation takes when the immutable-segment feature never ran.
func withoutSegmentMetadata(messages []Message) []Message {
	cleared := make([]Message, len(messages))
	copy(cleared, messages)
	for i := range cleared {
		cleared[i].Segment = nil
	}
	return cleared
}

// anthropicRequestWireBody sends messages through the Anthropic client against
// a recording server and returns the captured request body, so byte assertions
// run on the real wire payload through the provider's public API.
func anthropicRequestWireBody(t *testing.T, messages []provider.Message) []byte {
	t.Helper()
	var (
		mu   sync.Mutex
		body []byte
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		mu.Lock()
		body = data
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-3-5-sonnet","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(server.Close)

	client, err := provider.NewAnthropic(provider.ClientConfig{BaseURL: server.URL, APIKey: "test-key", Model: "claude-3-5-sonnet"})
	if err != nil {
		t.Fatalf("NewAnthropic() error = %v", err)
	}
	if _, err := client.ChatCompletion(context.Background(), provider.ChatRequest{Model: "claude-3-5-sonnet", Messages: messages}); err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(body) == 0 {
		t.Fatal("no request body captured")
	}
	return body
}

func TestPrepareTurnMarksImmutableSegmentBoundary(t *testing.T) {
	p := newTurnProgressor(segmentBoundaryRequest(), prompt.AssemblyOptions{}, nil)
	state := RunState{Conversation: segmentBoundaryConversation()}

	_, chatRequest, _, err := p.prepareTurn(context.Background(), state)
	if err != nil {
		t.Fatalf("prepareTurn() error = %v", err)
	}
	indexes := cacheBoundaryIndexes(chatRequest.Messages)
	if len(indexes) != 1 {
		t.Fatalf("cache boundaries = %v, want exactly one", indexes)
	}
	want := summaryContextPrefix + "Segment 2. second" + summaryContextSuffix
	if got := chatRequest.Messages[indexes[0]].Content; got != want {
		t.Fatalf("marked message content = %q, want %q", got, want)
	}
}

func TestPrepareTurnLeavesLegacySummaryUnmarked(t *testing.T) {
	p := newTurnProgressor(segmentBoundaryRequest(), prompt.AssemblyOptions{}, nil)
	state := RunState{Conversation: []Message{
		{Role: MessageRoleSummary, Content: "legacy summary"},
		{Role: MessageRoleUser, Content: "current request"},
	}}

	_, chatRequest, _, err := p.prepareTurn(context.Background(), state)
	if err != nil {
		t.Fatalf("prepareTurn() error = %v", err)
	}
	if indexes := cacheBoundaryIndexes(chatRequest.Messages); len(indexes) != 0 {
		t.Fatalf("cache boundaries = %v, want none for a legacy summary", indexes)
	}
}

func TestCompactionRequestMarksImmutableSegmentBoundary(t *testing.T) {
	conversation := segmentBoundaryConversation()
	state := RunState{Conversation: conversation, Lineage: newConversationLineage(conversation)}
	candidate := ConversationCandidate{GenerationID: 1, View: ConversationViewFull, Messages: conversation}

	chatRequest, _, _, err := buildCompactionRequestWithMode(
		context.Background(), segmentBoundaryRequest(), state, candidate, prompt.CompactionModeNormal, 64,
	)
	if err != nil {
		t.Fatalf("buildCompactionRequestWithMode() error = %v", err)
	}
	indexes := cacheBoundaryIndexes(chatRequest.Messages)
	if len(indexes) != 1 {
		t.Fatalf("cache boundaries = %v, want exactly one", indexes)
	}
	want := summaryContextPrefix + "Segment 2. second" + summaryContextSuffix
	if got := chatRequest.Messages[indexes[0]].Content; got != want {
		t.Fatalf("marked message content = %q, want %q", got, want)
	}
	if last := chatRequest.Messages[len(chatRequest.Messages)-1]; last.Content != prompt.RenderConversationCompactionInstruction("", prompt.CompactionModeNormal) {
		t.Fatalf("compaction instruction changed: %q", last.Content)
	}
}

// prepareTurnChatRequest assembles a main-turn request from conversation under
// request without a provider call.
func prepareTurnChatRequest(t *testing.T, request RunRequest, conversation []Message) provider.ChatRequest {
	t.Helper()
	p := newTurnProgressor(request, prompt.AssemblyOptions{}, nil)
	_, chatRequest, _, err := p.prepareTurn(context.Background(), RunState{Conversation: conversation})
	if err != nil {
		t.Fatalf("prepareTurn() error = %v", err)
	}
	return chatRequest
}

// TestPrepareTurnFlagOffSegmentWireMatchesClearedMetadata pins D18 on the main
// request path: with segmented compaction off, a conversation still carrying
// segment metadata produces the same provider bytes as the same conversation
// with Segment cleared.
func TestPrepareTurnFlagOffSegmentWireMatchesClearedMetadata(t *testing.T) {
	request := segmentBoundaryRequestWithPolicy(SegmentedCompactionPolicy{})
	segmented := segmentBoundaryConversation()

	wireBody := func(request RunRequest, conversation []Message) []byte {
		chatRequest := prepareTurnChatRequest(t, request, conversation)
		if indexes := cacheBoundaryIndexes(chatRequest.Messages); len(indexes) != 0 {
			t.Fatalf("cache boundaries = %v, want none with the flag off", indexes)
		}
		return anthropicRequestWireBody(t, chatRequest.Messages)
	}
	got := wireBody(request, segmented)
	want := wireBody(request, withoutSegmentMetadata(segmented))
	if !bytes.Equal(got, want) {
		t.Fatalf("flag-off payload differs from the cleared-metadata payload:\n got %s\nwant %s", got, want)
	}

	// The same conversation with the flag on must reach the wire differently,
	// so the equality above cannot pass by the marker never being applied.
	on := anthropicRequestWireBody(t, prepareTurnChatRequest(t, segmentBoundaryRequest(), segmented).Messages)
	if bytes.Equal(got, on) {
		t.Fatal("flag-on payload matches the flag-off payload; the segment marker never reached the wire")
	}
}

// TestCompactionRequestFlagOffLeavesSegmentsUnmarked pins D18 on the summarizer
// request path, whose converter is built separately from the main turn.
func TestCompactionRequestFlagOffLeavesSegmentsUnmarked(t *testing.T) {
	request := segmentBoundaryRequestWithPolicy(SegmentedCompactionPolicy{})
	segmented := segmentBoundaryConversation()

	build := func(conversation []Message) []byte {
		state := RunState{Conversation: conversation, Lineage: newConversationLineage(conversation)}
		candidate := ConversationCandidate{GenerationID: 1, View: ConversationViewFull, Messages: conversation}
		chatRequest, _, _, err := buildCompactionRequestWithMode(
			context.Background(), request, state, candidate, prompt.CompactionModeNormal, 64,
		)
		if err != nil {
			t.Fatalf("buildCompactionRequestWithMode() error = %v", err)
		}
		if indexes := cacheBoundaryIndexes(chatRequest.Messages); len(indexes) != 0 {
			t.Fatalf("cache boundaries = %v, want none with the flag off", indexes)
		}
		return anthropicRequestWireBody(t, chatRequest.Messages)
	}

	got := build(segmented)
	want := build(withoutSegmentMetadata(segmented))
	if !bytes.Equal(got, want) {
		t.Fatalf("flag-off compaction payload differs from the cleared-metadata payload:\n got %s\nwant %s", got, want)
	}
}
