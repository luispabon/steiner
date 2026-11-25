package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// elisionTestArchive is a fakeRecallArchive whose returned paths can be made
// long enough to exercise the marker-size threshold.
type elisionTestArchive struct {
	*fakeRecallArchive
	dir string
}

func (a elisionTestArchive) Write(ctx context.Context, entry RecallEntry) (string, error) {
	if _, err := a.fakeRecallArchive.Write(ctx, entry); err != nil {
		return "", err
	}
	return a.dir + "/" + RecallName(entry), nil
}

func (a elisionTestArchive) Path(name string) string { return a.dir + "/" + name }

func elisionTestRequest(archive RecallArchive) RunRequest {
	req := compactionTestBudget()
	req.RecallArchive = archive
	return withCompactionElision(req)
}

func elisionTestState(msgs []Message) RunState {
	return RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}
}

// elisionTestBody returns a result body long enough to pass the size thresholds
// whose tokenization resists compression by repeated text.
func elisionTestBody(label string) string {
	words := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	var b strings.Builder
	b.WriteString(label)
	for i := 0; b.Len() < 4800; i++ {
		b.WriteString(fmt.Sprintf(" %d-%s", i, words[i%len(words)]))
	}
	return b.String()
}

// elisionTestConversation returns a conversation whose tool results all have a
// later complete assistant message, so every result is consumed.
func elisionTestConversation(bodies []string) []Message {
	calls := make([]ToolCall, len(bodies))
	tools := make([]Message, len(bodies))
	for i, body := range bodies {
		calls[i] = ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "bash"}
		tools[i] = Message{Role: MessageRoleTool, Name: "bash", ToolCallID: calls[i].ID, Content: body}
	}
	msgs := []Message{
		{Role: MessageRoleUser, Content: "start"},
		{Role: MessageRoleAssistant, ToolCalls: calls},
	}
	msgs = append(msgs, tools...)
	return append(msgs, Message{Role: MessageRoleAssistant, Content: "done"})
}

func elisionTestTokens(t *testing.T, req RunRequest, body string) int {
	t.Helper()
	settings := req.compactionElision
	if settings == nil {
		t.Fatal("request is not stamped with compaction elision settings")
	}
	state := RunState{}
	baseline, err := fitConversationState(context.Background(), settings.main, state.WithConversation(nil))
	if err != nil {
		t.Fatalf("baseline fit error = %v", err)
	}
	tokens, err := estimateRetainedResultTokens(context.Background(), settings.main, state, baseline, retainedResultTarget{entry: RecallEntry{Kind: "tool", Body: body}}, fitConversationState)
	if err != nil {
		t.Fatalf("estimate error = %v", err)
	}
	return tokens
}

func conversationWarnings(t *testing.T, rec *eventRecorder) []output.ConversationWarningEvent {
	t.Helper()
	var warnings []output.ConversationWarningEvent
	for _, event := range rec.snapshot() {
		if event.Type != output.EventTypeConversationWarning {
			continue
		}
		payload, ok := event.Payload.(output.ConversationWarningEvent)
		if !ok {
			t.Fatalf("warning payload = %T, want output.ConversationWarningEvent", event.Payload)
		}
		warnings = append(warnings, payload)
	}
	return warnings
}

// TestElisionRetainedToolOutputsKeepsNewest proves the running budget keeps the
// newest consumed result verbatim, then elides every older consumed one.
func TestElisionRetainedToolOutputsKeepsNewest(t *testing.T) {
	t.Parallel()
	bodies := []string{elisionTestBody("oldest"), elisionTestBody("middle"), elisionTestBody("newest")}
	msgs := elisionTestConversation(bodies)
	archive := &fakeRecallArchive{}
	req := elisionTestRequest(archive)
	state := elisionTestState(msgs)
	budget := elisionTestTokens(t, req, bodies[1]) + elisionTestTokens(t, req, bodies[2])

	out, elided := elideRetainedToolOutputs(context.Background(), req, state, msgs, budget, fitConversationState)
	if !elided {
		t.Fatal("elided = false, want true")
	}
	if got := out[2].Content; !strings.HasPrefix(got, RecallMarkerPrefix) {
		t.Errorf("oldest result content = %q, want a marker", got)
	}
	if got := out[3].Content; got != bodies[1] {
		t.Errorf("middle result changed: %q", got)
	}
	if got := out[4].Content; got != bodies[2] {
		t.Errorf("newest result changed: %q", got)
	}
	if got := msgs[2].Content; got != bodies[0] {
		t.Errorf("source message mutated: %q", got)
	}
	if len(archive.written) != 1 || archive.written[0].Body != bodies[0] {
		t.Fatalf("archived entries = %+v, want only the oldest body", archive.written)
	}
}

