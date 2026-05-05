package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
)

func TestNaiveContextManagerPostIngestion(t *testing.T) {
	tests := []struct {
		name  string
		state RunState
	}{
		{
			name:  "empty state passes through unchanged",
			state: RunState{},
		},
		{
			name: "state with conversation passes through unchanged",
			state: RunState{
				Conversation: []Message{
					{Role: MessageRoleUser, Content: "hello"},
				},
				TurnCount: 3,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &NaiveContextManager{}
			got, err := m.PostIngestion(context.Background(), tc.state)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.TurnCount != tc.state.TurnCount {
				t.Errorf("TurnCount: got %d, want %d", got.TurnCount, tc.state.TurnCount)
			}
			if len(got.Conversation) != len(tc.state.Conversation) {
				t.Errorf("Conversation len: got %d, want %d", len(got.Conversation), len(tc.state.Conversation))
			}
		})
	}
}

func TestNaiveContextManagerPreAssembly(t *testing.T) {
	tests := []struct {
		name  string
		state RunState
	}{
		{
			name:  "empty state passes through unchanged",
			state: RunState{},
		},
		{
			name: "state with context passes through unchanged",
			state: RunState{
				TurnCount:  5,
				TokenCount: 1000,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &NaiveContextManager{}
			got, err := m.PreAssembly(context.Background(), tc.state)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.TurnCount != tc.state.TurnCount {
				t.Errorf("TurnCount: got %d, want %d", got.TurnCount, tc.state.TurnCount)
			}
			if got.TokenCount != tc.state.TokenCount {
				t.Errorf("TokenCount: got %d, want %d", got.TokenCount, tc.state.TokenCount)
			}
		})
	}
}

