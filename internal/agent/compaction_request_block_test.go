package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/config"
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

	// Three-byte runes make the byte cap land inside a rune: the cap is not a
	// multiple of three.
	request := strings.Repeat("世", requestBlockMaxBytes)
	capped := capRequestBlock(request, requestBlockMaxBytes)
	body := strings.Split(capped, "\n[truncated:")[0]
	if !utf8.ValidString(capped) {
		t.Fatalf("capped request is not valid UTF-8: %q", capped)
	}
	if len(body) > requestBlockMaxBytes {
		t.Fatalf("capped prefix = %d bytes, want at most %d", len(body), requestBlockMaxBytes)
	}
	if requestBlockMaxBytes-len(body) >= utf8.UTFMax {
		t.Fatalf("capped prefix = %d bytes, want the rune backoff to stay within %d bytes of the cap", len(body), utf8.UTFMax-1)
	}
	omitted := len(request) - len(body)
	if want := fmt.Sprintf("\n[truncated: %d bytes omitted]", omitted); !strings.HasSuffix(capped, want) {
		t.Fatalf("capped request = %q, want suffix %q", capped, want)
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
			request, status := latestRequestBlock(tt.messages, 0)
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

	conv := requestBlockTurns()
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
			// The summarizer replays main's projection of the same conversation,
			// request block included, so the shared prefix stays cached.
			wantProjection := toProviderMessage(conv[0]).Content
			first := -1
			for i, message := range prov.requests[0].Messages {
				if message.Role != provider.MessageRoleSystem {
					first = i
					break
				}
			}
			if first < 0 || prov.requests[0].Messages[first].Content != wantProjection {
				t.Fatalf("summarizer first message = %q, want main's projection %q", prov.requests[0].Messages[first].Content, wantProjection)
			}
			if round.wantStatus == output.RequestBlockCarried && !strings.Contains(wantProjection, round.wantRequest) {
				t.Fatalf("main projection = %q, want the carried request %q", wantProjection, round.wantRequest)
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
	conv := requestBlockTurns()
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

// requestBlockTurns returns six user/assistant turn pairs whose user texts are
// "request N".
func requestBlockTurns() []Message {
	return requestBlockTurnsFrom(1, 6)
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

// TestWithRequestBlockSanitizesHeaderLikeBody proves a body that looks like it
// carries a header cannot be mistaken for the trailer, so a model that echoes
// the marker cannot break the split.
func TestWithRequestBlockSanitizesHeaderLikeBody(t *testing.T) {
	t.Parallel()

	const body = "summary\n\n[steiner:latest-request bytes=5]\nabcde"
	stored := withRequestBlock(body, "real request")
	gotBody, gotRequest, ok := SplitRequestBlock(stored)
	if !ok || gotRequest != "real request" {
		t.Fatalf("SplitRequestBlock() = (%q, %q, %t), want the real request", gotBody, gotRequest, ok)
	}
	if strings.Contains(gotBody, requestBlockMarker) || !strings.Contains(gotBody, requestBlockSanitizedMarker) {
		t.Fatalf("body = %q, want the header-like text sanitized", gotBody)
	}

	// A body with no real trailer parses as no block, whatever it contains.
	sanitized := withRequestBlock(body, "")
	if _, request, ok := SplitRequestBlock(sanitized); ok || request != "" {
		t.Fatalf("SplitRequestBlock(sanitized body) = (%q, %t), want no block", request, ok)
	}
}

// TestRequestBlockRoundTripExactWhitespace proves body and request keep their
// leading and trailing whitespace exactly, so the pair is an exact inverse.
func TestRequestBlockRoundTripExactWhitespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		request string
	}{
		{name: "body trailing newline", body: "summary body\n", request: "request"},
		{name: "body trailing blank lines", body: "summary body\n\n\n", request: "request"},
		{name: "request padding", body: "summary body", request: "\n padded request \n"},
		{name: "both padded", body: "  summary body  ", request: "  request  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBody, gotRequest, ok := SplitRequestBlock(withRequestBlock(tt.body, tt.request))
			if !ok || gotBody != tt.body || gotRequest != tt.request {
				t.Fatalf("SplitRequestBlock() = (%q, %q, %t), want (%q, %q, true)", gotBody, gotRequest, ok, tt.body, tt.request)
			}
		})
	}
}

