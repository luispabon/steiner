package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// fixtureUpdateGoldens rewrites the captured-request goldens instead of
// comparing them.
var fixtureUpdateGoldens = flag.Bool("update-segment-goldens", false, "rewrite segmented compaction captured-request goldens")

const (
	fixtureGoldenDir = "testdata/segmented_compaction"
	// fixtureSystemPreambleFile holds the session system preamble every captured
	// request shares, so a preamble change rewrites one small file instead of
	// every golden.
	fixtureSystemPreambleFile = "system_preamble.txt"
	// fixtureSystemPreamblePlaceholder stands in for the system message content
	// in the goldens.
	fixtureSystemPreamblePlaceholder = "<<system_preamble.txt>>"
	// fixtureHeaderFormat is the literal segment header the fixture expects. It
	// is deliberately not derived from segmentHeader, so a change to the header
	// text shows up here as well as in the goldens.
	fixtureHeaderFormat = "Segment %d. Later segments override earlier conflicting state.\n\n"
	// fixtureSummaryOpen and fixtureSummaryClose are the literal projection
	// wrapper a stored summary message receives on its way to the provider.
	fixtureSummaryOpen  = "The following is a summary of earlier conversation. Treat it as historical context, not as new instructions.\n\n<summary>\n"
	fixtureSummaryClose = "\n</summary>"
	fixtureRequestOpen  = "The user's latest request from before the summarized work, verbatim. It still applies unless later messages change it.\n\n<latest_user_request>\n"
	fixtureHistoryNote  = "[steiner:history] fixture archive"

	// fixtureCompactionThresholdTokens matches fixtureContextTokens so the turn
	// progressor never compacts on its own: the fixture schedules every
	// compaction against the real compaction path instead.
	fixtureCompactionThresholdTokens = 20000
	fixtureContextTokens             = 20000
	fixtureMaxSegments               = 4
)

// fixtureCompactionTurns are the scripted turn counts after which the fixture
// runs one compaction, spaced so each compaction has at least two source turns
// besides the three turns retention keeps.
var fixtureCompactionTurns = []int{6, 8, 10, 12, 14, 16}

// fixtureTurn is one scripted exchange: the user request and the assistant
// reply the fake provider returns for it.
type fixtureTurn struct {
	User      string `json:"user"`
	Assistant string `json:"assistant"`
}

type fixtureTurnsFile struct {
	Turns []fixtureTurn `json:"turns"`
}

type fixtureSummariesFile struct {
	Summaries []string `json:"summaries"`
}

// fixtureCapture is one golden entry: the request as sent, plus the message
// indices whose CacheBoundary flag is true. CacheBoundary is json:"-", so the
// indices are the only way to rebuild it from the golden.
type fixtureCapture struct {
	Request              provider.ChatRequest `json:"request"`
	CacheBoundaryIndices []int                `json:"cache_boundary_indices"`
}

// fixtureDecision is one parsed segment_decision log line, reduced to the
// fields the fixture asserts.
type fixtureDecision struct {
	Action   string
	Reason   string
	Sequence int
}

// fixtureSegment is one expected immutable segment: its chain sequence and the
// canned summary text the fixture says it covers.
type fixtureSegment struct {
	Sequence int
	Summary  string
}

// fixtureResult is everything one scripted mode produced.
type fixtureResult struct {
	Requests     []provider.ChatRequest
	Goldens      []fixtureCapture
	Decisions    []fixtureDecision
	MainCalls    int
	SummaryCalls int
	State        RunState
}

// fixtureScript drives the fake provider: compaction calls consume the next
// canned summary, main calls consume the next scripted turn.
type fixtureScript struct {
	turns        []fixtureTurn
	summaries    []string
	nextTurn     int
	nextSummary  int
	summaryCalls int
}

func (s *fixtureScript) chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if req.CompactionSummary {
		s.summaryCalls++
		if s.nextSummary >= len(s.summaries) {
			return provider.ChatResponse{}, fmt.Errorf("fixture: no canned summary for compaction call %d", s.summaryCalls)
		}
		text := s.summaries[s.nextSummary]
		s.nextSummary++
		return provider.ChatResponse{
			Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: text},
			FinishReason: "stop",
		}, nil
	}
	if s.nextTurn >= len(s.turns) {
		return provider.ChatResponse{}, fmt.Errorf("fixture: no scripted turn for main call %d", s.nextTurn+1)
	}
	turn := s.turns[s.nextTurn]
	s.nextTurn++
	return provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: turn.Assistant},
		FinishReason: "stop",
	}, nil
}

