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
	"github.com/luispabon/steiner/internal/tool"
	builtin "github.com/luispabon/steiner/internal/tool/builtin"
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

func TestSmartContextManagerPostIngestionUsesPerMessageTurnsForLoadedToolHistory(t *testing.T) {
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
	state := RunState{
		TurnCount: 4,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "u1", Turn: 2},
			{Role: MessageRoleAssistant, Content: "a1", Turn: 2},
			{Role: MessageRoleTool, Name: "read", Content: content, Turn: 2},
			{Role: MessageRoleUser, Content: "u2", Turn: 4},
			{Role: MessageRoleAssistant, Content: "a2", Turn: 4},
			{Role: MessageRoleTool, Name: "read", Content: content, Turn: 4},
		},
	}
	state.Lineage = newConversationLineage(state.Conversation)

	got, err := (&SmartContextManager{}).PostIngestion(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Conversation[2].Turn != 2 || got.Conversation[5].Turn != 4 {
		t.Fatalf("turns were rewritten: %#v", got.Conversation)
	}
	if got.Conversation[2].Content != content {
		t.Fatalf("first loaded read = %q, want full content", got.Conversation[2].Content)
	}
	if !strings.Contains(got.Conversation[5].Content, "file unchanged since turn 2") {
		t.Fatalf("second loaded read = %q, want annotation anchored to its own prior turn", got.Conversation[5].Content)
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
	otherFile := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(otherFile, []byte("other\ncontent\n"), 0o644); err != nil {
		t.Fatalf("write other file: %v", err)
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
	otherContent := `{"path":"other.txt","start_line":1,"end_line":2,"total_lines":2,"output":"other\ncontent\n"}`
	cm := NewContextManager("smart", config.ContextManagementConfig{
		MaskingWindowTurns: 1,
		ReadAnnotations:    true,
	}).(*SmartContextManager)

	// Turn 1: first read of note.txt — full content
	got1 := cm.IngestToolResult(1, "read", content)
	if got1 != content {
		t.Fatalf("turn 1 read = %q, want full content", got1)
	}

	// Simulate PreAssembly for turn 2 — epoch advances, boundary=1
	state2 := RunState{
		TurnCount: 1,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "u1", Turn: 1},
			{Role: MessageRoleAssistant, Content: "a1", Turn: 1, ToolCalls: []ToolCall{{ID: "c1", Name: "read", Arguments: map[string]any{"path": "note.txt"}}}},
			{Role: MessageRoleTool, ToolCallID: "c1", Name: "read", Content: got1, Turn: 1},
		},
	}
	state2.Lineage = newConversationLineage(state2.Conversation)
	_, _ = cm.PreAssembly(context.Background(), state2)

	// Turn 2: read a DIFFERENT file — note.txt's tracker entry stays at LastTurn=1
	got2 := cm.IngestToolResult(2, "read", otherContent)
	if got2 != otherContent {
		t.Fatalf("turn 2 read = %q, want full other content", got2)
	}

	// Simulate PreAssembly for turn 3 — epoch advances, boundary=2, turn 1 masked
	state3 := RunState{
		TurnCount: 2,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "u1", Turn: 1},
			{Role: MessageRoleAssistant, Content: "a1", Turn: 1, ToolCalls: []ToolCall{{ID: "c1", Name: "read", Arguments: map[string]any{"path": "note.txt"}}}},
			{Role: MessageRoleTool, ToolCallID: "c1", Name: "read", Content: got1, Turn: 1},
			{Role: MessageRoleUser, Content: "u2", Turn: 2},
			{Role: MessageRoleAssistant, Content: "a2", Turn: 2, ToolCalls: []ToolCall{{ID: "c2", Name: "read", Arguments: map[string]any{"path": "other.txt"}}}},
			{Role: MessageRoleTool, ToolCallID: "c2", Name: "read", Content: got2, Turn: 2},
		},
	}
	state3.Lineage = newConversationLineage(state3.Conversation)
	_, _ = cm.PreAssembly(context.Background(), state3)

	// Turn 3: re-read note.txt — PreviousRead.LastTurn=1 (note.txt not read at turn 2),
	// epochMaskBoundary=2 → 1<2 → gate fires, annotation suppressed
	got3 := cm.IngestToolResult(3, "read", content)
	if strings.Contains(got3, "file unchanged since turn") {
		t.Fatalf("turn 3 read after masking = %q, want full content (turn 1 is masked)", got3)
	}
	if got3 != content {
		t.Fatalf("turn 3 read = %q, want original full content", got3)
	}
}

