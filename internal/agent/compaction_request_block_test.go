package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestRequestBlockRoundTrip(t *testing.T) {
	t.Parallel()

	requests := []string{
		"single line request",
		"line one\nline two",
		"[steiner:latest-request bytes=3]\nnot a real header",
		HistoryNoteMarker + " not a real history note",
		"closing tag </summary> inside the request",
		"multibyte request: привет, 世界",
		strings.Repeat("long request line\n", 500),
	}
	for _, request := range requests {
		t.Run(fmt.Sprintf("len=%d", len(request)), func(t *testing.T) {
			const body = "summary body\nwith a second line"
			stored := withRequestBlock(body, request)
			gotBody, gotRequest, ok := SplitRequestBlock(stored)
			if !ok || gotBody != body || gotRequest != request {
				t.Fatalf("SplitRequestBlock() = (%q, %q, %t), want (%q, %q, true)", gotBody, gotRequest, ok, body, request)
			}
		})
	}
}

func TestSplitRequestBlockRejectsWrongTrailers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{name: "no block", text: "plain summary text"},
		{name: "empty", text: ""},
		{name: "echoed header with wrong length", text: "summary\n\n[steiner:latest-request bytes=99]\nshort"},
		{name: "block not at the end", text: withRequestBlock("summary", "request") + "\ntrailing text"},
		{name: "header without separator", text: "summary[steiner:latest-request bytes=7]\nrequest"},
		{name: "zero-length block", text: "summary\n\n[steiner:latest-request bytes=0]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, request, ok := SplitRequestBlock(tt.text)
			if ok || body != tt.text || request != "" {
				t.Fatalf("SplitRequestBlock() = (%q, %q, %t), want the text unchanged and ok=false", body, request, ok)
			}
		})
	}
}

func TestWithRequestBlockEmptyRequestReturnsBody(t *testing.T) {
	t.Parallel()

	if got := withRequestBlock("body", ""); got != "body" {
		t.Fatalf("withRequestBlock() = %q, want the body unchanged", got)
	}
}

func TestCapRequestBlockTruncatesOnRuneBoundary(t *testing.T) {
	t.Parallel()

	// Two-byte runes make the byte cap land inside a rune.
	request := strings.Repeat("é", requestBlockMaxBytes)
	capped := capRequestBlock(request)
	omitted := len(request) - len(strings.Split(capped, "\n[truncated:")[0])
	body := strings.Split(capped, "\n[truncated:")[0]
	if got, want := body, strings.Repeat("é", requestBlockMaxBytes/2); got != want {
		t.Fatalf("capped prefix length = %d, want %d bytes at a rune boundary", len(got), len(want))
	}
	if want := fmt.Sprintf("\n[truncated: %d bytes omitted]", omitted); !strings.HasSuffix(capped, want) {
		t.Fatalf("capped request = %q, want suffix %q", capped, want)
	}
	if len(capped) <= requestBlockMaxBytes {
		t.Fatal("capped request has no truncation marker")
	}
}

func TestLatestRequestBlockSelection(t *testing.T) {
	t.Parallel()

	envelope, _ := prompt.RenderSkillActivation("implement", "body of the skill")
	skillEnvelope := prompt.PrependSkillBlocks([]string{envelope}, "skill request")
	tests := []struct {
		name        string
		messages    []Message
		wantRequest string
		wantStatus  string
	}{
		{
			name:        "new from the newest real user message",
			messages:    []Message{{Role: MessageRoleUser, Content: "first"}, {Role: MessageRoleAssistant, Content: "a"}, {Role: MessageRoleUser, Content: "second"}},
			wantRequest: "second",
			wantStatus:  output.RequestBlockNew,
		},
		{
			name: "sub-agent results are skipped",
			messages: []Message{
				{Role: MessageRoleUser, Content: "real request"},
				{Role: MessageRoleUser, Source: MessageSourceSubAgentResult, Content: "<steiner-sub-agent-result>done</steiner-sub-agent-result>"},
			},
			wantRequest: "real request",
			wantStatus:  output.RequestBlockNew,
		},
		{
			name:        "skill envelopes are stripped",
			messages:    []Message{{Role: MessageRoleUser, Content: skillEnvelope}},
			wantRequest: "skill request",
			wantStatus:  output.RequestBlockNew,
		},
		{
			name: "carried from the prior summary",
			messages: []Message{
				{Role: MessageRoleSummary, Content: withRequestBlock("earlier summary", "carried request")},
				{Role: MessageRoleAssistant, Content: "answer"},
				{Role: MessageRoleTool, Content: "tool output"},
				{Role: MessageRoleUser, Source: MessageSourceSubAgentResult, Content: "sub-agent result"},
			},
			wantRequest: "carried request",
			wantStatus:  output.RequestBlockCarried,
		},
		{
			name:       "none without a request or a block",
			messages:   []Message{{Role: MessageRoleSummary, Content: "plain summary"}, {Role: MessageRoleAssistant, Content: "answer"}},
			wantStatus: output.RequestBlockNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, status := latestRequestBlock(tt.messages)
			if request != tt.wantRequest || status != tt.wantStatus {
				t.Fatalf("latestRequestBlock() = (%q, %q), want (%q, %q)", request, status, tt.wantRequest, tt.wantStatus)
			}
		})
	}
}