// fixtureHistoryArchive hands out one fixed note and counts adopted appends, so
// the tail always carries a history note without touching the filesystem.
type fixtureHistoryArchive struct {
	appends int
}

func (a *fixtureHistoryArchive) Note([]Message) (string, error) { return fixtureHistoryNote, nil }

func (a *fixtureHistoryArchive) Append(context.Context, []Message) error {
	a.appends++
	return nil
}

// fixturePolicy is the segmented compaction policy the fixture exercises. A
// disabled policy is the zero value; the enabled one caps the chain at four
// segments with the production chain and post-compaction defaults, so only
// max-segments rebases.
func fixturePolicy(enabled bool) config.SegmentedCompactionPolicy {
	if !enabled {
		return config.SegmentedCompactionPolicy{}
	}
	return config.SegmentedCompactionPolicy{
		Enabled:                  true,
		MaxSegments:              fixtureMaxSegments,
		MaxChainPercent:          12.5,
		MaxPostCompactionPercent: 50,
	}
}

// fixtureExpectedDecisions is the append-or-rebase sequence the scripted
// conversation must produce with the flag on: a chain-start rebase, three
// appends, a max-segments rebase, then one more append.
func fixtureExpectedDecisions() []fixtureDecision {
	return []fixtureDecision{
		{Action: "rebase", Reason: segmentReasonChainStart, Sequence: 1},
		{Action: "append", Reason: segmentReasonAppend, Sequence: 2},
		{Action: "append", Reason: segmentReasonAppend, Sequence: 3},
		{Action: "append", Reason: segmentReasonAppend, Sequence: 4},
		{Action: "rebase", Reason: segmentReasonMaxSegments, Sequence: 1},
		{Action: "append", Reason: segmentReasonAppend, Sequence: 2},
	}
}

// fixturePlainSummaries derives the flag-off summarizer response of every
// compaction: equal information content in both modes, so the comparison
// isolates cache layout; real rewrite summaries may compress more. Each
// response is the blank-line joined concatenation of the on-mode segment
// bodies in the chain after that compaction, so a rebase starts a new chain.
func fixturePlainSummaries(decisions []fixtureDecision, summaries []string) []string {
	out := make([]string, len(decisions))
	var chain []string
	for i, decision := range decisions {
		if decision.Action == "rebase" {
			chain = []string{summaries[i]}
		} else {
			chain = append(chain, summaries[i])
		}
		out[i] = strings.Join(chain, "\n\n")
	}
	return out
}