func TestPostIngestionNaiveContextManagerKeepsToolOutput(t *testing.T) {
	state := RunState{
		TurnCount: 2,
		Conversation: []Message{
			{
				Role: MessageRoleTool,
				Name: "bash",
				Content: mustJSON(t, map[string]any{
					"exit_code": 1,
					"output":    "warning: keep\nwarning: keep\nfinal",
				}),
			},
		},
	}

	got, err := (&NaiveContextManager{}).PostIngestion(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Conversation[0].Content != state.Conversation[0].Content {
		t.Fatalf("Conversation[0].Content = %q, want unchanged", got.Conversation[0].Content)
	}
	if got.TurnCount != state.TurnCount {
		t.Fatalf("TurnCount = %d, want %d", got.TurnCount, state.TurnCount)
	}
}

func TestPostIngestionSmartContextManagerTransformsToolOutput(t *testing.T) {
	bashContent := mustJSON(t, bashOutputForIngestionTest())
	grepContent := mustJSON(t, grepOutputForIngestionTest())
	readContent := mustJSON(t, map[string]any{
		"path":        "README.md",
		"start_line":  1,
		"end_line":    3,
		"total_lines": 3,
		"output":      "alpha\n\nbeta\n",
	})

	state := RunState{
		TurnCount: 4,
		Conversation: []Message{
			{Role: MessageRoleTool, Name: "bash", Content: bashContent},
			{Role: MessageRoleTool, Name: "grep", Content: grepContent},
			{Role: MessageRoleTool, Name: "read", Content: readContent},
		},
	}
	state.Lineage = newConversationLineage(state.Conversation)

	got, err := (&SmartContextManager{}).PostIngestion(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TurnCount != state.TurnCount {
		t.Fatalf("TurnCount = %d, want %d", got.TurnCount, state.TurnCount)
	}
	if len(got.Conversation) != len(state.Conversation) {
		t.Fatalf("Conversation len = %d, want %d", len(got.Conversation), len(state.Conversation))
	}

	var bashResult struct {
		Output    string `json:"output"`
		Message   string `json:"message"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(got.Conversation[0].Content), &bashResult); err != nil {
		t.Fatalf("unmarshal bash result: %v", err)
	}
	if !bashResult.Truncated {
		t.Fatal("bash truncated = false, want true")
	}
	if !strings.Contains(bashResult.Output, "final tail") {
		t.Fatalf("bash output = %q, want tail content", bashResult.Output)
	}
	if strings.Contains(bashResult.Output, "\x1b[") {
		t.Fatalf("bash output = %q, want ANSI stripped", bashResult.Output)
	}
	if !strings.Contains(bashResult.Output, "warning: retry (repeated 3x)") {
		t.Fatalf("bash output = %q, want repeated warning collapse", bashResult.Output)
	}
	if !strings.Contains(bashResult.Message, "<truncated output shown=") {
		t.Fatalf("bash message = %q, want truncation marker", bashResult.Message)
	}

	var grepResult struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(got.Conversation[1].Content), &grepResult); err != nil {
		t.Fatalf("unmarshal grep result: %v", err)
	}
	if !strings.Contains(grepResult.Output, "info: retrying (repeated 200x)") {
		t.Fatalf("grep output = %q, want collapsed info lines", grepResult.Output)
	}
	if !strings.Contains(grepResult.Output, "<truncated output shown=") {
		t.Fatalf("grep output = %q, want truncation marker", grepResult.Output)
	}

	if got.Conversation[2].Content != state.Conversation[2].Content {
		t.Fatalf("read content = %q, want unchanged", got.Conversation[2].Content)
	}
	if len(got.Lineage.FullMessages()) != len(got.Conversation) {
		t.Fatalf("lineage len = %d, want %d", len(got.Lineage.FullMessages()), len(got.Conversation))
	}
	if got.Lineage.FullMessages()[0].Content != got.Conversation[0].Content {
		t.Fatal("lineage conversation diverged from active conversation")
	}
}

func TestSmartContextManagerPreAssembly(t *testing.T) {
	m := &SmartContextManager{}
	state := RunState{TurnCount: 4}
	got, err := m.PreAssembly(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TurnCount != state.TurnCount {
		t.Errorf("TurnCount: got %d, want %d", got.TurnCount, state.TurnCount)
	}
}

func TestSmartContextManagerPostIngestionInitializesEpochFromLoadedHistory(t *testing.T) {
	cm := &SmartContextManager{maskingWindowTurns: 5}
	state := RunState{TurnCount: 12}

	got, err := cm.PostIngestion(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TurnCount != state.TurnCount {
		t.Fatalf("TurnCount = %d, want %d", got.TurnCount, state.TurnCount)
	}
	if got, want := cm.epochStartTurn, 12; got != want {
		t.Fatalf("epochStartTurn = %d, want %d", got, want)
	}
	if got, want := cm.epochMaskBoundary, 7; got != want {
		t.Fatalf("epochMaskBoundary = %d, want %d", got, want)
	}
}

func TestSmartContextManagerKeepsMaskedPrefixStableAcrossEpochAdvance(t *testing.T) {
	cm := &SmartContextManager{maskingWindowTurns: 5, epochStartTurn: 5}
	first := RunState{
		TurnCount:    9,
		Conversation: epochTestConversation(10),
	}
	first.Lineage = newConversationLineage(first.Conversation)

	gotFirst, err := cm.PreAssembly(context.Background(), first)
	if err != nil {
		t.Fatalf("first PreAssembly error: %v", err)
	}
	if got, want := cm.epochMaskBoundary, 5; got != want {
		t.Fatalf("epochMaskBoundary after advance = %d, want %d", got, want)
	}
	if got, want := cm.epochStartTurn, 10; got != want {
		t.Fatalf("epochStartTurn after advance = %d, want %d", got, want)
	}

	second := RunState{
		TurnCount:    10,
		Conversation: epochTestConversation(11),
	}
	second.Lineage = newConversationLineage(second.Conversation)

	gotSecond, err := cm.PreAssembly(context.Background(), second)
	if err != nil {
		t.Fatalf("second PreAssembly error: %v", err)
	}
	if got, want := cm.epochMaskBoundary, 5; got != want {
		t.Fatalf("epochMaskBoundary after steady turn = %d, want %d", got, want)
	}
	if got, want := cm.epochStartTurn, 10; got != want {
		t.Fatalf("epochStartTurn after steady turn = %d, want %d", got, want)
	}

	firstMasked := maskedPrefixByTurn(gotFirst.Conversation, 5)
	secondMasked := maskedPrefixByTurn(gotSecond.Conversation, 5)
	if len(firstMasked) != len(secondMasked) {
		t.Fatalf("masked prefix len = %d, want %d", len(firstMasked), len(secondMasked))
	}
	for i := range firstMasked {
		if firstMasked[i].Role != secondMasked[i].Role || firstMasked[i].Content != secondMasked[i].Content {
			t.Fatalf("masked prefix message %d = %#v, want %#v", i, firstMasked[i], secondMasked[i])
		}
	}
	if !strings.Contains(firstMasked[1].Content, "[turn 1]") {
		t.Fatalf("first epoch assistant content = %q, want turn prefix", firstMasked[1].Content)
	}
}

func TestIngestToolResultBlocksAnnotationWhenPreviousReadMasked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	content := `{"path":"note.txt","start_line":1,"end_line":3,"total_lines":3,"output":"one\ntwo\nthree\n"}`
	cm := NewContextManager("smart", config.ContextManagementConfig{
		MaskingWindowTurns: 1,
		ReadAnnotations:    true,
	}).(*SmartContextManager)

	// Turn 1: first read — full content
	got1 := cm.IngestToolResult(1, "read", content)
	if got1 != content {
		t.Fatalf("turn 1 read = %q, want full content", got1)
	}

	// Turn 2: re-read with no masking active — should get annotation
	got2 := cm.IngestToolResult(2, "read", content)
	if !strings.Contains(got2, "file unchanged since turn 1") {
		t.Fatalf("turn 2 read = %q, want unchanged annotation", got2)
	}

	// Simulate epoch advance: masking boundary moves past turn 2
	cm.epochMaskBoundary = 3

	// Turn 3: re-read — PreviousRead.LastTurn is 2 (updated by turn 2's read),
	// which is now masked, so the visibility gate should suppress the
	// annotation and return full content
	got3 := cm.IngestToolResult(3, "read", content)
	if strings.Contains(got3, "file unchanged since turn") {
		t.Fatalf("turn 3 read after masking = %q, want full content (turn 1 masked)", got3)
	}
	if got3 != content {
		t.Fatalf("turn 3 read = %q, want original full content", got3)
	}
}

func TestNewContextManager(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		wantType string
	}{
		{"naive mode", "naive", "*agent.NaiveContextManager"},
		{"smart mode", "smart", "*agent.SmartContextManager"},
		{"empty falls back to naive", "", "*agent.NaiveContextManager"},
		{"unknown falls back to naive", "unknown", "*agent.NaiveContextManager"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewContextManager(tc.mode)
			if m == nil {
				t.Fatal("NewContextManager returned nil")
			}
			switch tc.wantType {
			case "*agent.NaiveContextManager":
				if _, ok := m.(*NaiveContextManager); !ok {
					t.Errorf("got %T, want *NaiveContextManager", m)
				}
			case "*agent.SmartContextManager":
				if _, ok := m.(*SmartContextManager); !ok {
					t.Errorf("got %T, want *SmartContextManager", m)
				}
			}
		})
	}
}

func TestNewContextManagerAppliesCompactionStrategy(t *testing.T) {
	m, ok := NewContextManager("smart", config.ContextManagementConfig{
		CompactionStrategy: config.CompactionStrategyHybrid,
	}).(*SmartContextManager)
	if !ok {
		t.Fatalf("NewContextManager returned %T, want *SmartContextManager", m)
	}
	if got, want := m.compactionStrategy, config.CompactionStrategyHybrid; got != want {
		t.Fatalf("compactionStrategy = %q, want %q", got, want)
	}
}

func TestNewContextManagerAppliesScratchpadMode(t *testing.T) {
	m, ok := NewContextManager("smart", config.ContextManagementConfig{
		ScratchpadMode: config.ScratchpadModeHybrid,
	}).(*SmartContextManager)
	if !ok {
		t.Fatalf("NewContextManager returned %T, want *SmartContextManager", m)
	}
	if got, want := m.scratchpadMode, config.ScratchpadModeHybrid; got != want {
		t.Fatalf("scratchpadMode = %q, want %q", got, want)
	}
}

func TestIngestToolResultCapturesScratchpadState(t *testing.T) {
	t.Parallel()
	cm := &SmartContextManager{}
	result := cm.IngestToolResult(1, "scratchpad", `{"status":"ok","intent":"fix bug","decisions":"chose X","open":"","next":"fix"}`)
	if result != `{"ok":true}` {
		t.Fatalf("result = %q, want compact ack", result)
	}
	if cm.scratchpad.Intent != "fix bug" {
		t.Fatalf("Intent = %q, want fix bug", cm.scratchpad.Intent)
	}
	if cm.scratchpad.Decisions != "chose X" {
		t.Fatalf("Decisions = %q, want chose X", cm.scratchpad.Decisions)
	}
}

func TestIngestToolResultWarnsOnLegacyScratchpadFields(t *testing.T) {
	var events []output.Event
	cm := &SmartContextManager{}
	cm.SetEventSink(output.SinkFunc(func(event output.Event) { events = append(events, event) }))

	result := cm.IngestToolResult(1, "scratchpad", `{"status":"ok","goal":"fix bug","plan":"read code","step":"reading","decisions":"chose X","files":"foo.go (read)","open":"","next":"fix"}`)
	if result != `{"ok":true}` {
		t.Fatalf("result = %q, want compact ack", result)
	}
	var sawWarning bool
	for _, event := range events {
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok || payload.Kind != "scratchpad" || payload.Severity != "warning" {
			continue
		}
		sawWarning = true
		if !containsString(payload.Notes, "ignored legacy fields: files, goal, plan, step") && !containsString(payload.Notes, "ignored legacy fields: goal, plan, step, files") {
			t.Fatalf("warning notes = %v, want legacy fields note", payload.Notes)
		}
	}
	if !sawWarning {
		t.Fatal("missing legacy scratchpad warning diagnostic")
	}
}

func TestHeuristicDecisionsAppendWithoutModelScratchpadInput(t *testing.T) {
	cm := &SmartContextManager{}

	if got := cm.ObserveToolResult(1, "edit", map[string]any{"path": "note.txt"}, `{"path":"note.txt","output":"updated line"}`); got != `{"path":"note.txt","output":"updated line"}` {
		t.Fatalf("ObserveToolResult(edit) = %q, want passthrough JSON", got)
	}
	cm.RecordCompaction(2)

	if !strings.Contains(cm.scratchpad.Decisions, "edited note.txt") {
		t.Fatalf("Decisions = %q, want edit heuristic", cm.scratchpad.Decisions)
	}
	if !strings.Contains(cm.scratchpad.Decisions, "compaction occurred at turn 2") {
		t.Fatalf("Decisions = %q, want compaction heuristic", cm.scratchpad.Decisions)
	}
}

func TestHeuristicDecisionsRecordFileSwitches(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")
	if err := os.WriteFile(first, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write first file: %v", err)
	}
	if err := os.WriteFile(second, []byte("two\n"), 0o644); err != nil {
		t.Fatalf("write second file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	cm := &SmartContextManager{}
	_ = cm.ObserveToolResult(1, "read", nil, `{"path":"first.txt","start_line":1,"end_line":1,"total_lines":1,"output":"one\n"}`)
	_ = cm.ObserveToolResult(2, "read", nil, `{"path":"second.txt","start_line":1,"end_line":1,"total_lines":1,"output":"two\n"}`)

	if cm.scratchpad.WorkingFile != "second.txt" {
		t.Fatalf("WorkingFile = %q, want second.txt", cm.scratchpad.WorkingFile)
	}
	if !strings.Contains(cm.scratchpad.Decisions, "switched from first.txt to second.txt") {
		t.Fatalf("Decisions = %q, want file-switch heuristic", cm.scratchpad.Decisions)
	}
}

func epochTestConversation(turns int) []Message {
	messages := make([]Message, 0, turns*2)
	for turn := 1; turn <= turns; turn++ {
		messages = append(messages, Message{
			Role:    MessageRoleUser,
			Content: fmt.Sprintf("user %d", turn),
			Turn:    turn,
		})
		messages = append(messages, Message{
			Role:    MessageRoleAssistant,
			Content: fmt.Sprintf("assistant %d", turn),
			Turn:    turn,
		})
	}
	return messages
}

func maskedPrefixByTurn(messages []Message, boundary int) []Message {
	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.Turn > 0 && message.Turn < boundary {
			out = append(out, message)
		}
	}
	return out
}

func TestIngestToolResultEmitsGenerationMismatchDiagnostic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	var events []output.Event
	cm := &SmartContextManager{}
	cm.SetEventSink(output.SinkFunc(func(event output.Event) { events = append(events, event) }))

	content := `{"path":"note.txt","start_line":1,"end_line":1,"total_lines":1,"output":"one\n"}`
	if got := cm.IngestToolResult(1, "read", content); got != content {
		t.Fatalf("first read = %q, want full content", got)
	}
	cm.RecordMutation("note.txt")
	if got := cm.IngestToolResult(2, "read", content); got != content {
		t.Fatalf("second read after generation bump = %q, want full content", got)
	}

	var mismatch output.ContextDiagnosticsEvent
	found := false
	for _, event := range events {
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok || payload.Kind != "file_annotation" || payload.Turn != 2 {
			continue
		}
		mismatch = payload
		found = true
	}
	if !found {
		t.Fatal("missing file annotation diagnostic for second read")
	}
	if got, want := mismatch.Reason, "generation changed"; got != want {
		t.Fatalf("diagnostic reason = %q, want %q", got, want)
	}
	if !containsString(mismatch.Notes, "mtime_unchanged") {
		t.Fatalf("diagnostic notes = %v, want mtime_unchanged", mismatch.Notes)
	}
}

func TestOnTurnCompleteResetsFailuresOnCall(t *testing.T) {
	cm := &SmartContextManager{scratchpadMode: config.ScratchpadModeHybrid, scratchpadFailures: 2}
	cm.OnTurnComplete(1, true)
	if cm.scratchpadFailures != 0 {
		t.Fatalf("scratchpadFailures = %d, want 0 after scratchpad called", cm.scratchpadFailures)
	}
}

func TestOnTurnCompleteIncrementsFailuresWhenMissed(t *testing.T) {
	cm := &SmartContextManager{scratchpadMode: config.ScratchpadModeHybrid}
	cm.OnTurnComplete(1, false)
	if cm.scratchpadFailures != 1 {
		t.Fatalf("scratchpadFailures = %d, want 1", cm.scratchpadFailures)
	}
	cm.OnTurnComplete(2, false)
	if cm.scratchpadFailures != 2 {
		t.Fatalf("scratchpadFailures = %d, want 2", cm.scratchpadFailures)
	}
}

func TestOnTurnCompleteEmitsEventAtThreshold(t *testing.T) {
	var events []output.Event
	cm := &SmartContextManager{scratchpadMode: config.ScratchpadModeHybrid}
	cm.SetEventSink(output.SinkFunc(func(e output.Event) { events = append(events, e) }))

	cm.OnTurnComplete(1, false)
	cm.OnTurnComplete(2, false)
	if len(events) != 0 {
		t.Fatalf("events emitted before threshold: %d", len(events))
	}
	cm.OnTurnComplete(3, false)
	if len(events) != 1 {
		t.Fatalf("events = %d after threshold, want 1", len(events))
	}
}

func TestOnTurnCompleteNaiveIsNoop(t *testing.T) {
	// NaiveContextManager.OnTurnComplete must not panic.
	m := &NaiveContextManager{}
	m.OnTurnComplete(0, false)
	m.OnTurnComplete(1, true)
}

func TestParseScratchpadToolResultDecisionsConcatenation(t *testing.T) {
	tests := []struct {
		name          string
		previous      Scratchpad
		content       string
		wantDecisions string
	}{
		{
			name:          "first decision appended",
			previous:      Scratchpad{},
			content:       `{"intent":"g","decisions":"chose A","open":"","next":"n"}`,
			wantDecisions: "chose A",
		},
		{
			name:          "subsequent decision concatenated",
			previous:      Scratchpad{Decisions: "chose A"},
			content:       `{"intent":"g","decisions":"chose B","open":"","next":"n"}`,
			wantDecisions: "chose A\nchose B",
		},
		{
			name:          "none skips append",
			previous:      Scratchpad{Decisions: "chose A"},
			content:       `{"intent":"g","decisions":"none","open":"","next":"n"}`,
			wantDecisions: "chose A",
		},
		{
			name:          "empty skips append",
			previous:      Scratchpad{Decisions: "chose A"},
			content:       `{"intent":"g","decisions":"","open":"","next":"n"}`,
			wantDecisions: "chose A",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, ok := parseScratchpadToolResult(tc.content, tc.previous)
			if !ok {
				t.Fatal("parseScratchpadToolResult() = false, want true")
			}
			if len(warnings) != 0 {
				t.Fatalf("warnings = %v, want none", warnings)
			}
			if got.Decisions != tc.wantDecisions {
				t.Errorf("Decisions = %q, want %q", got.Decisions, tc.wantDecisions)
			}
		})
	}
}

func TestParseScratchpadToolResultDecisionsByteCapEviction(t *testing.T) {
	// Fill previous decisions close to cap, then add more to trigger eviction.
	old := strings.Repeat("x", 1990)
	previous := Scratchpad{Decisions: old}
	content := `{"intent":"g","decisions":"new entry","open":"","next":"n"}`
	got, warnings, ok := parseScratchpadToolResult(content, previous)
	if !ok {
		t.Fatal("parseScratchpadToolResult() = false, want true")
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if len(got.Decisions) > decisionsMaxBytes {
		t.Errorf("Decisions len = %d, want <= %d", len(got.Decisions), decisionsMaxBytes)
	}
	if !strings.Contains(got.Decisions, "new entry") {
		t.Errorf("Decisions = %q, want to contain newest entry", got.Decisions)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(data)
}

func bashOutputForIngestionTest() map[string]any {
	return map[string]any{
		"exit_code": 1,
		"output":    strings.Repeat("filler line\n", 900) + "\x1b[31mwarning: retry\x1b[0m\nwarning: retry\nwarning: retry\nfinal tail\n",
	}
}

func grepOutputForIngestionTest() map[string]any {
	lines := make([]string, 0, 240)
	for i := 0; i < 205; i++ {
		lines = append(lines, "info: retrying")
	}
	for i := 0; i < 35; i++ {
		lines = append(lines, "match line")
	}
	return map[string]any{
		"matches":  240,
		"returned": 240,
		"output":   strings.Join(lines, "\n"),
	}
}
