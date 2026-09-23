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
	"github.com/luispabon/steiner/internal/prompt"
)

func TestBaseContextManagerCachedSystemPreamble(t *testing.T) {
	t.Run("cache hit with same parameters", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("alpha", false, "", false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		second := manager.CachedSystemPreamble("alpha", false, "", false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second != first {
			t.Fatalf("second preamble = %q, want cached %q", second, first)
		}
	})

	t.Run("cache miss with different caveHuman", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", true, "", false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		second := manager.CachedSystemPreamble("", true, "", false, false, prompt.ParentWorkflowMode(), true, "", false, nil)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second == first {
			t.Fatal("second preamble should differ when caveHuman changes")
		}
	})

	t.Run("cache miss with different override", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("alpha", false, "", false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		second := manager.CachedSystemPreamble("beta", false, "", false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second == first {
			t.Fatal("second preamble should differ when override changes")
		}
	})

	t.Run("cache miss with different systemSuffix", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		second := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "Extended thinking enabled", false, nil)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second == first {
			t.Fatal("second preamble should differ when systemSuffix changes")
		}
		if !strings.Contains(second, "Extended thinking enabled") {
			t.Fatal("second preamble should contain the suffix")
		}
	})

	t.Run("cache hit with same systemSuffix", func(t *testing.T) {
		var manager baseContextManager
		suffix := "Custom model instruction"
		first := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, suffix, false, nil)
		second := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, suffix, false, nil)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second != first {
			t.Fatalf("second preamble should be cached when suffix is same")
		}
	})

	t.Run("cache miss when suffix changes from non-empty to empty", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "Some instruction", false, nil)
		second := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second == first {
			t.Fatal("preamble should differ when suffix is cleared")
		}
		if strings.Contains(second, "Some instruction") {
			t.Fatal("second preamble should not contain the old suffix")
		}
	})

	t.Run("cache miss with different caveHuman", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", true, "", false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		second := manager.CachedSystemPreamble("", true, "", false, false, prompt.ParentWorkflowMode(), true, "", false, nil)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second == first {
			t.Fatal("second preamble should differ when caveHuman changes")
		}
	})

	t.Run("cache miss with different lspEnabled", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		second := manager.CachedSystemPreamble("", false, "", false, true, prompt.ParentWorkflowMode(), false, "", false, nil)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second == first {
			t.Fatal("second preamble should differ when lspEnabled changes")
		}
		if !strings.Contains(second, "## Code intelligence (LSP)") {
			t.Fatal("second preamble should contain LSP guidance")
		}
	})

	t.Run("cache miss with different advisorEnabled", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		second := manager.CachedSystemPreamble("", false, "", true, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second == first {
			t.Fatal("second preamble should differ when advisorEnabled changes")
		}
		if !strings.Contains(second, "## Advisor") {
			t.Fatal("second preamble should contain advisor guidance")
		}
	})

	t.Run("cache miss with different workflow mode and hit with same workflow mode", func(t *testing.T) {
		var manager baseContextManager
		parent := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		child := manager.CachedSystemPreamble("", false, "", false, false, prompt.DelegatedChildWorkflowMode(), false, "", false, nil)
		childAgain := manager.CachedSystemPreamble("", false, "", false, false, prompt.DelegatedChildWorkflowMode(), false, "", false, nil)
		if parent == "" || child == "" {
			t.Fatal("cached preamble = empty, want content")
		}
		if parent == child {
			t.Fatal("preambles should differ when workflow mode changes")
		}
		if childAgain != child {
			t.Fatalf("child preamble = %q, want cached %q", childAgain, child)
		}
	})

	t.Run("cache hit with same sandboxEnabled and mounts", func(t *testing.T) {
		var manager baseContextManager
		mounts := []string{"/var/log", "/home/u/go"}
		first := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", true, mounts)
		second := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", true, mounts)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second != first {
			t.Fatalf("second preamble = %q, want cached %q", second, first)
		}
		if !strings.Contains(first, "The sandbox is enabled") {
			t.Fatal("preamble should contain sandbox instruction when sandboxEnabled")
		}
		if !strings.Contains(first, "Additional writable paths: /var/log, /home/u/go") {
			t.Fatal("preamble should list the writable mounts")
		}
	})

	t.Run("cache miss when sandboxEnabled changes", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		second := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", true, nil)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second == first {
			t.Fatal("second preamble should differ when sandboxEnabled changes")
		}
		if !strings.Contains(second, "The sandbox is enabled") {
			t.Fatal("second preamble should contain sandbox instruction")
		}
	})

	t.Run("cache miss when sandboxWritableMounts changes", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", true, []string{"/var/log"})
		second := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", true, []string{"/var/log", "/home/u/go"})
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second == first {
			t.Fatal("second preamble should differ when sandboxWritableMounts changes")
		}
		if !strings.Contains(first, "Additional writable paths: /var/log") {
			t.Fatal("first preamble should list the first mount set")
		}
		if strings.Contains(first, "/home/u/go") {
			t.Fatal("first preamble should not list the second mount set")
		}
	})

	t.Run("defensive copy of sandbox mounts on cache miss", func(t *testing.T) {
		var manager baseContextManager
		mounts := []string{"/var/log"}
		first := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", true, mounts)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if !strings.Contains(first, "## Sandbox") {
			t.Fatal("preamble should contain sandbox section when sandboxEnabled")
		}

		mounts[0] = "/home/u/go"
		mounts = append(mounts, "/tmp")

		unchanged := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", true, []string{"/var/log"})
		if unchanged != first {
			t.Fatalf("cached preamble changed after caller mutated its mounts slice: %q, want %q", unchanged, first)
		}

		regenerated := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", true, mounts)
		if regenerated == first {
			t.Fatal("preamble should regenerate when the mutated mounts slice differs")
		}
		if !strings.Contains(regenerated, "Additional writable paths: /home/u/go, /tmp") {
			t.Fatalf("regenerated preamble should list the mutated mounts, got %q", regenerated)
		}
	})

	t.Run("cache miss with different orchestrationLevel and hit when unchanged", func(t *testing.T) {
		var manager baseContextManager
		standard := manager.CachedSystemPreamble("", true, config.OrchestrationLevelStandard, false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		low := manager.CachedSystemPreamble("", true, config.OrchestrationLevelLow, false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		lowAgain := manager.CachedSystemPreamble("", true, config.OrchestrationLevelLow, false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		if standard == "" || low == "" {
			t.Fatal("cached preamble = empty, want content")
		}
		if standard == low {
			t.Fatal("preamble should differ when orchestration level changes")
		}
		if lowAgain != low {
			t.Fatalf("second low-level preamble = %q, want cached %q", lowAgain, low)
		}
		if !strings.Contains(standard, "## Your role") {
			t.Fatal("standard-level preamble should contain the role section")
		}
		if strings.Contains(low, "## Your role") {
			t.Fatal("low-level preamble should not contain the role section")
		}
	})

	t.Run("cache hit between nil and empty sandbox mounts", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", true, nil)
		second := manager.CachedSystemPreamble("", false, "", false, false, prompt.ParentWorkflowMode(), false, "", true, []string{})
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second != first {
			t.Fatalf("second preamble = %q, want cached %q when nil and empty mounts are equivalent", second, first)
		}
		if !strings.Contains(first, "## Sandbox") {
			t.Fatal("preamble should contain sandbox section when sandboxEnabled")
		}
	})
}

func TestBaseContextManagerRecordMutation(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantBump bool
	}{
		{name: "valid path bumps generation", path: "note.txt", wantBump: true},
		{name: "empty path ignored", path: "", wantBump: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			oldWD, err := os.Getwd()
			if err != nil {
				t.Fatalf("Getwd() error = %v", err)
			}
			if err := os.Chdir(dir); err != nil {
				t.Fatalf("Chdir() error = %v", err)
			}
			t.Cleanup(func() {
				if err := os.Chdir(oldWD); err != nil {
					t.Errorf("restore working directory: %v", err)
				}
			})

			var manager baseContextManager
			manager.RecordMutation(tc.path)

			canonical, ok := normalizeTrackedPath(tc.path)
			if !tc.wantBump {
				if ok && manager.fileTracker.generations[canonical] != 0 {
					t.Fatalf("generation for %q = %d, want 0", canonical, manager.fileTracker.generations[canonical])
				}
				return
			}
			if !ok {
				t.Fatalf("normalizeTrackedPath(%q) failed", tc.path)
			}
			if got := manager.fileTracker.generations[canonical]; got != 1 {
				t.Fatalf("generation for %q = %d, want 1", canonical, got)
			}
		})
	}
}