func fixtureReadJSON(t *testing.T, name string, into any) {
	t.Helper()
	path := filepath.Join(fixtureGoldenDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
}

// fixtureRunConversation drives the scripted turns through the real turn
// progressor, with the real compaction path, and captures every provider
// request in order.
func fixtureRunConversation(t *testing.T, policy config.SegmentedCompactionPolicy) fixtureResult {
	t.Helper()
	var turns fixtureTurnsFile
	fixtureReadJSON(t, "turns.json", &turns)
	var summaries fixtureSummariesFile
	fixtureReadJSON(t, "summaries.json", &summaries)
	if len(turns.Turns) == 0 || len(summaries.Summaries) == 0 {
		t.Fatal("fixture files must be non-empty")
	}

	summaryTexts := summaries.Summaries
	if !policy.Enabled {
		summaryTexts = fixturePlainSummaries(fixtureExpectedDecisions(), summaries.Summaries)
	}
	script := &fixtureScript{turns: turns.Turns, summaries: summaryTexts}
	providerStub := &fakeProvider{chatFn: script.chat}
	archive := &fixtureHistoryArchive{}
	logPath := filepath.Join(t.TempDir(), "compaction.log")

	req := RunRequest{
		Provider:      providerStub,
		ResolvedModel: provider.ResolvedModel{BackendModelID: "fixture-model"},
		Prompt: prompt.AssemblyOptions{
			SkipAgents:         true,
			SkipProjectContext: true,
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               fixtureContextTokens,
			MaxCompletionTokens:       128,
			CompactionThresholdTokens: fixtureCompactionThresholdTokens,
			SummaryMaxTokens:          32,
			NormalSummaryMaxTokens:    32,
			EmergencySummaryMaxTokens: 16,
		},
		MaxTokens:           intPtr(64),
		SegmentedCompaction: policy,
		CompactionLogPath:   logPath,
		HistoryArchive:      archive,
		Events:              output.NoopSink{},
		ContextManager:      NewContextStateManager(),
	}
	// Main turns and summarizer calls both carry the session system preamble
	// prepareBasePrompt renders, so they share the same static prefix.
	base := prepareBasePrompt(req)
	progressor := newTurnProgressor(req, base, (&Runner{}).compactConversationForBudget)

	ctx := context.Background()
	state := RunState{Lineage: newConversationLineage(nil)}
	skipped := map[string]bool{}
	compactionCount := new(int)
	compactionSchedule := map[int]bool{}
	for _, turn := range fixtureCompactionTurns {
		compactionSchedule[turn] = true
	}
	for i, turn := range turns.Turns {
		user := Message{Role: MessageRoleUser, Content: turn.User}
		state.Lineage = state.Lineage.WithAppendedMessages([]Message{user})
		state.Conversation = state.Lineage.FullMessages()
		for attempt := 0; ; attempt++ {
			if attempt > 5 {
				t.Fatalf("turn %d: turn progressor kept retrying after a compaction", i+1)
			}
			out := progressor.advance(ctx, state)
			if out.Error != nil {
				t.Fatalf("turn %d: advance error: %v", i+1, out.Error)
			}
			state = out.State
			if out.Retry {
				continue
			}
			break
		}
		last := state.Conversation[len(state.Conversation)-1]
		if last.Role != MessageRoleAssistant {
			t.Fatalf("turn %d: conversation ends with %q, want the scripted assistant reply", i+1, last.Role)
		}
		if !compactionSchedule[i+1] {
			continue
		}
		// Run one compaction on the real compaction path, the way the turn
		// progressor does when its budget check asks for one.
		applied, attempted, reason, err := (&Runner{}).compactConversationForBudgetWithSteering(ctx, req, &state, state.TurnCount, nil, skipped, compactionCount, "")
		if err != nil {
			t.Fatalf("turn %d: compaction error: %v", i+1, err)
		}
		if !attempted || !applied {
			t.Fatalf("turn %d: compaction applied=%t attempted=%t reason=%q, want an applied compaction", i+1, applied, attempted, reason)
		}
	}
	if *compactionCount != len(fixtureCompactionTurns) {
		t.Fatalf("compaction stages = %d, want %d", *compactionCount, len(fixtureCompactionTurns))
	}

	result := fixtureResult{Requests: providerStub.requests, State: state}
	for _, request := range result.Requests {
		if request.CompactionSummary {
			result.SummaryCalls++
			continue
		}
		result.MainCalls++
	}
	for _, line := range segmentDecisionLines(t, logPath) {
		result.Decisions = append(result.Decisions, fixtureParseDecision(t, line))
	}
	result.Goldens = make([]fixtureCapture, len(result.Requests))
	for i, request := range result.Requests {
		indices := cacheBoundaryIndexes(request.Messages)
		if indices == nil {
			indices = []int{}
		}
		result.Goldens[i] = fixtureCapture{Request: request, CacheBoundaryIndices: indices}
	}
	return result
}

// fixtureParseDecision reduces one segment_decision line to the asserted fields.
func fixtureParseDecision(t *testing.T, line string) fixtureDecision {
	t.Helper()
	d := fixtureDecision{}
	for _, field := range strings.Fields(line) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "action":
			d.Action = value
		case "reason":
			d.Reason = value
		case "sequence":
			if _, err := fmt.Sscanf(value, "%d", &d.Sequence); err != nil {
				t.Fatalf("parse sequence from %q: %v", line, err)
			}
		}
	}
	if d.Action == "" || d.Reason == "" {
		t.Fatalf("segment decision line is missing action or reason: %q", line)
	}
	return d
}

// fixtureGoldenCaptures replaces the session system preamble, which every
// captured request shares, with fixtureSystemPreamblePlaceholder so the goldens
// store it once. It returns the substituted captures and the preamble itself.
func fixtureGoldenCaptures(t *testing.T, captures []fixtureCapture) ([]fixtureCapture, string) {
	t.Helper()
	if len(captures) == 0 || len(captures[0].Request.Messages) == 0 {
		t.Fatal("no captured requests to store")
	}
	preamble := captures[0].Request.Messages[0].Content
	if captures[0].Request.Messages[0].Role != provider.MessageRoleSystem {
		t.Fatal("first captured request does not start with the system preamble")
	}
	out := make([]fixtureCapture, len(captures))
	for i, entry := range captures {
		messages := make([]provider.Message, len(entry.Request.Messages))
		copy(messages, entry.Request.Messages)
		replaced := false
		for j := range messages {
			if messages[j].Role != provider.MessageRoleSystem {
				continue
			}
			if messages[j].Content != preamble {
				t.Fatalf("entry %d: system message %d differs from the first captured system preamble\nregenerate with:\n  go test ./internal/agent -run SegmentsFixture -update-segment-goldens", i, j)
			}
			messages[j].Content = fixtureSystemPreamblePlaceholder
			replaced = true
		}
		if !replaced {
			t.Fatalf("entry %d: request carries no system preamble", i)
		}
		entry.Request.Messages = messages
		out[i] = entry
	}
	return out, preamble
}