// TestElisionRetainedToolOutputsCountsUnconsumed proves an unconsumed body counts
// against the budget even though it is never elided.
func TestElisionRetainedToolOutputsCountsUnconsumed(t *testing.T) {
	t.Parallel()
	bodies := []string{elisionTestBody("oldest"), elisionTestBody("middle"), elisionTestBody("newest")}
	calls := []ToolCall{{ID: "call-0", Name: "bash"}, {ID: "call-1", Name: "bash"}, {ID: "call-2", Name: "bash"}}
	msgs := []Message{
		{Role: MessageRoleUser, Content: "start"},
		{Role: MessageRoleAssistant, ToolCalls: calls[:1]},
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-0", Content: bodies[0]},
		{Role: MessageRoleAssistant, Content: "read the first result"},
		{Role: MessageRoleUser, Content: "again"},
		{Role: MessageRoleAssistant, ToolCalls: calls[1:]},
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-1", Content: bodies[1]},
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-2", Content: bodies[2]},
	}
	archive := &fakeRecallArchive{}
	req := elisionTestRequest(archive)
	state := elisionTestState(msgs)
	// Two unconsumed bodies spend the budget, so only the consumed oldest one
	// is elided. Without counting them the budget would not be reached.
	budget := elisionTestTokens(t, req, bodies[1]) + elisionTestTokens(t, req, bodies[2])

	out, elided := elideRetainedToolOutputs(context.Background(), req, state, msgs, budget, fitConversationState)
	if !elided {
		t.Fatal("elided = false, want true")
	}
	if got := out[2].Content; !strings.HasPrefix(got, RecallMarkerPrefix) {
		t.Errorf("consumed oldest result = %q, want a marker", got)
	}
	if out[6].Content != bodies[1] || out[7].Content != bodies[2] {
		t.Error("an unconsumed result was elided")
	}
}

// TestElisionRetainedToolOutputsThresholds proves both size limits keep a body
// verbatim, including a marker-larger-than-body case caused by a long path.
func TestElisionRetainedToolOutputsThresholds(t *testing.T) {
	t.Parallel()
	big := elisionTestBody("newer")
	cases := []struct {
		label  string
		body   string
		path   string
		wantEl bool
	}{
		{label: "below the byte floor", body: strings.Repeat("tiny ", 100), wantEl: false},
		{label: "under four markers", body: elisionTestBody("medium")[:2048], path: strings.Repeat("deep/", 200), wantEl: false},
		{label: "above four markers", body: elisionTestBody("medium")[:2048], wantEl: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			base := &fakeRecallArchive{}
			var archive RecallArchive = base
			if tc.path != "" {
				archive = elisionTestArchive{fakeRecallArchive: base, dir: tc.path}
			}
			req := elisionTestRequest(archive)
			msgs := elisionTestConversation([]string{tc.body, big})
			state := elisionTestState(msgs)
			budget := elisionTestTokens(t, req, big)

			out, elided := elideRetainedToolOutputs(context.Background(), req, state, msgs, budget, fitConversationState)
			if elided != tc.wantEl {
				t.Fatalf("elided = %v, want %v", elided, tc.wantEl)
			}
			if tc.wantEl && !strings.HasPrefix(out[2].Content, RecallMarkerPrefix) {
				t.Errorf("eligible body content = %q, want a marker", out[2].Content)
			}
			if !tc.wantEl && out[2].Content != tc.body {
				t.Errorf("ineligible body changed: %q", out[2].Content)
			}
		})
	}
}