func TestIngestToolResultBlocksAnnotationWhenPreviousReadCompacted(t *testing.T) {
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
		MaskingWindowTurns: 5,
		ReadAnnotations:    true,
	}).(*SmartContextManager)

	// Turn 1: first read — full content
	got1 := cm.IngestToolResult(1, "read", content)
	if got1 != content {
		t.Fatalf("turn 1 read = %q, want full content", got1)
	}

	// Turn 2: re-read — annotation (turn 1 still visible)
	got2 := cm.IngestToolResult(2, "read", content)
	if !strings.Contains(got2, "file unchanged since turn 1") {
		t.Fatalf("turn 2 read = %q, want unchanged annotation", got2)
	}

	// Simulate compaction by setting minVisibleTurn above turn 1
	cm.minVisibleTurn = 2

	// Turn 3: re-read — PreviousRead.LastTurn is 2 (updated by turn 2's read),
	// minVisibleTurn=2, 2<2 false — gate doesn't fire for consecutive reads.
	// Annotation still references turn 2 which is still visible.
	// The gate fires when there is a gap between the last read and current turn.
	got3 := cm.IngestToolResult(3, "read", content)
	if !strings.Contains(got3, "file unchanged since turn") {
		t.Fatalf("turn 3 read = %q, want unchanged annotation (previous read turn 2 still visible)", got3)
	}
	_ = got1
	_ = got2
	_ = got3
}

func TestIngestToolResultBlocksAnnotationWhenPreviousReadCompactedWithGap(t *testing.T) {
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
		MaskingWindowTurns: 5,
		ReadAnnotations:    true,
	}).(*SmartContextManager)

	// Turn 1: first read — full content
	got1 := cm.IngestToolResult(1, "read", content)
	if got1 != content {
		t.Fatalf("turn 1 read = %q, want full content", got1)
	}

	// Don't read at turn 2 — creates a gap, PreviousRead.LastTurn stays at 1

	// Simulate compaction by setting minVisibleTurn above turn 1
	cm.minVisibleTurn = 2

	// Turn 3: re-read — PreviousRead.LastTurn is 1 (no read at turn 2),
	// minVisibleTurn=2, 1<2 — gate fires!
	got3 := cm.IngestToolResult(3, "read", content)
	if strings.Contains(got3, "file unchanged since turn") {
		t.Fatalf("turn 3 read after compaction = %q, want full content (turn 1 dropped)", got3)
	}
	if got3 != content {
		t.Fatalf("turn 3 read = %q, want original full content", got3)
	}
	_ = got1
}

func TestIngestToolResultAfterMaskingSupportsExactEditFollowUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	original := "alpha\nbeta\ncharlie\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
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

	readResult := `{"path":"note.txt","start_line":1,"end_line":3,"total_lines":3,"output":"alpha\nbeta\ncharlie\n"}`
	cm := NewContextManager("smart", config.ContextManagementConfig{
		MaskingWindowTurns: 1,
		ReadAnnotations:    true,
	}).(*SmartContextManager)

	if got := cm.IngestToolResult(1, "read", readResult); got != readResult {
		t.Fatalf("turn 1 read = %q, want full content", got)
	}

	state2 := RunState{
		TurnCount: 1,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "u1", Turn: 1},
			{Role: MessageRoleAssistant, Content: "a1", Turn: 1},
		},
	}
	state2.Lineage = newConversationLineage(state2.Conversation)
	if _, err := cm.PreAssembly(context.Background(), state2); err != nil {
		t.Fatalf("turn 2 preassembly: %v", err)
	}

	state3 := RunState{
		TurnCount: 2,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "u1", Turn: 1},
			{Role: MessageRoleAssistant, Content: "a1", Turn: 1},
			{Role: MessageRoleUser, Content: "u2", Turn: 2},
			{Role: MessageRoleAssistant, Content: "a2", Turn: 2},
		},
	}
	state3.Lineage = newConversationLineage(state3.Conversation)
	if _, err := cm.PreAssembly(context.Background(), state3); err != nil {
		t.Fatalf("turn 3 preassembly: %v", err)
	}

	got3 := cm.IngestToolResult(3, "read", readResult)
	if strings.Contains(got3, "file unchanged since turn") {
		t.Fatalf("turn 3 read = %q, want no stale annotation", got3)
	}

	var reread builtin.ReadResult
	if err := json.Unmarshal([]byte(got3), &reread); err != nil {
		t.Fatalf("unmarshal reread result: %v", err)
	}
	if reread.Output != original {
		t.Fatalf("reread output = %q, want full original content", reread.Output)
	}

	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	editTool := builtin.NewEditTool(builtin.Env{WorkDir: dir, PathPolicy: &policy})
	resultI, err := editTool.Handler(context.Background(), map[string]any{
		"path":       "note.txt",
		"old_string": reread.Output,
		"new_string": "alpha\nbeta updated\ncharlie\n",
	})
	if err != nil {
		t.Fatalf("edit handler error: %v", err)
	}
	mutated, ok := resultI.(*builtin.MutationResult)
	if !ok {
		t.Fatalf("edit result type = %T, want *MutationResult", resultI)
	}
	if !mutated.Mutated {
		t.Fatalf("edit mutated = false, want true; output=%q", mutated.Output)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if got, want := string(data), "alpha\nbeta updated\ncharlie\n"; got != want {
		t.Fatalf("edited file = %q, want %q", got, want)
	}
}