// fixtureEncodeGoldens renders the captures with stable formatting.
func fixtureEncodeGoldens(t *testing.T, captures []fixtureCapture) []byte {
	t.Helper()
	if captures == nil {
		captures = []fixtureCapture{}
	}
	data, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatalf("marshal goldens: %v", err)
	}
	return append(data, '\n')
}

// fixtureWrittenPreamble holds the system preamble the first update-mode mode
// wrote, so a later mode cannot silently overwrite it with different text.
var fixtureWrittenPreamble string

// fixtureCheckPreambleStable reports the update-mode conflict where a later mode
// renders a system preamble that differs from the one already written. The two
// modes share one preamble file, so the last writer would otherwise win.
func fixtureCheckPreambleStable(written, preamble string) error {
	if written != "" && written != preamble {
		return errors.New("system preamble differs from the one an earlier mode wrote")
	}
	return nil
}

// fixtureCheckGolden compares encoded with the stored golden and its shared
// system preamble, or rewrites both when -update-segment-goldens is set.
func fixtureCheckGolden(t *testing.T, mode, preamble string, encoded []byte) {
	t.Helper()
	preamblePath := filepath.Join(fixtureGoldenDir, fixtureSystemPreambleFile)
	name := "captured_" + mode + ".json"
	path := filepath.Join(fixtureGoldenDir, name)
	if *fixtureUpdateGoldens {
		if err := fixtureCheckPreambleStable(fixtureWrittenPreamble, preamble); err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
		fixtureWrittenPreamble = preamble
		if err := os.MkdirAll(fixtureGoldenDir, 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(preamblePath, []byte(preamble), 0o644); err != nil {
			t.Fatalf("write system preamble %s: %v", preamblePath, err)
		}
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
	}
	storedPreamble, err := os.ReadFile(preamblePath)
	if err != nil {
		t.Fatalf("read system preamble %s: %v\nregenerate with:\n  go test ./internal/agent -run SegmentsFixture -update-segment-goldens", preamblePath, err)
	}
	if string(storedPreamble) != preamble {
		t.Fatalf("live system preamble does not match %s\nregenerate with:\n  go test ./internal/agent -run SegmentsFixture -update-segment-goldens", preamblePath)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\nregenerate with:\n  go test ./internal/agent -run SegmentsFixture -update-segment-goldens", path, err)
	}
	if !bytes.Equal(stored, encoded) {
		t.Fatalf("captured requests drifted from %s\nregenerate with:\n  go test ./internal/agent -run SegmentsFixture -update-segment-goldens", path)
	}
}

// TestFixtureCheckPreambleStable pins the guard that keeps a later update mode
// from overwriting the shared system preamble with different text.
func TestFixtureCheckPreambleStable(t *testing.T) {
	tests := []struct {
		name     string
		written  string
		preamble string
		wantErr  bool
	}{
		{name: "first write", preamble: "preamble a"},
		{name: "same preamble", written: "preamble a", preamble: "preamble a"},
		{name: "different preamble", written: "preamble a", preamble: "preamble b", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fixtureCheckPreambleStable(tt.written, tt.preamble)
			if got := err != nil; got != tt.wantErr {
				t.Fatalf("fixtureCheckPreambleStable(%q, %q) error = %v, want error %t", tt.written, tt.preamble, err, tt.wantErr)
			}
		})
	}
}