// TestElisionRetainedToolOutputsWriteFailurePreservesCandidate proves a failed
// archive write keeps only that body verbatim and warns once, while older
// candidates still elide.
func TestElisionRetainedToolOutputsWriteFailurePreservesCandidate(t *testing.T) {
	t.Parallel()
	bodies := []string{elisionTestBody("oldest"), elisionTestBody("failed"), elisionTestBody("kept"), elisionTestBody("newest")}
	msgs := elisionTestConversation(bodies)
	archive := &fakeRecallArchive{failAt: map[int]error{1: errors.New("disk full")}}
	var rec eventRecorder
	req := elisionTestRequest(archive)
	req.Events = rec.sink()
	state := elisionTestState(msgs)
	budget := elisionTestTokens(t, req, bodies[2]) + elisionTestTokens(t, req, bodies[3])

	out, elided := elideRetainedToolOutputs(context.Background(), req, state, msgs, budget, fitConversationState)
	if !elided {
		t.Fatal("elided = false, want true")
	}
	if out[3].Content != bodies[1] {
		t.Errorf("candidate with a failed write = %q, want its body verbatim", out[3].Content)
	}
	if !strings.HasPrefix(out[2].Content, RecallMarkerPrefix) {
		t.Errorf("older candidate = %q, want a marker", out[2].Content)
	}
	warnings := conversationWarnings(t, &rec)
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "disk full") {
		t.Fatalf("warnings = %+v, want one archive failure", warnings)
	}
}

// TestElisionRetainedToolOutputsMixedEnvelopeMessage proves only the envelope body
// is replaced, with its attributes and the rest of the message preserved.
func TestElisionRetainedToolOutputsMixedEnvelopeMessage(t *testing.T) {
	t.Parallel()
	c1 := SubAgentCompletion{Seq: 1, AgentID: "code-1", AgentType: "code", Status: "completed", ParentCallID: "call-1", ObjectivePreview: "one", Body: elisionTestBody("agent-one")}
	c2 := SubAgentCompletion{Seq: 2, AgentID: "code-2", AgentType: "review", Status: "completed", ParentCallID: "call-2", ObjectivePreview: "two", Body: elisionTestBody("agent-two")}
	skill, _ := prompt.RenderSkillActivation("plan", "plan body")
	msg, ok := BuildDeliveryMessage(DeliveryParts{
		SkillBlocks: []string{skill},
		Completions: []SubAgentCompletion{c2, c1},
		UserText:    "user text",
		Images:      []ImageBlock{{ID: "image-1", MediaType: "image/png"}},
	})
	if !ok {
		t.Fatal("BuildDeliveryMessage returned false")
	}
	msgs := []Message{msg, {Role: MessageRoleAssistant, Content: "done"}}
	archive := &fakeRecallArchive{}
	req := elisionTestRequest(archive)
	state := elisionTestState(msgs)
	budget := elisionTestTokens(t, req, c2.Body)

	out, elided := elideRetainedToolOutputs(context.Background(), req, state, msgs, budget, fitConversationState)
	if !elided {
		t.Fatal("elided = false, want true")
	}
	blocks := prompt.SplitMessageBlocks(out[0].Content)
	if len(blocks.ResultEnvelopes) != 2 {
		t.Fatalf("envelopes = %d, want 2", len(blocks.ResultEnvelopes))
	}
	if len(blocks.SkillBlocks) != 1 || blocks.Rest != "user text" {
		t.Errorf("skill blocks or user text lost: %+v", blocks)
	}
	if len(out[0].Images) != 1 || out[0].Images[0].ID != "image-1" {
		t.Errorf("images = %+v, want the original image", out[0].Images)
	}
	first, ok := ParseSubAgentResultEnvelope(blocks.ResultEnvelopes[0])
	if !ok {
		t.Fatalf("rewritten envelope does not parse: %q", blocks.ResultEnvelopes[0])
	}
	if !strings.HasPrefix(first.Inner, RecallMarkerPrefix) {
		t.Errorf("older envelope inner = %q, want a marker", first.Inner)
	}
	want := fmt.Sprintf(`bytes="%d"`, len(first.Inner))
	if !strings.Contains(blocks.ResultEnvelopes[0], want) {
		t.Errorf("envelope lacks rewritten byte length %s: %q", want, blocks.ResultEnvelopes[0])
	}
	if first.AgentID != "code-1" || first.AgentType != "code" || first.Status != "completed" || first.CallID != "call-1" {
		t.Errorf("attributes changed: %+v", first)
	}
	second, ok := ParseSubAgentResultEnvelope(blocks.ResultEnvelopes[1])
	if !ok || second.AgentID != "code-2" || second.Inner == "" || strings.HasPrefix(second.Inner, RecallMarkerPrefix) {
		t.Errorf("newer envelope changed: %+v", second)
	}
}