func TestIngestToolResultSuppressesTurnZeroPlaceholderAnnotations(t *testing.T) {
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

	got1 := cm.IngestToolResult(0, "read", content)
	if got1 != content {
		t.Fatalf("turn 0 read = %q, want full content", got1)
	}

	cm.minVisibleTurn = 1
	cm.epochMaskBoundary = 1

	got2 := cm.IngestToolResult(2, "read", content)
	if got2 != content {
		t.Fatalf("turn 2 reread after turn 0 placeholder = %q, want full content", got2)
	}
	if strings.Contains(got2, "file unchanged since turn 0") {
		t.Fatalf("turn 2 reread = %q, want no turn 0 annotation", got2)
	}
}

func TestObserveReadHeuristicsRecordsSuppressionFact(t *testing.T) {
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

	// Turn 1: first read
	_ = cm.IngestToolResult(1, "read", content)

	// Turn 2: re-read, gets annotation
	_ = cm.IngestToolResult(2, "read", content)

	// Simulate masking boundary advancing past turn 2
	cm.epochMaskBoundary = 3

	// Turn 3: re-read, gate suppresses annotation
	_ = cm.IngestToolResult(3, "read", content)

	if !strings.Contains(cm.scratchpad.Decisions, "previous read turn 2 no longer visible") {
		t.Fatalf("Decisions = %q, want suppression fact", cm.scratchpad.Decisions)
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

func TestIngestToolResultLeavesScratchpadUnchangedOnLegacyOnlyPayload(t *testing.T) {
	cm := &SmartContextManager{scratchpad: Scratchpad{Intent: "keep"}}

	result := cm.IngestToolResult(1, "scratchpad", `{"status":"ok","goal":"fix bug","plan":"read code","step":"reading","decisions":"chose X","files":"foo.go (read)","open":"","next":"fix"}`)
	if result != `{"ok":true}` {
		t.Fatalf("result = %q, want compact ack", result)
	}
	if cm.scratchpad.Intent != "keep" {
		t.Fatalf("Intent = %q, want unchanged scratchpad on rejected legacy payload", cm.scratchpad.Intent)
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

func TestHeuristicDecisionsSanitizeBashCommandSummary(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	cm := &SmartContextManager{}

	got := cm.ObserveToolResult(1, "bash", map[string]any{
		"cwd":     cwd,
		"command": "cd " + cwd + " && go test ./internal/agent",
	}, `{"exit_code":0,"output":"ok\n"}`)
	if got != `{"exit_code":0,"output":"ok\n"}` {
		t.Fatalf("ObserveToolResult(bash) = %q, want passthrough JSON", got)
	}
	if strings.Contains(cm.scratchpad.Decisions, cwd) {
		t.Fatalf("Decisions = %q, want no absolute cwd", cm.scratchpad.Decisions)
	}
	if !strings.Contains(cm.scratchpad.Decisions, "tests passed: go test ./internal/agent") {
		t.Fatalf("Decisions = %q, want sanitized bash summary", cm.scratchpad.Decisions)
	}
}

func TestSummarizeRecentToolCallsSanitizesAbsolutePaths(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	got := summarizeRecentToolCalls([]Message{
		{
			Role: MessageRoleAssistant,
			ToolCalls: []ToolCall{
				{
					Name: "bash",
					Arguments: map[string]any{
						"cwd":     cwd,
						"command": "cd " + cwd + " && go test ./...",
					},
				},
			},
		},
	}, 1)
	if len(got) != 1 {
		t.Fatalf("summarizeRecentToolCalls() len = %d, want 1", len(got))
	}
	if strings.Contains(got[0], cwd) {
		t.Fatalf("summary = %q, want no absolute cwd", got[0])
	}
	if !strings.Contains(got[0], "go test ./...") {
		t.Fatalf("summary = %q, want sanitized command fragment", got[0])
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
	if strings.Contains(cm.scratchpad.Decisions, "switched from first.txt to second.txt") {
		t.Fatalf("Decisions = %q, want no durable file-switch heuristic", cm.scratchpad.Decisions)
	}
	if !strings.Contains(cm.scratchpad.LastAction, "read second.txt") {
		t.Fatalf("LastAction = %q, want read working-file update", cm.scratchpad.LastAction)
	}
}

func TestObserveToolResultTracksApplyPatchMutationHeuristics(t *testing.T) {
	cm := &SmartContextManager{}
	got := cm.ObserveToolResult(4, "apply_patch", map[string]any{"path": "note.txt"}, `{"path":"note.txt","output":"patched 1 hunk"}`)
	if got != `{"path":"note.txt","output":"patched 1 hunk"}` {
		t.Fatalf("ObserveToolResult(apply_patch) = %q, want passthrough JSON", got)
	}
	if cm.scratchpad.WorkingFile != "note.txt" {
		t.Fatalf("WorkingFile = %q, want note.txt", cm.scratchpad.WorkingFile)
	}
	if !strings.Contains(cm.scratchpad.LastAction, "patched note.txt") {
		t.Fatalf("LastAction = %q, want apply_patch working-file update", cm.scratchpad.LastAction)
	}
}

func TestApplyPatchMutationInvalidatesReadAnnotationsAcrossRanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ncharlie\n"), 0o644); err != nil {
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

	firstRegion := `{"path":"note.txt","start_line":1,"end_line":1,"total_lines":3,"output":"alpha\n"}`
	secondRegion := `{"path":"note.txt","start_line":2,"end_line":2,"total_lines":3,"output":"beta\n"}`

	t.Run("successful apply_patch bumps file generation for later reads", func(t *testing.T) {
		cm := &SmartContextManager{}
		if got := cm.IngestToolResult(1, "read", firstRegion); got != firstRegion {
			t.Fatalf("first read = %q, want full content", got)
		}
		recordMutationForContextManager(cm, "apply_patch", map[string]any{"path": "note.txt"}, &builtin.ApplyPatchResult{
			Paths:        []string{"note.txt"},
			HunksApplied: 1,
			Output:       "patched one hunk",
		})

		gotSame := cm.IngestToolResult(2, "read", firstRegion)
		if strings.Contains(gotSame, "file unchanged since turn 1") {
			t.Fatalf("same-region reread = %q, want no stale annotation", gotSame)
		}
		gotDifferent := cm.IngestToolResult(3, "read", secondRegion)
		if strings.Contains(gotDifferent, "file unchanged since turn") {
			t.Fatalf("different-region reread = %q, want no stale annotation", gotDifferent)
		}
	})

	t.Run("failed apply_patch leaves generation unchanged", func(t *testing.T) {
		cm := &SmartContextManager{}
		if got := cm.IngestToolResult(1, "read", firstRegion); got != firstRegion {
			t.Fatalf("first read = %q, want full content", got)
		}
		recordMutationForContextManager(cm, "apply_patch", map[string]any{"path": "note.txt"}, &builtin.ApplyPatchResult{
			Paths:        []string{"note.txt"},
			HunksApplied: 1,
			HunksFailed:  1,
			Output:       "hunk 0: no match for old text",
		})

		got := cm.IngestToolResult(2, "read", firstRegion)
		if !strings.Contains(got, "file unchanged since turn 1") {
			t.Fatalf("failed-patch reread = %q, want unchanged annotation preserved", got)
		}
	})
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
			got, ok := parseScratchpadToolResult(tc.content, tc.previous)
			if !ok {
				t.Fatal("parseScratchpadToolResult() = false, want true")
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
	got, ok := parseScratchpadToolResult(content, previous)
	if !ok {
		t.Fatal("parseScratchpadToolResult() = false, want true")
	}
	if len(got.Decisions) > decisionsMaxBytes {
		t.Errorf("Decisions len = %d, want <= %d", len(got.Decisions), decisionsMaxBytes)
	}
	if !strings.Contains(got.Decisions, "new entry") {
		t.Errorf("Decisions = %q, want to contain newest entry", got.Decisions)
	}
}

func TestParseScratchpadToolResultDecisionsLineCapEviction(t *testing.T) {
	lines := make([]string, 0, decisionsMaxLines+4)
	for i := 0; i < decisionsMaxLines+4; i++ {
		lines = append(lines, fmt.Sprintf("line-%02d", i))
	}
	previous := Scratchpad{Decisions: strings.Join(lines[:decisionsMaxLines], "\n")}
	got, ok := parseScratchpadToolResult(`{"intent":"g","decisions":"line-new","open":"","next":"n"}`, previous)
	if !ok {
		t.Fatal("parseScratchpadToolResult() = false, want true")
	}
	if len(strings.Split(got.Decisions, "\n")) > decisionsMaxLines {
		t.Fatalf("Decisions lines = %d, want <= %d", len(strings.Split(got.Decisions, "\n")), decisionsMaxLines)
	}
	if strings.Contains(got.Decisions, "line-00") {
		t.Fatalf("Decisions = %q, want oldest entry evicted first", got.Decisions)
	}
	if !strings.Contains(got.Decisions, "line-new") {
		t.Fatalf("Decisions = %q, want newest entry preserved", got.Decisions)
	}
}

func TestParseScratchpadToolResultRejectsLegacyOnlyPayloads(t *testing.T) {
	previous := Scratchpad{Intent: "keep"}
	got, ok := parseScratchpadToolResult(`{"status":"ok","goal":"fix bug","plan":"read code","step":"reading","files":"foo.go (read)"}`, previous)
	if ok {
		t.Fatalf("parseScratchpadToolResult() = true, want false; got=%+v", got)
	}
	if got.Intent != "keep" {
		t.Fatalf("parseScratchpadToolResult() changed state on legacy payload: %+v", got)
	}
}

func TestResetTaskStateIfNeededClearsStaleTaskFields(t *testing.T) {
	cm := &SmartContextManager{
		scratchpad: Scratchpad{
			Intent:       "inspect note",
			Decisions:    "old decision",
			Open:         "why is it stale?",
			Next:         "re-read note",
			WorkingFile:  "note.txt",
			LastAction:   "read note.txt",
			SessionState: "session state: turn=3 compactions=1",
		},
	}
	state := RunState{
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "continue"},
			{Role: MessageRoleUser, Content: "commit changes"},
		},
		Context: ContextState{
			ActiveFocus:        &ActiveFocus{Text: "inspect note"},
			UnresolvedWork:     []UnresolvedWorkItem{{Text: "re-read note"}},
			FileTrackerSummary: []string{"note.txt"},
			RecentToolCalls:    []string{"read path=note.txt"},
		},
	}

	cm.resetTaskStateIfNeeded(&state)

	if cm.scratchpad.Intent != "" || cm.scratchpad.Decisions != "" || cm.scratchpad.Open != "" || cm.scratchpad.Next != "" {
		t.Fatalf("scratchpad not cleared: %+v", cm.scratchpad)
	}
	if cm.scratchpad.WorkingFile != "" || cm.scratchpad.LastAction != "" {
		t.Fatalf("working fields not cleared: %+v", cm.scratchpad)
	}
	if state.Context.ActiveFocus != nil {
		t.Fatal("ActiveFocus = non-nil, want cleared")
	}
	if len(state.Context.UnresolvedWork) != 0 {
		t.Fatalf("UnresolvedWork = %v, want cleared", state.Context.UnresolvedWork)
	}
	if len(state.Context.FileTrackerSummary) != 0 || len(state.Context.RecentToolCalls) != 0 {
		t.Fatalf("scaffold summaries not cleared: %+v", state.Context)
	}
}

func TestResetTaskStateIfNeededIgnoresContinuations(t *testing.T) {
	cm := &SmartContextManager{
		scratchpad: Scratchpad{
			Intent:      "keep",
			WorkingFile: "note.txt",
		},
	}
	state := RunState{
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "please keep going"},
		},
	}

	cm.resetTaskStateIfNeeded(&state)

	if cm.scratchpad.Intent != "keep" || cm.scratchpad.WorkingFile != "note.txt" {
		t.Fatalf("scratchpad changed on continuation: %+v", cm.scratchpad)
	}
	if state.Context.ActiveFocus != nil || len(state.Context.UnresolvedWork) != 0 {
		t.Fatalf("context changed on continuation: %+v", state.Context)
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