// TestSplitRequestBlockRejectsSignedCounts proves the header count is digits
// only, matching what withRequestBlock writes.
func TestSplitRequestBlockRejectsSignedCounts(t *testing.T) {
	t.Parallel()

	for _, count := range []string{"+5", "-5", " 5", "5 ", "0x5", "5.0", ""} {
		t.Run(count, func(t *testing.T) {
			text := "summary\n\n" + requestBlockMarker + " bytes=" + count + "]\nabcde"
			body, request, ok := SplitRequestBlock(text)
			if ok || request != "" || body != text {
				t.Fatalf("SplitRequestBlock() = (%q, %q, %t), want the text unchanged and ok=false", body, request, ok)
			}
		})
	}
	// Leading zeros still parse as digits.
	text := "summary\n\n[steiner:latest-request bytes=05]\nabcde"
	if body, request, ok := SplitRequestBlock(text); !ok || body != "summary" || request != "abcde" {
		t.Fatalf("SplitRequestBlock() = (%q, %q, %t), want the digits-only count accepted", body, request, ok)
	}
}

// TestLatestRequestBlockSkipsNonTextualUserMessages proves skill-only and
// image-only user messages are not requests: the scan keeps the new label and
// falls back to carried only when no textual real user message exists.
func TestLatestRequestBlockSkipsNonTextualUserMessages(t *testing.T) {
	t.Parallel()

	envelope, _ := prompt.RenderSkillActivation("implement", "body of the skill")
	skillOnly := prompt.PrependSkillBlocks([]string{envelope}, "")
	tests := []struct {
		name        string
		messages    []Message
		wantRequest string
		wantStatus  string
	}{
		{
			name:        "skill-only newest message keeps scanning",
			messages:    []Message{{Role: MessageRoleUser, Content: "older request"}, {Role: MessageRoleUser, Content: skillOnly}},
			wantRequest: "older request",
			wantStatus:  output.RequestBlockNew,
		},
		{
			name:        "image-only newest message keeps scanning",
			messages:    []Message{{Role: MessageRoleUser, Content: "older request"}, {Role: MessageRoleUser, Images: []ImageBlock{{MediaType: "image/png", Data: "abc"}}}},
			wantRequest: "older request",
			wantStatus:  output.RequestBlockNew,
		},
		{
			name: "no textual message falls back to carried",
			messages: []Message{
				{Role: MessageRoleSummary, Content: withRequestBlock("earlier summary", "carried request")},
				{Role: MessageRoleUser, Content: skillOnly},
			},
			wantRequest: "carried request",
			wantStatus:  output.RequestBlockCarried,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, status := latestRequestBlock(tt.messages, 0)
			if request != tt.wantRequest || status != tt.wantStatus {
				t.Fatalf("latestRequestBlock() = (%q, %q), want (%q, %q)", request, status, tt.wantRequest, tt.wantStatus)
			}
		})
	}
}

// TestLatestRequestBlockCapsToContextWindow proves a small window shrinks the
// block below the flat maximum.
func TestLatestRequestBlockCapsToContextWindow(t *testing.T) {
	t.Parallel()

	limit := requestBlockCapBytes(8000)
	if want := 1600; limit != want {
		t.Fatalf("requestBlockCapBytes(8000) = %d, want %d", limit, want)
	}
	big := strings.Repeat("x", 4000)
	messages := []Message{{Role: MessageRoleUser, Content: big}}
	got, status := latestRequestBlock(messages, 8000)
	if status != output.RequestBlockNew {
		t.Fatalf("status = %q, want %q", status, output.RequestBlockNew)
	}
	kept := strings.Split(got, "\n[truncated:")[0]
	if len(kept) > limit {
		t.Fatalf("kept %d bytes, want at most the window cap %d", len(kept), limit)
	}
	if !strings.Contains(got, "bytes omitted]") {
		t.Fatalf("capped request = %q, want a truncation marker", got)
	}
}