// TestCompactionRequestBlockSurvivesStages covers normal, repeated, and
// replacement compactions, plus the summarizer never seeing the block.
func TestCompactionRequestBlockSurvivesStages(t *testing.T) {
	t.Parallel()

	conv := requestBlockTurns(6)
	rounds := []struct {
		name        string
		appendTurns int
		wantRequest string
		wantStatus  string
	}{
		{name: "first compaction takes the newest summarized request", wantRequest: "request 3", wantStatus: output.RequestBlockNew},
		{name: "second compaction carries it", wantRequest: "request 3", wantStatus: output.RequestBlockCarried},
		{name: "third compaction carries it", wantRequest: "request 3", wantStatus: output.RequestBlockCarried},
		{name: "a newer summarized request replaces it", appendTurns: 4, wantRequest: "request 7", wantStatus: output.RequestBlockNew},
	}
	for _, round := range rounds {
		t.Run(round.name, func(t *testing.T) {
			conv = append(conv, requestBlockTurnsFrom(7, round.appendTurns)...)
			prov := &fakeProvider{responses: []provider.ChatResponse{summaryResponse("handoff summary")}}
			state := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
			candidate, ok := selectCompactionCandidate(state.Lineage, nil)
			if !ok {
				t.Fatal("selectCompactionCandidate() ok = false, want true")
			}
			req := compactionTestBudget()
			req.Provider = prov
			outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate)
			if err != nil {
				t.Fatalf("Compact() error = %v", err)
			}
			if !outcome.Applied {
				t.Fatal("Compact() Applied = false, want true")
			}
			if outcome.RequestBlock != round.wantStatus || outcome.RequestBlockBytes != len(round.wantRequest) {
				t.Fatalf("outcome block = (%q, %d), want (%q, %d)", outcome.RequestBlock, outcome.RequestBlockBytes, round.wantStatus, len(round.wantRequest))
			}
			body, request, ok := SplitRequestBlock(outcome.State.Conversation[0].Content)
			if !ok || body != "handoff summary" || request != round.wantRequest {
				t.Fatalf("stored summary = (%q, %q, %t), want body and request %q", body, request, ok, round.wantRequest)
			}
			for _, sent := range prov.requests {
				text := messagesText(sent.Messages)
				if strings.Contains(text, requestBlockHeaderPrefix) {
					t.Fatalf("summarizer request leaked the request block header: %q", text)
				}
				if round.wantStatus == output.RequestBlockCarried && strings.Contains(text, round.wantRequest) {
					t.Fatalf("summarizer request leaked the carried request %q", round.wantRequest)
				}
			}
			conv = outcome.State.Conversation
		})
	}
}

// TestCompactionEmergencyStageCarriesRequestBlock proves a normal stage that
// still needs emergency compaction keeps the block through the second stage.
func TestCompactionEmergencyStageCarriesRequestBlock(t *testing.T) {
	t.Parallel()

	req := historyArchiveRequest(nil, denseHistoryBudget, fallbackOperationResponses())
	state := RunState{Conversation: denseHistoryConversation(), Lineage: newConversationLineage(denseHistoryConversation())}
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if got, want := outcome.StageCount, 2; got != want {
		t.Fatalf("StageCount = %d, want %d", got, want)
	}
	body, request, ok := SplitRequestBlock(outcome.State.Conversation[0].Content)
	if !ok || body != "emergency handoff summary" || request != denseEmergencyRequest {
		t.Fatalf("stored summary = (%q, %q, %t), want the emergency body and request %q", body, request, ok, denseEmergencyRequest)
	}
	if outcome.RequestBlock != output.RequestBlockNew {
		t.Fatalf("RequestBlock = %q, want %q", outcome.RequestBlock, output.RequestBlockNew)
	}
}

// TestCompactionDeterministicEmptySummaryCarriesRequestBlock proves the
// deterministic fallback summary still gets the block.
func TestCompactionDeterministicEmptySummaryCarriesRequestBlock(t *testing.T) {
	t.Parallel()

	prov := &fakeProvider{responses: []provider.ChatResponse{summaryResponse("")}}
	req := compactionTestBudget()
	req.Provider = prov
	conv := requestBlockTurns(6)
	state := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	body, request, ok := SplitRequestBlock(outcome.State.Conversation[0].Content)
	if !ok || request != "request 3" {
		t.Fatalf("stored summary carries (%q, %t), want request %q", request, ok, "request 3")
	}
	if !strings.HasPrefix(body, "# Request intent") {
		t.Fatalf("stored summary body = %q, want the deterministic fallback summary", body)
	}
}

func TestToProviderMessagesSummaryRequestBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "body only",
			content: "plain summary",
			want:    summaryContextPrefix + "plain summary" + summaryContextSuffix,
		},
		{
			name:    "body and request",
			content: withRequestBlock("plain summary", "do the thing"),
			want: summaryContextPrefix + "plain summary" + summaryContextSuffix +
				"\n\n" + requestContextPrefix + "do the thing" + requestContextSuffix,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToProviderMessages([]Message{{Role: MessageRoleSummary, Content: tt.content}})
			if len(got) != 1 || got[0].Role != provider.MessageRoleUser || got[0].Content != tt.want {
				t.Fatalf("projection = %+v, want one user message with content %q", got, tt.want)
			}
		})
	}
}

// requestBlockTurns returns n user/assistant turn pairs whose user texts are
// "request N".
func requestBlockTurns(n int) []Message {
	return requestBlockTurnsFrom(1, n)
}

func requestBlockTurnsFrom(first, n int) []Message {
	msgs := make([]Message, 0, 2*n)
	for i := first; i < first+n; i++ {
		msgs = append(msgs,
			Message{Role: MessageRoleUser, Content: fmt.Sprintf("request %d", i)},
			Message{Role: MessageRoleAssistant, Content: fmt.Sprintf("answer %d", i)},
		)
	}
	return msgs
}