// TestElisionRetainedToolOutputsArchivesMarkerShapedBody proves no prefix
// recognition: a body that looks like a marker is archived verbatim and elided,
// and the markers it writes are too small to be elided again.
func TestElisionRetainedToolOutputsArchivesMarkerShapedBody(t *testing.T) {
	t.Parallel()
	markerShaped := RecallMarker("existing-name", "/recall/existing-name") + elisionTestBody(" old body")
	bodies := []string{markerShaped, elisionTestBody("newest")}
	msgs := elisionTestConversation(bodies)
	archive := &fakeRecallArchive{}
	req := elisionTestRequest(archive)
	state := elisionTestState(msgs)
	budget := elisionTestTokens(t, req, bodies[1])

	out, elided := elideRetainedToolOutputs(context.Background(), req, state, msgs, budget, fitConversationState)
	if !elided {
		t.Fatal("elided = false, want true")
	}
	if len(archive.written) != 1 || archive.written[0].Body != markerShaped {
		t.Fatalf("archived entries = %+v, want the marker-shaped body verbatim", archive.written)
	}
	if out[2].Content == markerShaped || !strings.HasPrefix(out[2].Content, RecallMarkerPrefix) {
		t.Errorf("marker-shaped body content = %q, want a fresh marker", out[2].Content)
	}

	again, elidedAgain := elideRetainedToolOutputs(context.Background(), req, state, out, 1, fitConversationState)
	if elidedAgain {
		t.Error("a second pass elided already-written markers")
	}
	if !reflect.DeepEqual(again, out) {
		t.Error("a second pass changed the retained messages")
	}
	if len(archive.written) != 1 {
		t.Errorf("a second pass wrote %d entries, want 1", len(archive.written)-1)
	}
}

// TestElisionRetainedToolOutputsEstimationErrorWarnsOnce proves a failed
// per-result estimate warns once, keeps the messages verbatim, and stops the
// walk instead of trying every remaining result.
func TestElisionRetainedToolOutputsEstimationErrorWarnsOnce(t *testing.T) {
	t.Parallel()
	bodies := []string{elisionTestBody("old"), elisionTestBody("new")}
	msgs := elisionTestConversation(bodies)
	var rec eventRecorder
	req := elisionTestRequest(&fakeRecallArchive{})
	req.Events = rec.sink()
	state := elisionTestState(msgs)
	calls := 0
	failingFit := func(ctx context.Context, fitReq RunRequest, fitState RunState) (prompt.RequestTokenBudget, error) {
		calls++
		if calls > 1 {
			return prompt.RequestTokenBudget{}, errors.New("no tokenizer")
		}
		return fitConversationState(ctx, fitReq, fitState)
	}

	out, elided := elideRetainedToolOutputs(context.Background(), req, state, msgs, 1, failingFit)
	if elided {
		t.Error("elided = true after an estimation error")
	}
	if !reflect.DeepEqual(historyArchiveContents(out), historyArchiveContents(msgs)) {
		t.Error("an estimation error changed the retained messages")
	}
	if calls != 2 {
		t.Errorf("fit calls = %d, want the baseline and one estimate", calls)
	}
	warnings := conversationWarnings(t, &rec)
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "no tokenizer") {
		t.Fatalf("warnings = %+v, want one estimate failure", warnings)
	}
}