// TestLatestRequestBlockTinyWindowDropsBlock proves a window whose effective
// cap rounds down to zero keeps no block, rather than storing a bare truncation
// marker as the request.
func TestLatestRequestBlockTinyWindowDropsBlock(t *testing.T) {
	t.Parallel()

	const window = 10
	if gotCap := requestBlockCapBytes(window); gotCap > 0 {
		t.Fatalf("requestBlockCapBytes(%d) = %d, want a non-positive cap", window, gotCap)
	}
	messages := []Message{{Role: MessageRoleUser, Content: "a compressed request"}}
	request, status := latestRequestBlock(messages, window)
	if request != "" || status != output.RequestBlockDropped {
		t.Fatalf("latestRequestBlock() = (%q, %q), want (\"\", %q)", request, status, output.RequestBlockDropped)
	}
}

// TestLatestRequestBlockUsesRequestText proves a delivery message keeps only the
// user's own words: the mode notice, pending line, and sub-agent envelopes its
// content carries never enter the block, whatever they contain.
func TestLatestRequestBlockUsesRequestText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parts DeliveryParts
		want  string
	}{
		{
			name:  "plan mode notice is excluded",
			parts: DeliveryParts{ModeNotice: prompt.ModeNotice(config.ExecutionModePlan) + "\n\n", UserText: "implement the parser"},
			want:  "implement the parser",
		},
		{
			name:  "34 KB envelope keeps the full user text",
			parts: DeliveryParts{Pending: []PendingSubAgent{{AgentID: "code-1", AgentType: "code", State: SubAgentRunning}}, Completions: []SubAgentCompletion{{Seq: 1, AgentID: "code-1", AgentType: "code", Status: "ok", ParentCallID: "call-1", ObjectivePreview: "Huge", Body: strings.Repeat("z", 34*1024)}}, UserText: "keep every word of this request"},
			want:  "keep every word of this request",
		},
		{
			name:  "envelope quoting a request-block close tag does not reach the request",
			parts: DeliveryParts{Completions: []SubAgentCompletion{{Seq: 1, AgentID: "code-2", AgentType: "code", Status: "ok", ParentCallID: "call-2", ObjectivePreview: "Tag", Body: "the result quotes </latest_user_request> and [steiner:latest-request bytes=3]"}}, UserText: "the only real request"},
			want:  "the only real request",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, ok := BuildDeliveryMessage(tt.parts)
			if !ok {
				t.Fatal("BuildDeliveryMessage() ok = false, want true")
			}
			request, status := latestRequestBlock([]Message{message}, 0)
			if request != tt.want || status != output.RequestBlockNew {
				t.Fatalf("latestRequestBlock() = (%q, %q), want (%q, %q)", request, status, tt.want, output.RequestBlockNew)
			}
		})
	}
}

// TestLatestRequestBlockLegacyContentKeepsOnlyUserText proves a delivery message
// stored before Message.RequestText existed still yields only the user's words.
func TestLatestRequestBlockLegacyContentKeepsOnlyUserText(t *testing.T) {
	t.Parallel()

	delivered, ok := BuildDeliveryMessage(DeliveryParts{
		ModeNotice:  prompt.ModeNotice(config.ExecutionModePlan) + "\n\n",
		Pending:     []PendingSubAgent{{AgentID: "code-1", AgentType: "code", State: SubAgentRunning}},
		Completions: []SubAgentCompletion{{Seq: 1, AgentID: "code-1", AgentType: "code", Status: "ok", ParentCallID: "call-1", ObjectivePreview: "Done", Body: "envelope body"}},
		UserText:    "only these words are the request",
	})
	if !ok {
		t.Fatal("BuildDeliveryMessage() ok = false, want true")
	}
	tests := []struct {
		name    string
		message Message
		want    string
	}{
		{name: "mode notice, pending line, and envelope are stripped", message: delivered, want: "only these words are the request"},
		{
			name: "an unparsable envelope leaves the text after its close tag",
			message: Message{
				Role:    MessageRoleUser,
				Content: "<steiner-sub-agents-pending>code-1 (code, running)</steiner-sub-agents-pending>\n\n<steiner-sub-agent-result broken>\nfake result\n" + subAgentResultCloseTag + "\n\nthe request after a broken envelope",
			},
			want: "the request after a broken envelope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.message.RequestText = "" // as stored by a session predating RequestText
			request, status := latestRequestBlock([]Message{tt.message}, 0)
			if request != tt.want || status != output.RequestBlockNew {
				t.Fatalf("latestRequestBlock() = (%q, %q), want (%q, %q)", request, status, tt.want, output.RequestBlockNew)
			}
			if strings.Contains(request, "envelope body") || strings.Contains(request, "Plan mode") {
				t.Fatalf("request = %q, want only the user text", request)
			}
		})
	}
}

