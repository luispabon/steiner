package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestBaseContextManagerCachedSystemPreamble(t *testing.T) {
	t.Run("cache hit with same parameters", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("alpha", false, false, false, "")
		second := manager.CachedSystemPreamble("alpha", false, false, false, "")
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second != first {
			t.Fatalf("second preamble = %q, want cached %q", second, first)
		}
	})

	t.Run("cache miss with different cavemanMode", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", true, false, false, "")
		second := manager.CachedSystemPreamble("", true, true, false, "")
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second == first {
			t.Fatal("second preamble should differ when cavemanMode changes")
		}
	})

	t.Run("cache miss with different override", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("alpha", false, false, false, "")
		second := manager.CachedSystemPreamble("beta", false, false, false, "")
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second == first {
			t.Fatal("second preamble should differ when override changes")
		}
	})

	t.Run("cache miss with different systemSuffix", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", false, false, false, "")
		second := manager.CachedSystemPreamble("", false, false, false, "Extended thinking enabled")
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
		first := manager.CachedSystemPreamble("", false, false, false, suffix)
		second := manager.CachedSystemPreamble("", false, false, false, suffix)
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second != first {
			t.Fatalf("second preamble should be cached when suffix is same")
		}
	})

	t.Run("cache miss when suffix changes from non-empty to empty", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", false, false, false, "Some instruction")
		second := manager.CachedSystemPreamble("", false, false, false, "")
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

	t.Run("cache miss with different humanizerMode", func(t *testing.T) {
		var manager baseContextManager
		first := manager.CachedSystemPreamble("", true, false, false, "")
		second := manager.CachedSystemPreamble("", true, false, true, "")
		if first == "" {
			t.Fatal("first preamble = empty, want content")
		}
		if second == first {
			t.Fatal("second preamble should differ when humanizerMode changes")
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
			t.Cleanup(func() { _ = os.Chdir(oldWD) })

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
			t.Cleanup(func() { _ = os.Chdir(oldWD) })

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
			payload, ok := events[len(events)-1].Payload.(output.ContextDiagnosticsEvent)
			if !ok {
				t.Fatalf("payload type = %T, want ContextDiagnosticsEvent", events[len(events)-1].Payload)
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