// TestElisionRetainedToolOutputsSkipsExhaustedEstimates proves that once the
// budget is spent, older consumed candidates are elided without estimating
// them: an estimator that would fail for them never runs and elision continues.
func TestElisionRetainedToolOutputsSkipsExhaustedEstimates(t *testing.T) {
	t.Parallel()
	bodies := []string{elisionTestBody("oldest"), elisionTestBody("middle"), elisionTestBody("newest")}
	msgs := elisionTestConversation(bodies)
	archive := &fakeRecallArchive{}
	var rec eventRecorder
	req := elisionTestRequest(archive)
	req.Events = rec.sink()
	state := elisionTestState(msgs)
	// The newest body alone fills the budget, so every older body is decided
	// without a token count.
	budget := elisionTestTokens(t, req, bodies[2])
	calls := 0
	failingFit := func(ctx context.Context, fitReq RunRequest, fitState RunState) (prompt.RequestTokenBudget, error) {
		calls++
		if calls > 2 {
			return prompt.RequestTokenBudget{}, errors.New("estimate after exhaustion")
		}
		return fitConversationState(ctx, fitReq, fitState)
	}

	out, elided := elideRetainedToolOutputs(context.Background(), req, state, msgs, budget, failingFit)
	if !elided {
		t.Fatal("elided = false, want true")
	}
	if calls != 2 {
		t.Errorf("fit calls = %d, want the baseline and the budget-crossing body only", calls)
	}
	for _, index := range []int{2, 3} {
		if !strings.HasPrefix(out[index].Content, RecallMarkerPrefix) {
			t.Errorf("older consumed result %d = %q, want a marker", index, out[index].Content)
		}
	}
	if out[4].Content != bodies[2] {
		t.Errorf("budget-crossing result = %q, want its body verbatim", out[4].Content)
	}
	if len(archive.written) != 2 {
		t.Errorf("archived entries = %d, want the two older bodies", len(archive.written))
	}
	if warnings := conversationWarnings(t, &rec); len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
}

// TestElisionRetainedToolOutputsSkips proves nil archives and stateful consuming
// providers leave the retained messages untouched.
func TestElisionRetainedToolOutputsSkips(t *testing.T) {
	t.Parallel()
	bodies := []string{elisionTestBody("a"), elisionTestBody("b")}
	msgs := elisionTestConversation(bodies)

	nilArchive := elisionTestRequest(nil)
	if out, elided := elideRetainedToolOutputs(context.Background(), nilArchive, elisionTestState(msgs), msgs, 1, fitConversationState); elided || !reflect.DeepEqual(out, msgs) {
		t.Error("a nil archive changed the retained messages")
	}

	stateful := elisionTestRequest(&fakeRecallArchive{})
	stateful.Provider = newClaudeLikeCompactionProvider(false)
	stateful = withCompactionElision(stateful)
	if out, elided := elideRetainedToolOutputs(context.Background(), stateful, elisionTestState(msgs), msgs, 1, fitConversationState); elided || !reflect.DeepEqual(out, msgs) {
		t.Error("a stateful provider elided result bodies")
	}
}

// TestCompactionFallbackRequestKeepsMainElisionSettings proves a fallback copy
// carries the main decisions: elision follows the consuming provider and the
// main estimator, never the fallback target.
func TestCompactionFallbackRequestKeepsMainElisionSettings(t *testing.T) {
	t.Parallel()
	main := compactionFallbackTestRequest(&fakeProvider{})
	main.Prompt.SkillsRoots = nil
	main.Prompt.SkillNames = nil
	main.ResolvedModel.PromptSuffix = "main suffix"
	main = withCompactionElision(main)
	fallback, err := compactionFallbackRequest(main, compactionFallbackTestTarget())
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}
	if fallback.compactionElision != main.compactionElision {
		t.Fatal("fallback copy lost the main elision settings")
	}
	body := elisionTestBody("fallback")
	if got, want := elisionTestTokens(t, fallback, body), elisionTestTokens(t, main, body); got != want {
		t.Errorf("fallback estimate = %d, want the main estimate %d", got, want)
	}

	stateful := compactionFallbackTestRequest(newClaudeLikeCompactionProvider(false))
	stateful.Prompt.SkillsRoots = nil
	stateful.Prompt.SkillNames = nil
	stateful = withCompactionElision(stateful)
	statefulFallback, err := compactionFallbackRequest(stateful, compactionFallbackTestTarget())
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}
	if !statefulFallback.compactionElision.skip {
		t.Error("a non-stateful fallback cleared the stateful main skip")
	}
}