// fixtureAssertRoundTrip proves a golden entry rebuilds the exact request that
// was sent, including the CacheBoundary flags restored from the indices and the
// shared system preamble expanded from its placeholder.
func fixtureAssertRoundTrip(t *testing.T, preamble string, encoded []byte, sent []provider.ChatRequest) {
	t.Helper()
	var decoded []fixtureCapture
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode goldens: %v", err)
	}
	if len(decoded) != len(sent) {
		t.Fatalf("golden entries = %d, want %d", len(decoded), len(sent))
	}
	reencoded, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		t.Fatalf("re-marshal goldens: %v", err)
	}
	reencoded = append(reencoded, '\n')
	if !bytes.Equal(reencoded, encoded) {
		t.Fatalf("golden bytes do not round-trip through fixtureCapture")
	}
	for i, entry := range decoded {
		requestJSON, err := json.Marshal(entry.Request)
		if err != nil {
			t.Fatalf("entry %d: marshal request: %v", i, err)
		}
		var rebuilt provider.ChatRequest
		if err := json.Unmarshal(requestJSON, &rebuilt); err != nil {
			t.Fatalf("entry %d: decode request: %v", i, err)
		}
		again, err := json.Marshal(rebuilt)
		if err != nil {
			t.Fatalf("entry %d: re-marshal request: %v", i, err)
		}
		if !bytes.Equal(again, requestJSON) {
			t.Fatalf("entry %d: request does not round-trip losslessly:\n got %s\nwant %s", i, again, requestJSON)
		}
		for _, index := range entry.CacheBoundaryIndices {
			if index < 0 || index >= len(rebuilt.Messages) {
				t.Fatalf("entry %d: cache boundary index %d is out of range", i, index)
			}
			rebuilt.Messages[index].CacheBoundary = true
		}
		for j := range rebuilt.Messages {
			if rebuilt.Messages[j].Content == fixtureSystemPreamblePlaceholder {
				rebuilt.Messages[j].Content = preamble
			}
		}
		// CacheBoundary never marshals, so the rebuilt struct and the sent
		// request agree on every field of the wire payload. Local-only fields
		// such as RawPromptTokenEstimate are per-send estimates and are outside
		// the golden's contract.
		if got, want := fixtureJSON(t, rebuilt), fixtureJSON(t, sent[i]); !bytes.Equal(got, want) {
			t.Fatalf("entry %d: golden does not rebuild the request that was sent:\n got %s\nwant %s", i, got, want)
		}
		if got, want := cacheBoundaryIndexes(rebuilt.Messages), entry.CacheBoundaryIndices; !slices.Equal(got, want) {
			t.Fatalf("entry %d: restored cache boundaries = %v, want %v", i, got, want)
		}
	}
}

// fixtureConversationStart returns the index of the first conversation message:
// the single system preamble precedes it.
func fixtureConversationStart(t *testing.T, request provider.ChatRequest) int {
	t.Helper()
	if len(request.Messages) == 0 || request.Messages[0].Role != provider.MessageRoleSystem {
		t.Fatalf("request does not start with the system preamble")
	}
	return 1
}

// fixtureLeadingSegmentCount counts the leading conversation messages that carry
// a projected segment header for sequence 1, 2, 3, and so on.
func fixtureLeadingSegmentCount(t *testing.T, request provider.ChatRequest) int {
	t.Helper()
	start := fixtureConversationStart(t, request)
	count := 0
	for {
		index := start + count
		if index >= len(request.Messages) {
			break
		}
		if !strings.Contains(request.Messages[index].Content, fmt.Sprintf(fixtureHeaderFormat, count+1)) {
			break
		}
		count++
	}
	return count
}

// fixtureExpectedSegment is the literal provider projection of one immutable
// segment built from fixture data alone.
func fixtureExpectedSegment(summary string, sequence int) provider.Message {
	return provider.Message{
		Role:    provider.MessageRoleUser,
		Content: fixtureSummaryOpen + fmt.Sprintf(fixtureHeaderFormat, sequence) + summary + fixtureSummaryClose,
	}
}

// fixtureExpectedChain returns the segments in effect after applied compactions,
// derived from the expected decision sequence and the canned summaries.
func fixtureExpectedChain(decisions []fixtureDecision, summaries []string, applied int) []fixtureSegment {
	var chain []fixtureSegment
	for i := 0; i < applied && i < len(decisions); i++ {
		decision := decisions[i]
		if decision.Action == "rebase" {
			chain = []fixtureSegment{{Sequence: decision.Sequence, Summary: summaries[i]}}
			continue
		}
		chain = append(chain, fixtureSegment{Sequence: decision.Sequence, Summary: summaries[i]})
	}
	return chain
}

func fixtureJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	return data
}