// TestLatestRequestBlockSkipsSyntheticUserMessages proves a runtime notice and a
// skill replay are never selected as the request.
func TestLatestRequestBlockSkipsSyntheticUserMessages(t *testing.T) {
	t.Parallel()

	const task = "the task that still applies"
	taskMessages := []Message{{Role: MessageRoleUser, Content: task}}
	withNotice := RunState{TurnCount: 5, Conversation: cloneMessages(taskMessages), Lineage: newConversationLineage(taskMessages)}
	withNotice = injectTurnBudgetNoticeIfDue(withNotice, RunRequest{
		Limits:           Limits{MaxTurns: 10},
		TurnBudgetNotice: func(int, int) string { return "You have used 5 of 10 turns" },
	})
	if len(withNotice.Conversation) != 2 {
		t.Fatalf("notice conversation len = %d, want 2", len(withNotice.Conversation))
	}

	envelope, _ := prompt.RenderSkillActivation("implement", "body of the skill")
	replay, ok := replayedSkillMessage([]prompt.SkillBlock{{Name: "implement", State: prompt.SkillBlockActive, Text: envelope}})
	if !ok {
		t.Fatal("replayedSkillMessage() ok = false, want true")
	}
	tests := []struct {
		name     string
		messages []Message
	}{
		{name: "turn budget notice", messages: withNotice.Conversation},
		{name: "skill replay", messages: append(cloneMessages(taskMessages), replay)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, status := latestRequestBlock(tt.messages, 0)
			if request != task || status != output.RequestBlockNew {
				t.Fatalf("latestRequestBlock() = (%q, %q), want (%q, %q)", request, status, task, output.RequestBlockNew)
			}
		})
	}
}

// TestLatestRequestBlockRecapsCarriedRequest proves a carried block shrinks when
// the window shrinks under it.
func TestLatestRequestBlockRecapsCarriedRequest(t *testing.T) {
	t.Parallel()

	const window = 8000
	limit := requestBlockCapBytes(window)
	big := strings.Repeat("x", 4000)
	messages := []Message{{Role: MessageRoleSummary, Content: withRequestBlock("summary", big)}}
	got, status := latestRequestBlock(messages, window)
	if status != output.RequestBlockCarried {
		t.Fatalf("status = %q, want %q", status, output.RequestBlockCarried)
	}
	kept := strings.Split(got, "\n[truncated:")[0]
	if len(kept) > limit {
		t.Fatalf("kept %d bytes, want at most the window cap %d", len(kept), limit)
	}
	if !strings.Contains(got, "bytes omitted]") {
		t.Fatalf("capped request = %q, want a truncation marker", got)
	}
}