// TestElisionRetainedToolOutputsFrozenRequests proves the generation stores the
// markers once, so requests assembled from it are byte-identical.
func TestElisionRetainedToolOutputsFrozenRequests(t *testing.T) {
	t.Parallel()
	bodies := []string{elisionTestBody("old"), elisionTestBody("new")}
	msgs := elisionTestConversation(bodies)
	req := elisionTestRequest(&fakeRecallArchive{})
	state := elisionTestState(msgs)
	budget := elisionTestTokens(t, req, bodies[1])
	elidedRetention, elided := elideRetainedToolOutputs(context.Background(), req, state, msgs, budget, fitConversationState)
	if !elided {
		t.Fatal("elided = false, want true")
	}
	frozen := buildSummarizedCompactionState(state, "summary", elidedRetention)

	first, err := prompt.Assemble(context.Background(), assemblyOptions(prepareBasePrompt(req), frozen))
	if err != nil {
		t.Fatalf("first Assemble() error = %v", err)
	}
	second, err := prompt.Assemble(context.Background(), assemblyOptions(prepareBasePrompt(req), frozen))
	if err != nil {
		t.Fatalf("second Assemble() error = %v", err)
	}
	firstJSON, err := json.Marshal(first.Messages)
	if err != nil {
		t.Fatalf("marshal first messages: %v", err)
	}
	secondJSON, err := json.Marshal(second.Messages)
	if err != nil {
		t.Fatalf("marshal second messages: %v", err)
	}
	if !reflect.DeepEqual(first.Messages, second.Messages) || string(firstJSON) != string(secondJSON) {
		t.Fatal("requests assembled from the frozen generation differ")
	}
	found := false
	for _, msg := range first.Messages {
		if strings.Contains(msg.Content, RecallMarkerPrefix) {
			found = true
		}
	}
	if !found {
		t.Fatal("assembled request lacks the frozen marker")
	}
}

// elisionCompactionConversation returns a summarized source turn followed by a
// retained unit of consumed tool results, a closing assistant unit and a final
// turn. The results fall inside the normal three-unit retention window.
func elisionCompactionConversation(resultCount int) (msgs []Message, sourceBody string, retainedBodies []string) {
	sourceBody = elisionTestBody("source")
	sourceCalls := []ToolCall{{ID: "call-source", Name: "bash"}}
	msgs = []Message{
		{Role: MessageRoleUser, Content: "source question"},
		{Role: MessageRoleAssistant, ToolCalls: sourceCalls},
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-source", Content: sourceBody},
		{Role: MessageRoleAssistant, Content: "source answer"},
	}
	calls := make([]ToolCall, resultCount)
	tools := make([]Message, resultCount)
	retainedBodies = make([]string, resultCount)
	for i := range resultCount {
		retainedBodies[i] = elisionTestBody(fmt.Sprintf("retained-%d", i))
		calls[i] = ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "bash"}
		tools[i] = Message{Role: MessageRoleTool, Name: "bash", ToolCallID: calls[i].ID, Content: retainedBodies[i]}
	}
	msgs = append(msgs,
		Message{Role: MessageRoleUser, Content: "retained question"},
		Message{Role: MessageRoleAssistant, ToolCalls: calls},
	)
	msgs = append(msgs, tools...)
	msgs = append(msgs,
		Message{Role: MessageRoleAssistant, Content: "retained answer"},
		Message{Role: MessageRoleUser, Content: "final question"},
		Message{Role: MessageRoleAssistant, Content: "final answer"},
	)
	return msgs, sourceBody, retainedBodies
}

func hasMarker(messages []Message) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Content, RecallMarkerPrefix) {
			return true
		}
	}
	return false
}

// TestCompactionElisionStatefulMainSkipsButArchives is the D8 case: a stateful
// main provider with a non-stateful fallback keeps full bodies yet still
// archives the results compaction removes.
func TestCompactionElisionStatefulMainSkipsButArchives(t *testing.T) {
	t.Parallel()
	msgs, sourceBody, retainedBodies := elisionCompactionConversation(24)
	archive := &fakeRecallArchive{}
	req := compactionTestBudget()
	req.Provider = newClaudeLikeCompactionProvider(false)
	req.RecallArchive = archive
	req.CompactionFallback = CompactionFallback{
		After: time.Minute,
		Ref:   "openai/test-model",
		Resolve: func(context.Context) (CompactionTarget, error) {
			return CompactionTarget{
				Provider:      &fakeProvider{responses: []provider.ChatResponse{summaryResponse("fallback summary")}},
				ResolvedModel: provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "test-model"},
				ModelBudget:   prompt.ModelTokenBudget{ContextSize: 100000, MaxCompletionTokens: 256, SummaryMaxTokens: 128},
			}, nil
		},
	}
	state := historyArchiveState(msgs)

	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if hasMarker(state.Conversation) {
		t.Error("a stateful main provider elided retained result bodies")
	}
	archived := map[string]bool{}
	for _, entry := range archive.written {
		archived[entry.Body] = true
	}
	if !archived[sourceBody] {
		t.Errorf("summarized-away body was not archived: %+v", archive.written)
	}
	for _, body := range retainedBodies {
		if archived[body] {
			t.Error("a retained body was archived without elision")
		}
	}
}