// fixtureAssertSegmented checks the flag-on invariants: decision sequence,
// segment byte identity, cache boundary placement, rebase shape, and tail
// ownership of the history note and request block.
func fixtureAssertSegmented(t *testing.T, result fixtureResult) {
	t.Helper()
	expected := fixtureExpectedDecisions()
	if len(result.Decisions) != len(expected) {
		t.Fatalf("segment decisions = %v, want %v", result.Decisions, expected)
	}
	for i := range expected {
		if result.Decisions[i] != expected[i] {
			t.Fatalf("segment decision %d = %+v, want %+v (all: %v)", i+1, result.Decisions[i], expected[i], result.Decisions)
		}
	}
	if result.SummaryCalls != len(expected) {
		t.Fatalf("summary calls = %d, want one per compaction (%d)", result.SummaryCalls, len(expected))
	}
	if result.MainCalls < len(expected) {
		t.Fatalf("main calls = %d, want at least one per compaction", result.MainCalls)
	}

	var summaries fixtureSummariesFile
	fixtureReadJSON(t, "summaries.json", &summaries)

	// segmentBytes remembers the first sent bytes of each segment in each chain
	// episode, so a later request that changes an older segment fails here.
	segmentBytes := map[string][]byte{}
	for i, request := range result.Requests {
		if request.CompactionSummary {
			continue
		}
		applied := fixtureAppliedBefore(result.Requests, i)
		chain := fixtureExpectedChain(expected, summaries.Summaries, applied)
		start := fixtureConversationStart(t, request)
		if got := fixtureLeadingSegmentCount(t, request); got != len(chain) {
			t.Fatalf("main request %d: leading segments = %d, want %d after %d compactions", i, got, len(chain), applied)
		}
		for offset, segment := range chain {
			actual := request.Messages[start+offset]
			want := fixtureExpectedSegment(segment.Summary, segment.Sequence)
			if got, wantBytes := fixtureJSON(t, actual), fixtureJSON(t, want); !bytes.Equal(got, wantBytes) {
				t.Fatalf("main request %d segment %d bytes:\n got %s\nwant %s", i, segment.Sequence, got, wantBytes)
			}
			key := fmt.Sprintf("ep%d/seq%d", fixtureAppliedEpisode(expected, applied), segment.Sequence)
			if first, ok := segmentBytes[key]; ok {
				if !bytes.Equal(first, fixtureJSON(t, actual)) {
					t.Fatalf("main request %d segment %s changed since it was first sent", i, key)
				}
			} else {
				segmentBytes[key] = fixtureJSON(t, actual)
			}
			if strings.Contains(actual.Content, "[steiner:history]") {
				t.Fatalf("main request %d segment %d carries a history note: %q", i, segment.Sequence, actual.Content)
			}
			if strings.Contains(actual.Content, "<latest_user_request>") {
				t.Fatalf("main request %d segment %d carries a latest-request block: %q", i, segment.Sequence, actual.Content)
			}
		}
		boundaries := cacheBoundaryIndexes(request.Messages)
		if len(chain) == 0 {
			if len(boundaries) != 0 {
				t.Fatalf("main request %d cache boundaries = %v, want none before the first compaction", i, boundaries)
			}
		} else {
			want := start + len(chain) - 1
			if len(boundaries) != 1 || boundaries[0] != want {
				t.Fatalf("main request %d cache boundaries = %v, want exactly [%d] (last of %d segments)", i, boundaries, want, len(chain))
			}
			tail := request.Messages[start+len(chain)]
			if !strings.Contains(tail.Content, fixtureHistoryNote) {
				t.Fatalf("main request %d tail lacks the history note: %q", i, tail.Content)
			}
			if !strings.Contains(tail.Content, fixtureRequestOpen) {
				t.Fatalf("main request %d tail lacks the latest-request block: %q", i, tail.Content)
			}
		}
	}

	// After the max-segments rebase the prefix is one segment plus the tail.
	rebaseAt := 4 // index of the max-segments decision in the expected sequence
	afterRebase := fixtureFirstMainAfter(result.Requests, fixtureSummaryCallsBefore(result.Requests, rebaseAt+1))
	if afterRebase < 0 {
		t.Fatal("no main request after the max-segments rebase")
	}
	request := result.Requests[afterRebase]
	start := fixtureConversationStart(t, request)
	if got := fixtureLeadingSegmentCount(t, request); got != 1 {
		t.Fatalf("main request after rebase has %d leading segments, want 1", got)
	}
	if got := request.Messages[start].Content; !strings.Contains(got, fmt.Sprintf(fixtureHeaderFormat, 1)) || !strings.Contains(got, summaries.Summaries[rebaseAt]) {
		t.Fatalf("main request after rebase segment 1 = %q, want the rebased summary", got)
	}
	if next := request.Messages[start+1]; next.Role != provider.MessageRoleUser {
		t.Fatalf("main request after rebase: message after segment 1 is %q", next.Content)
	}
	if !strings.Contains(request.Messages[start+1].Content, fixtureHistoryNote) {
		t.Fatalf("main request after rebase: tail after segment 1 lacks the history note")
	}

	final := result.Requests[len(result.Requests)-1]
	if final.CompactionSummary {
		t.Fatal("last captured request is a summary call, want a main turn")
	}
	var joined strings.Builder
	for _, message := range final.Messages {
		joined.WriteString(message.Content)
		joined.WriteString("\n")
	}
	for _, want := range []string{"error E-4471", "error E-4472", "ruled out: sqlite", "## Corrections"} {
		if !strings.Contains(joined.String(), want) {
			t.Fatalf("final main request is missing %q", want)
		}
	}
}