// TestCompactionFallbackTargetCarriesRequestBlock proves a fallback-target
// compaction stores the block and reports it as new.
func TestCompactionFallbackTargetCarriesRequestBlock(t *testing.T) {
	t.Parallel()

	run := newFallbackOperationRun(fallbackOperationNow)
	outcome, err := run.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v", err)
	}
	if outcome.FallbackStatus != output.CompactionFallbackUsed {
		t.Fatalf("FallbackStatus = %q, want %q", outcome.FallbackStatus, output.CompactionFallbackUsed)
	}
	if outcome.RequestBlock != output.RequestBlockNew {
		t.Fatalf("RequestBlock = %q, want %q", outcome.RequestBlock, output.RequestBlockNew)
	}
	stored := outcome.State.Conversation[0]
	body, request, ok := SplitRequestBlock(stored.Content)
	if stored.Role != MessageRoleSummary || !ok || request == "" {
		t.Fatalf("stored summary = (%q, %q, %t), want a summary with a request block", body, request, ok)
	}
}

// TestCompactWithLineageStoresRequestBlock proves the agent-level manual
// compaction entry stores the block in the returned conversation.
func TestCompactWithLineageStoresRequestBlock(t *testing.T) {
	t.Parallel()

	run := newFallbackOperationRun(fallbackOperationNow)
	run.req.CompactionFallback = CompactionFallback{}
	compacted, _, applied, err := NewRunner().CompactWithLineage(context.Background(), run.req, ConversationLineage{}, run.state.Conversation)
	if err != nil {
		t.Fatalf("CompactWithLineage() error = %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true")
	}
	body, request, ok := SplitRequestBlock(compacted[0].Content)
	if compacted[0].Role != MessageRoleSummary || !ok || request == "" {
		t.Fatalf("compacted summary = (%q, %q, %t), want a summary with a request block", body, request, ok)
	}
}

// TestSummarizeCompactionStagesDropsBlockFromEmergencyStage proves a stage that
// only fails because the block keeps the prompt over the threshold is adopted
// without the block instead of failing the compaction.
func TestSummarizeCompactionStagesDropsBlockFromEmergencyStage(t *testing.T) {
	t.Parallel()

	fits := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.1}
	dense := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.9}
	preStage := RunState{Conversation: requestBlockTurns(), Lineage: newConversationLineage(requestBlockTurns())}
	sawBlocked := false
	stages := summarizeCompactionStages{
		stageRunner: func(_ context.Context, _ RunRequest, _ RunState, _ int, _ ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
			summary := "normal summary"
			if params.mode == prompt.CompactionModeEmergency {
				summary = "emergency summary"
			}
			return CompactionOutcome{
				Applied:           true,
				State:             requestBlockSummaryState(summary),
				SummaryText:       withRequestBlock(summary, "latest request"),
				RequestBlock:      output.RequestBlockNew,
				RequestBlockBytes: len("latest request"),
			}, nil
		},
		fitRunner: func(_ context.Context, _ RunRequest, state RunState) (prompt.RequestTokenBudget, error) {
			if strings.Contains(state.Conversation[0].Content, requestBlockMarker) {
				sawBlocked = true
				return dense, nil
			}
			return fits, nil
		},
	}

	outcome, err := stages.run(context.Background(), RunRequest{}, preStage, 1, ConversationCandidate{Messages: preStage.Conversation})
	if err != nil {
		t.Fatalf("run() error = %v, want the compaction adopted without the block", err)
	}
	if !sawBlocked {
		t.Error("fit ran only after the block was dropped, want the escalation tried with the block first")
	}
	if outcome.RequestBlock != output.RequestBlockDropped || outcome.RequestBlockBytes != 0 {
		t.Fatalf("block = (%q, %d), want (%q, 0)", outcome.RequestBlock, outcome.RequestBlockBytes, output.RequestBlockDropped)
	}
	if outcome.SummaryText != "emergency summary" {
		t.Fatalf("SummaryText = %q, want the emergency summary", outcome.SummaryText)
	}
	if got, want := outcome.StageCount, 2; got != want {
		t.Fatalf("StageCount = %d, want %d", got, want)
	}
	if strings.Contains(outcome.State.Conversation[0].Content, requestBlockMarker) {
		t.Fatalf("stored summary = %q, want the block removed", outcome.State.Conversation[0].Content)
	}
}