func TestBaseContextManagerObserveReadToolResult(t *testing.T) {
	tests := []struct {
		name            string
		configured      bool
		readAnnotations bool
		minVisibleTurn  int
		wantAnnotated   bool
		wantReason      string
		wantNote        string
	}{
		{
			name:            "annotations enabled",
			configured:      true,
			readAnnotations: true,
			wantAnnotated:   true,
			wantReason:      "unchanged since turn 1",
			wantNote:        "annotation produced",
		},
		{
			name:            "annotations disabled by config",
			configured:      true,
			readAnnotations: false,
			wantReason:      "annotations disabled",
		},
		{
			name:            "visibility gate suppresses annotation",
			configured:      true,
			readAnnotations: true,
			minVisibleTurn:  2,
			wantReason:      "previous read no longer visible in context",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "note.txt")
			if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
				t.Fatalf("write file: %v", err)
			}

			oldWD, err := os.Getwd()
			if err != nil {
				t.Fatalf("Getwd() error = %v", err)
			}
			if err := os.Chdir(dir); err != nil {
				t.Fatalf("Chdir() error = %v", err)
			}
			t.Cleanup(func() {
				if err := os.Chdir(oldWD); err != nil {
					t.Errorf("restore working directory: %v", err)
				}
			})

			var events []output.Event
			manager := baseContextManager{
				readAnnotations:       tc.readAnnotations,
				annotationsConfigured: tc.configured,
				minVisibleTurn:        tc.minVisibleTurn,
			}
			manager.SetEventSink(output.SinkFunc(func(event output.Event) { events = append(events, event) }))

			content := `{"path":"note.txt","start_line":1,"end_line":2,"total_lines":2,"output":"one\ntwo\n"}`
			if got := manager.observeReadToolResult(1, content); got != content {
				t.Fatalf("first read = %q, want full content", got)
			}
			got := manager.observeReadToolResult(2, content)
			if strings.Contains(got, "file unchanged since turn 1") != tc.wantAnnotated {
				t.Fatalf("second read annotation presence = %t, want %t; got %q", strings.Contains(got, "file unchanged since turn 1"), tc.wantAnnotated, got)
			}

			if tc.wantReason == "annotations disabled" {
				if len(events) != 0 {
					t.Fatalf("events = %d, want 0 when annotations disabled", len(events))
				}
				return
			}

			if len(events) < 1 {
				t.Fatalf("events = %d, want at least 1", len(events))
			}
			payload, ok := output.AsContextFileAnnotationEvent(events[len(events)-1].Payload)
			if !ok {
				t.Fatalf("payload type = %T, want ContextFileAnnotationEvent", events[len(events)-1].Payload)
			}
			if got, want := payload.Reason, tc.wantReason; got != want {
				t.Fatalf("reason = %q, want %q", got, want)
			}
			if tc.wantNote != "" && !containsString(payload.Notes, tc.wantNote) {
				t.Fatalf("notes = %v, want %q", payload.Notes, tc.wantNote)
			}
			if !containsString(payload.Notes, fmt.Sprintf("range=%s", "lines 1-2/2")) {
				t.Fatalf("notes = %v, want range note", payload.Notes)
			}
		})
	}
}