// fixtureAssertPlain checks the flag-off invariants: one summary message per
// generation, no segment metadata, no cache boundary, and no decisions logged.
func fixtureAssertPlain(t *testing.T, result fixtureResult) {
	t.Helper()
	if len(result.Decisions) != 0 {
		t.Fatalf("flag-off segment decisions = %v, want none", result.Decisions)
	}
	if result.SummaryCalls < 6 {
		t.Fatalf("flag-off compactions = %d, want at least 6", result.SummaryCalls)
	}
	for i, request := range result.Requests {
		if boundaries := cacheBoundaryIndexes(request.Messages); len(boundaries) != 0 {
			t.Fatalf("flag-off request %d cache boundaries = %v, want none", i, boundaries)
		}
	}

	summary := result.State.Lineage.FullMessages()
	leading := 0
	for leading < len(summary) && summary[leading].Role == MessageRoleSummary {
		if summary[leading].Segment != nil {
			t.Fatalf("flag-off summary message carries segment metadata: %+v", summary[leading].Segment)
		}
		leading++
	}
	if leading != 1 {
		t.Fatalf("flag-off final conversation has %d summary messages, want exactly 1", leading)
	}
	for _, message := range summary {
		if message.Segment != nil {
			t.Fatalf("flag-off conversation carries segment metadata: %+v", message.Segment)
		}
	}

	var summaries fixtureSummariesFile
	fixtureReadJSON(t, "summaries.json", &summaries)
	plain := fixturePlainSummaries(fixtureExpectedDecisions(), summaries.Summaries)
	final := result.State.Lineage.FullMessages()[0]
	want := plain[result.SummaryCalls-1]
	if !strings.Contains(final.Content, want) {
		t.Fatalf("flag-off final summary = %q, want the concatenated segment bodies %q", final.Content, want)
	}

	// Each main request after the first compaction carries exactly one leading
	// summary message.
	applied := 0
	for i, request := range result.Requests {
		if request.CompactionSummary {
			continue
		}
		applied = fixtureAppliedBefore(result.Requests, i)
		start := fixtureConversationStart(t, request)
		count := 0
		for start+count < len(request.Messages) && strings.HasPrefix(request.Messages[start+count].Content, fixtureSummaryOpen) {
			count++
		}
		want := 0
		if applied > 0 {
			want = 1
		}
		if count != want {
			t.Fatalf("flag-off main request %d leading summaries = %d, want %d after %d compactions", i, count, want, applied)
		}
	}
}

// fixtureAppliedBefore counts the summary calls captured before request index.
func fixtureAppliedBefore(requests []provider.ChatRequest, index int) int {
	applied := 0
	for i := 0; i < index; i++ {
		if requests[i].CompactionSummary {
			applied++
		}
	}
	return applied
}

// fixtureSummaryCallsBefore returns the index of the first request that follows
// count summary calls.
func fixtureSummaryCallsBefore(requests []provider.ChatRequest, count int) int {
	seen := 0
	for i, request := range requests {
		if seen >= count {
			return i
		}
		if request.CompactionSummary {
			seen++
		}
	}
	return -1
}

// fixtureFirstMainAfter returns the first main request index at or after start.
func fixtureFirstMainAfter(requests []provider.ChatRequest, start int) int {
	if start < 0 {
		return -1
	}
	for i := start; i < len(requests); i++ {
		if !requests[i].CompactionSummary {
			return i
		}
	}
	return -1
}

// fixtureAppliedEpisode numbers the chain the given applied count belongs to:
// each rebase starts a new episode.
func fixtureAppliedEpisode(decisions []fixtureDecision, applied int) int {
	episode := 0
	for i := 0; i < applied && i < len(decisions); i++ {
		if decisions[i].Action == "rebase" {
			episode++
		}
	}
	return episode
}