// TestSummarizeCompactionStagesDropsBlockFromNormalStage proves the earlier
// applied stage is adopted without its block when the emergency stage cannot fit
// even without one.
func TestSummarizeCompactionStagesDropsBlockFromNormalStage(t *testing.T) {
	t.Parallel()

	fits := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.1}
	dense := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.9}
	preStage := RunState{Conversation: requestBlockTurns(), Lineage: newConversationLineage(requestBlockTurns())}
	stages := summarizeCompactionStages{
		stageRunner: func(_ context.Context, _ RunRequest, _ RunState, _ int, _ ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
			summary := "normal summary"
			if params.mode == prompt.CompactionModeEmergency {
				summary = "emergency " + strings.Repeat("detail ", 200)
			}
			return CompactionOutcome{
				Applied:           true,
				State:             requestBlockSummaryState(summary),
				SummaryText:       withRequestBlock(summary, "latest request"),
				RequestBlock:      output.RequestBlockNew,
				RequestBlockBytes: len("latest request"),
			}, nil
		},
		fitRunner: func(_ context.Context, _ RunRequest, state RunState) (prompt.RequestTokenBudget, error) {
			if strings.Contains(state.Conversation[0].Content, requestBlockMarker) || strings.HasPrefix(state.Conversation[0].Content, "emergency") {
				return dense, nil
			}
			return fits, nil
		},
	}

	outcome, err := stages.run(context.Background(), RunRequest{}, preStage, 1, ConversationCandidate{Messages: preStage.Conversation})
	if err != nil {
		t.Fatalf("run() error = %v, want the normal stage adopted without the block", err)
	}
	if outcome.RequestBlock != output.RequestBlockDropped {
		t.Fatalf("RequestBlock = %q, want %q", outcome.RequestBlock, output.RequestBlockDropped)
	}
	if outcome.SummaryText != "normal summary" {
		t.Fatalf("SummaryText = %q, want the normal summary", outcome.SummaryText)
	}
	// The emergency summary applied but could not be kept, so its call ran and
	// escalated the compaction count.
	if got, want := outcome.StageCount, 2; got != want {
		t.Fatalf("StageCount = %d, want %d", got, want)
	}
}

// TestSummarizeCompactionStagesPropagatesNonBudgetErrors proves the
// block-dropping rungs retry only budget failures: a provider error from the
// emergency stage propagates instead of adopting a summary without the verbatim
// request, even when dropping the block would make the result fit.
func TestSummarizeCompactionStagesPropagatesNonBudgetErrors(t *testing.T) {
	t.Parallel()

	fits := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.1}
	dense := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.9}
	providerErr := errors.New("provider 500")
	tests := []struct {
		name          string
		normalApplied bool
	}{
		{name: "normal applied", normalApplied: true},
		{name: "normal not applied", normalApplied: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conv := requestBlockTurns()
			preStage := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
			fitsWithoutBlock := 0
			stages := summarizeCompactionStages{
				stageRunner: func(_ context.Context, _ RunRequest, _ RunState, _ int, _ ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
					if params.mode == prompt.CompactionModeEmergency {
						return CompactionOutcome{}, providerErr
					}
					if !tt.normalApplied {
						return CompactionOutcome{Fit: fits}, nil
					}
					return CompactionOutcome{
						Applied:           true,
						State:             requestBlockSummaryState("normal summary"),
						SummaryText:       withRequestBlock("normal summary", "latest request"),
						RequestBlock:      output.RequestBlockNew,
						RequestBlockBytes: len("latest request"),
					}, nil
				},
				fitRunner: func(_ context.Context, _ RunRequest, state RunState) (prompt.RequestTokenBudget, error) {
					if strings.Contains(state.Conversation[0].Content, requestBlockMarker) {
						return dense, nil
					}
					fitsWithoutBlock++
					return fits, nil
				},
			}

			outcome, err := stages.run(context.Background(), RunRequest{}, preStage, 1, ConversationCandidate{Messages: conv})
			if !errors.Is(err, providerErr) {
				t.Fatalf("run() error = %v, want the provider error", err)
			}
			if outcome.Applied || outcome.RequestBlock == output.RequestBlockDropped {
				t.Fatalf("outcome = %+v, want no adopted summary", outcome)
			}
			if fitsWithoutBlock != 0 {
				t.Fatalf("fit ran %d times without the block, want the block kept", fitsWithoutBlock)
			}
		})
	}
}