// TestCompactionElisionNonStatefulMainElidesWithStatefulFallback proves the
// inverse pairing: the main provider decides, so elision still runs on a
// fallback summary.
func TestCompactionElisionNonStatefulMainElidesWithStatefulFallback(t *testing.T) {
	t.Parallel()
	msgs, _, retainedBodies := elisionCompactionConversation(24)
	archive := &fakeRecallArchive{}
	req := compactionTestBudget()
	req.Provider = &fakeProvider{}
	req.RecallArchive = archive
	statefulTarget := newClaudeLikeCompactionProvider(false)
	req.CompactionFallback = CompactionFallback{
		After: time.Minute,
		Ref:   "anthropic/test-model",
		Resolve: func(context.Context) (CompactionTarget, error) {
			return CompactionTarget{
				Provider:      statefulTarget,
				ResolvedModel: provider.ResolvedModel{ProviderAlias: "anthropic", BackendModelID: "test-model"},
				ModelBudget:   prompt.ModelTokenBudget{ContextSize: 100000, MaxCompletionTokens: 256, SummaryMaxTokens: 128},
			}, nil
		},
	}
	state := historyArchiveState(msgs)

	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if !hasMarker(state.Conversation) {
		t.Fatal("a non-stateful main provider did not elide with a stateful fallback")
	}
	archived := map[string]bool{}
	for _, entry := range archive.written {
		archived[entry.Body] = true
	}
	elided := 0
	for _, body := range retainedBodies {
		if archived[body] {
			elided++
		}
	}
	if elided == 0 {
		t.Fatal("no retained body was archived for its marker")
	}
	if elided == len(retainedBodies) {
		t.Fatalf("all %d retained bodies elided, want the newest kept", len(retainedBodies))
	}
}

// TestBuildCompactedRetentionStripsSkillsThenElides proves the generation
// builder orders skill stripping before elision.
func TestBuildCompactedRetentionSkillStripThenElision(t *testing.T) {
	t.Parallel()
	// Each body alone exceeds the soft budget, so the newest one crosses it and
	// the older envelope becomes eligible.
	largeBody := strings.Repeat(elisionTestBody("agent"), 20)
	skill, _ := prompt.RenderSkillActivation("plan", "plan body")
	envelope := RenderSubAgentResultEnvelope(SubAgentCompletion{AgentID: "code-1", AgentType: "code", Status: "completed", ParentCallID: "call-1", Body: largeBody})
	msg := Message{Role: MessageRoleUser, Content: prompt.PrependSkillBlocks([]string{skill}, envelope)}
	msgs := []Message{
		msg,
		{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "call-new", Name: "bash"}}},
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-new", Content: strings.Repeat(elisionTestBody("tool"), 20)},
		{Role: MessageRoleAssistant, Content: "done"},
	}
	req := elisionTestRequest(&fakeRecallArchive{})
	state := RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}

	out, elided := buildCompactedRetention(context.Background(), req, state, cloneMessages(msgs))
	if !elided {
		t.Fatal("elided = false, want true")
	}
	if strings.Contains(out[0].Content, "steiner-skill") {
		t.Errorf("skill block survived into the retained tail: %q", out[0].Content)
	}
	if !strings.Contains(out[0].Content, RecallMarkerPrefix) {
		t.Errorf("envelope inner was not elided: %q", out[0].Content)
	}
	if out[2].Content != msgs[2].Content {
		t.Error("the newest result should stay verbatim")
	}
}