// TestSegmentsFixture runs the scripted conversation through the real turn and
// compaction flow once per mode and pins the captured requests.
func TestSegmentsFixture(t *testing.T) {
	// Each run starts with no recorded preamble, so a repeated test process does
	// not compare against a preamble from a previous run.
	fixtureWrittenPreamble = ""
	t.Run("Off", func(t *testing.T) {
		result := fixtureRunConversation(t, fixturePolicy(false))
		captures, preamble := fixtureGoldenCaptures(t, result.Goldens)
		encoded := fixtureEncodeGoldens(t, captures)
		fixtureCheckGolden(t, "off", preamble, encoded)
		fixtureAssertRoundTrip(t, preamble, encoded, result.Requests)
		fixtureAssertPlain(t, result)
	})
	t.Run("On", func(t *testing.T) {
		result := fixtureRunConversation(t, fixturePolicy(true))
		captures, preamble := fixtureGoldenCaptures(t, result.Goldens)
		encoded := fixtureEncodeGoldens(t, captures)
		fixtureCheckGolden(t, "on", preamble, encoded)
		fixtureAssertRoundTrip(t, preamble, encoded, result.Requests)
		fixtureAssertSegmented(t, result)
	})
}

// segmentCompactionMessages builds two immutable segments, a mutable tail with
// note and request block, a two-message append span and one retained turn.
func segmentCompactionMessages() []Message {
	return []Message{
		{Role: MessageRoleSummary, Content: segmentHeader(1) + "first facts", Segment: testSegment(1, 2)},
		{Role: MessageRoleSummary, Content: segmentHeader(2) + "second facts", Segment: testSegment(2, 3)},
		{Role: MessageRoleSummary, Content: withRequestBlock("[steiner:history] archived to sessions/old.jsonl", "old request")},
		{Role: MessageRoleUser, Content: "span request"},
		{Role: MessageRoleAssistant, Content: "span reply"},
		{Role: MessageRoleUser, Content: "current request"},
	}
}

// mustJSON marshals a message slice for a byte-identity comparison.
func mustJSON(t *testing.T, messages []Message) []byte {
	t.Helper()
	encoded, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	return encoded
}

// segmentStageConversation builds the in-run shape of a segmented conversation:
// two immutable segments, a mutable tail, a two-message append span and three
// retained turns. Retention keeps the last three turns, so the append span is
// FullMessages()[3:5].
func segmentStageConversation() []Message {
	return []Message{
		{Role: MessageRoleSummary, Content: segmentHeader(1) + "first facts", Segment: testSegment(1, 2)},
		{Role: MessageRoleSummary, Content: segmentHeader(2) + "second facts", Segment: testSegment(2, 3)},
		{Role: MessageRoleSummary, Content: withRequestBlock("[steiner:history] archived to sessions/old.jsonl", "old request")},
		{Role: MessageRoleUser, Content: "span request"},
		{Role: MessageRoleAssistant, Content: "span reply"},
		{Role: MessageRoleUser, Content: "recent one"},
		{Role: MessageRoleAssistant, Content: "recent one reply"},
		{Role: MessageRoleUser, Content: "recent two"},
		{Role: MessageRoleAssistant, Content: "recent two reply"},
		{Role: MessageRoleUser, Content: "recent three"},
		{Role: MessageRoleAssistant, Content: "recent three reply"},
	}
}

// segmentedCompactionPolicy is the default experiment policy, enabled.
func segmentedCompactionPolicy() config.SegmentedCompactionPolicy {
	policy := config.SegmentedCompactionPolicy{MaxSegments: 8, MaxChainPercent: 12.5, MaxPostCompactionPercent: 50}
	policy.Enabled = true
	return policy
}

// segmentedCompactionRequest builds a compaction request for the fixture
// conversations: a large window so only the segment thresholds can rebase.
func segmentedCompactionRequest(p provider.Provider, policy config.SegmentedCompactionPolicy) RunRequest {
	return RunRequest{
		Provider:            p,
		ResolvedModel:       provider.ResolvedModel{BackendModelID: "test-model"},
		SegmentedCompaction: policy,
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               100000,
			MaxCompletionTokens:       256,
			SafetyMarginTokens:        0,
			SummaryMaxTokens:          128,
			NormalSummaryMaxTokens:    128,
			EmergencySummaryMaxTokens: 64,
		},
	}
}

// oneSummaryProvider returns a fake provider that answers each summary call
// with the next scripted text.
func oneSummaryProvider(texts ...string) *fakeProvider {
	responses := make([]provider.ChatResponse, 0, len(texts))
	for _, text := range texts {
		responses = append(responses, provider.ChatResponse{
			Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: text},
			FinishReason: "stop",
		})
	}
	return &fakeProvider{responses: responses}
}

func segmentDecisionLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read compaction log: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "segment_decision ") {
			lines = append(lines, line)
		}
	}
	return lines
}