// TestSummarizeCompactionStagesNormalOnlyAdoptCarriesEmergencyClear proves the
// normal-only adopt reports a backend state clear retired by either attempt.
func TestSummarizeCompactionStagesNormalOnlyAdoptCarriesEmergencyClear(t *testing.T) {
	t.Parallel()

	fits := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.1}
	dense := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.9}
	conv := requestBlockTurns()
	preStage := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
	stages := summarizeCompactionStages{
		stageRunner: func(_ context.Context, _ RunRequest, _ RunState, _ int, _ ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
			outcome := CompactionOutcome{
				Applied:           true,
				State:             requestBlockSummaryState("normal summary"),
				SummaryText:       withRequestBlock("normal summary", "latest request"),
				RequestBlock:      output.RequestBlockNew,
				RequestBlockBytes: len("latest request"),
			}
			if params.mode == prompt.CompactionModeEmergency {
				outcome.State = requestBlockSummaryState("emergency summary")
				outcome.SummaryText = withRequestBlock("emergency summary", "latest request")
				outcome.BackendStateCleared = true
			}
			return outcome, nil
		},
		fitRunner: func(_ context.Context, _ RunRequest, state RunState) (prompt.RequestTokenBudget, error) {
			// The emergency summary is over budget with or without the block, so
			// only the normal result can be adopted without it.
			if strings.Contains(state.Conversation[0].Content, requestBlockMarker) || strings.Contains(state.Conversation[0].Content, "emergency summary") {
				return dense, nil
			}
			return fits, nil
		},
	}

	outcome, err := stages.run(context.Background(), RunRequest{}, preStage, 1, ConversationCandidate{Messages: conv})
	if err != nil {
		t.Fatalf("run() error = %v, want the normal stage adopted without the block", err)
	}
	if outcome.RequestBlock != output.RequestBlockDropped || outcome.RequestBlockBytes != 0 {
		t.Fatalf("block = (%q, %d), want (%q, 0)", outcome.RequestBlock, outcome.RequestBlockBytes, output.RequestBlockDropped)
	}
	if outcome.SummaryText != "normal summary" {
		t.Fatalf("SummaryText = %q, want the normal summary", outcome.SummaryText)
	}
	if !outcome.BackendStateCleared {
		t.Error("BackendStateCleared = false, want the emergency attempt's clear carried")
	}
	// The emergency summary call ran even though its summary was discarded.
	if got, want := outcome.StageCount, 2; got != want {
		t.Fatalf("StageCount = %d, want %d", got, want)
	}
}

// TestSummarizeCompactionStagesFallbackProviderErrorRerunsOnMain proves an
// emergency-stage provider error on a fallback target escapes the ladder, so the
// operation still reruns on main instead of adopting a summary without the
// verbatim request.
func TestSummarizeCompactionStagesFallbackProviderErrorRerunsOnMain(t *testing.T) {
	t.Parallel()

	run := newFallbackOperationRun(fallbackOperationNow)
	fits := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.1}
	dense := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.9}
	providerErr := errors.New("target emergency failed")
	mainReruns := 0
	compaction := func(summaryReq *RunRequest) (CompactionOutcome, error) {
		if summaryReq == nil {
			mainReruns++
			return CompactionOutcome{Applied: true, State: run.state, Fit: fits}, nil
		}
		return summarizeCompactionStages{
			stageRunner: func(_ context.Context, _ RunRequest, _ RunState, _ int, _ ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
				if params.mode == prompt.CompactionModeEmergency {
					return CompactionOutcome{}, providerErr
				}
				return CompactionOutcome{
					Applied:           true,
					State:             requestBlockSummaryState("normal summary"),
					SummaryText:       withRequestBlock("normal summary", "latest request"),
					RequestBlock:      output.RequestBlockNew,
					RequestBlockBytes: len("latest request"),
				}, nil
			},
			fitRunner: func(_ context.Context, _ RunRequest, state RunState) (prompt.RequestTokenBudget, error) {
				if strings.Contains(state.Conversation[0].Content, requestBlockMarker) {
					return dense, nil
				}
				return fits, nil
			},
		}.run(context.Background(), run.req, run.state, 5, ConversationCandidate{Messages: run.state.Conversation})
	}

	outcome, err := compactWithFallback(context.Background(), run.req, run.state, 5, compaction)
	if err != nil {
		t.Fatalf("compactWithFallback() error = %v, want the main rerun to succeed", err)
	}
	if mainReruns != 1 {
		t.Errorf("main reruns = %d, want 1", mainReruns)
	}
	if outcome.FallbackStatus != output.CompactionFallbackSkipped {
		t.Errorf("FallbackStatus = %q, want %q", outcome.FallbackStatus, output.CompactionFallbackSkipped)
	}
	if !outcome.Applied {
		t.Error("Applied = false, want the main rerun adopted")
	}
}