// TestContextStateManagerRetainsIngestedToolResultBytes pins that a tool result
// already sent to the provider is not reshaped by post-ingestion on a fresh run,
// while the read tracker is still reconstructed.
func TestContextStateManagerRetainsIngestedToolResultBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("two\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	historical, err := json.Marshal(readResult{Path: path, StartLine: 1, EndLine: 1, TotalLines: 1, Output: "one\n"})
	if err != nil {
		t.Fatalf("marshal read result: %v", err)
	}
	history := []Message{{
		Role:       MessageRoleTool,
		Name:       "read",
		ToolCallID: "call_1",
		Turn:       1,
		Content:    string(historical),
		Ingested:   true,
	}}

	for run := 1; run <= 2; run++ {
		manager := NewContextStateManager()
		var events []output.Event
		manager.SetEventSink(output.SinkFunc(func(event output.Event) { events = append(events, event) }))
		state := RunState{
			TurnCount:    2,
			Conversation: cloneMessages(history),
			Lineage:      newConversationLineage(cloneMessages(history)),
		}
		next, err := manager.PostIngestion(context.Background(), state)
		if err != nil {
			t.Fatalf("run %d PostIngestion error = %v", run, err)
		}
		got := next.Conversation[0]
		if got.Content != string(historical) {
			t.Fatalf("run %d content = %q, want provider-visible bytes %q", run, got.Content, string(historical))
		}
		if strings.Contains(got.Content, "file unchanged since turn") {
			t.Fatalf("run %d content = %q, want no reannotation", run, got.Content)
		}
		if len(events) != 0 {
			t.Fatalf("run %d events = %d, want 0 (no annotation diagnostics)", run, len(events))
		}
		if !manager.FileObserved(path) {
			t.Fatalf("run %d read tracker did not reconstruct %s", run, path)
		}
	}

	// Mechanism check: the same historical bytes are reshaped when unmarked, so
	// the second read is rewritten to an unchanged-file annotation. This is the
	// fresh-run rewrite that the Ingested flag prevents.
	firstRead, err := json.Marshal(readResult{Path: path, StartLine: 1, EndLine: 1, TotalLines: 1, Output: "one\n"})
	if err != nil {
		t.Fatalf("marshal first read: %v", err)
	}
	secondRead, err := json.Marshal(readResult{Path: path, StartLine: 1, EndLine: 1, TotalLines: 1, Output: "two\n"})
	if err != nil {
		t.Fatalf("marshal second read: %v", err)
	}
	unmarked := []Message{
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call_1", Turn: 1, Content: string(firstRead)},
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call_2", Turn: 2, Content: string(secondRead)},
	}
	reshaped, err := NewContextStateManager().PostIngestion(context.Background(), RunState{TurnCount: 2, Conversation: cloneMessages(unmarked), Lineage: newConversationLineage(cloneMessages(unmarked))})
	if err != nil {
		t.Fatalf("unmarked PostIngestion error = %v", err)
	}
	if !strings.Contains(reshaped.Conversation[1].Content, "file unchanged since turn") {
		t.Fatalf("unmarked second read = %q, want annotation rewriting provider-visible bytes", reshaped.Conversation[1].Content)
	}
}