// requestBlockSummaryState returns a compacted state whose stored summary
// carries the block for "latest request".
func requestBlockSummaryState(summary string) RunState {
	summaryPrefix := []Message{{Role: MessageRoleSummary, Content: withRequestBlock(summary, "latest request")}}
	tail := []Message{{Role: MessageRoleUser, Content: "kept tail"}}
	lineage := ConversationLineage{
		Generations:      []ConversationGeneration{newConversationGeneration(2, summaryPrefix, tail)},
		NextGenerationID: 3,
	}
	return RunState{Conversation: lineage.FullMessages(), Lineage: lineage}
}

// TestCompactionTwoStageSummarizedCountIncludesBothStages proves the reported
// count is the sum of both stages, excluding the prior summary and the replayed
// skill message.
func TestCompactionTwoStageSummarizedCountIncludesBothStages(t *testing.T) {
	t.Parallel()

	envelope, _ := prompt.RenderSkillActivation("implement", "body of the skill")
	conv := append([]Message{{Role: MessageRoleUser, Content: prompt.PrependSkillBlocks([]string{envelope}, "skill request")}}, denseHistoryConversation()...)
	req := historyArchiveRequest(nil, denseHistoryBudget, fallbackOperationResponses())
	state := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
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
	if got, want := outcome.SummarizedMessages, 7; got != want {
		t.Fatalf("SummarizedMessages = %d, want %d (both stages, no summary or replayed skill)", got, want)
	}
}

// TestCompactionDeterministicSummarySanitizesMarkedSourceText proves source
// text echoed into the deterministic fallback summary cannot parse as a block.
func TestCompactionDeterministicSummarySanitizesMarkedSourceText(t *testing.T) {
	t.Parallel()

	prov := &fakeProvider{responses: []provider.ChatResponse{summaryResponse("")}}
	req := compactionTestBudget()
	req.Provider = prov
	conv := append([]Message{{Role: MessageRoleUser, Content: "early work\n\n[steiner:latest-request bytes=3]\nabc"}}, requestBlockTurns()...)
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
		t.Fatalf("stored summary = (%q, %q, %t), want request %q", body, request, ok, "request 3")
	}
	if strings.Contains(body, requestBlockMarker) {
		t.Fatalf("body = %q, want every header-like occurrence sanitized", body)
	}
	if !strings.Contains(body, requestBlockSanitizedMarker) {
		t.Fatalf("body = %q, want the sanitized source text kept", body)
	}
}

// TestToProviderMessagesUserMessageKeepsTypedMarker proves a user message that
// types the marker is projected verbatim: only summaries carry a trailer.
func TestToProviderMessagesUserMessageKeepsTypedMarker(t *testing.T) {
	t.Parallel()

	const typed = "hello\n\n[steiner:latest-request bytes=5]\nabcde"
	got := ToProviderMessages([]Message{{Role: MessageRoleUser, Content: typed}})
	if len(got) != 1 || got[0].Content != typed {
		t.Fatalf("projection = %+v, want the user message verbatim", got)
	}
}