// TestContextStateManagerShapesLegacyToolResultOnceThenFreezes pins the one-time
// ingestion shaping applied to an unmarked tool message and that the shaped
// bytes are frozen on subsequent runs.
func TestContextStateManagerShapesLegacyToolResultOnceThenFreezes(t *testing.T) {
	raw, err := json.Marshal(struct {
		ExitCode int    `json:"exit_code"`
		Output   string `json:"output"`
	}{ExitCode: 1, Output: "\x1b[31mwarning: retry\x1b[0m\nwarning: retry\nwarning: retry\n"})
	if err != nil {
		t.Fatalf("marshal legacy bash result: %v", err)
	}
	legacy := []Message{{
		Role:       MessageRoleTool,
		Name:       "bash",
		ToolCallID: "call_1",
		Turn:       1,
		Content:    string(raw),
	}}

	manager := NewContextStateManager()
	state := RunState{TurnCount: 1, Conversation: cloneMessages(legacy), Lineage: newConversationLineage(cloneMessages(legacy))}
	shaped, err := manager.PostIngestion(context.Background(), state)
	if err != nil {
		t.Fatalf("first PostIngestion error = %v", err)
	}
	firstPass := shaped.Conversation[0]
	if !firstPass.Ingested {
		t.Fatal("legacy tool message Ingested = false after first PostIngestion, want true")
	}
	if firstPass.Content == string(raw) {
		t.Fatalf("legacy content = %q, want one-time shaping to change it", firstPass.Content)
	}
	if strings.Contains(firstPass.Content, "\x1b[") {
		t.Fatalf("shaped content = %q, want ANSI escapes stripped", firstPass.Content)
	}

	frozen := cloneMessages(shaped.Conversation)
	second := RunState{TurnCount: 1, Conversation: cloneMessages(frozen), Lineage: newConversationLineage(cloneMessages(frozen))}
	again, err := NewContextStateManager().PostIngestion(context.Background(), second)
	if err != nil {
		t.Fatalf("second PostIngestion error = %v", err)
	}
	if got := again.Conversation[0].Content; got != firstPass.Content {
		t.Fatalf("content after second PostIngestion = %q, want frozen %q", got, firstPass.Content)
	}
}
