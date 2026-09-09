package output

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestNewFileLogSinkEmptyPath(t *testing.T) {
	if _, err := NewFileLogSink("", FileLogOptions{}); err == nil {
		t.Fatal("NewFileLogSink(\"\") expected error")
	}
	if _, err := NewFileLogSink("  ", FileLogOptions{}); err == nil {
		t.Fatal("NewFileLogSink(\"  \") expected error")
	}
}

func TestNewFileLogSinkCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "nested", "session.log")
	sink, err := NewFileLogSink(path, FileLogOptions{})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sink.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("file was not created at %s", path)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat log directory: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("log directory mode = %o, want 700", info.Mode().Perm())
	}
}

func TestFileLogSinkUpgradesExistingFileWithoutChangingParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.log")
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("create log file: %v", err)
	}

	sink, err := NewFileLogSink(path, FileLogOptions{})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	if info, err := os.Stat(path); err != nil {
		t.Fatalf("stat log file: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", info.Mode().Perm())
	}
	if info, err := os.Stat(dir); err != nil {
		t.Fatalf("stat parent: %v", err)
	} else if info.Mode().Perm() != 0o755 {
		t.Errorf("parent mode = %o, want unchanged 755", info.Mode().Perm())
	}
}

func TestFileLogSinkEmitWithNilReceiver(_ *testing.T) {
	var sink *FileLogSink
	sink.Emit(Event{Type: EventTypeRunStarted})
}

func TestFileLogSinkEmitAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	sink, err := NewFileLogSink(path, FileLogOptions{})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	// Must not panic once the file handle is gone.
	sink.Emit(Event{Type: EventTypeRunStarted})
}

// readLines reads path and returns each non-empty line unmarshaled into an
// Event, failing the test if any line is not valid JSON.
func readLines(t *testing.T, path string) []Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var events []Event
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line is not valid JSON: %v\nline: %s", err, line)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan error: %v", err)
	}
	return events
}

func TestFileLogSinkWritesJSONLWithLogStartedFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.log")
	sink, err := NewFileLogSink(path, FileLogOptions{
		BuildSHA: "abc123",
		Version:  "1.2.3",
	})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	sink.Emit(NewRunStartedEvent("exec", "test-model", "fix the bug", 4, 64))
	sink.Emit(NewUserInputEvent("fix the bug", "exec", nil))
	sink.Emit(NewRunFinishedEvent(1, "complete", "run complete after 1 turn", "", nil))

	events := readLines(t, path)
	if len(events) != 4 {
		t.Fatalf("got %d lines, want 4 (log_started + 3 events)", len(events))
	}
	if events[0].Type != EventTypeLogStarted {
		t.Fatalf("first line type = %q, want %q", events[0].Type, EventTypeLogStarted)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(raw), `"build_sha":"abc123"`) {
		t.Fatalf("log_started line missing build_sha\n%s", raw)
	}

	if events[1].Type != EventTypeRunStarted {
		t.Fatalf("second line type = %q, want %q", events[1].Type, EventTypeRunStarted)
	}
	if events[2].Type != EventTypeUserInput {
		t.Fatalf("third line type = %q, want %q", events[2].Type, EventTypeUserInput)
	}
	if events[3].Type != EventTypeRunFinished {
		t.Fatalf("fourth line type = %q, want %q", events[3].Type, EventTypeRunFinished)
	}
}

func TestFileLogSinkPreservesEventScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.log")
	sink, err := NewFileLogSink(path, FileLogOptions{})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	scoped := WithAgentTypeScope(WithAgentScope(NewAssistantMessageEvent(1, "assistant", "hi"), "child-1"), "code")
	sink.Emit(scoped)

	events := readLines(t, path)
	var found bool
	for _, event := range events {
		if event.Type != EventTypeAssistantMessage {
			continue
		}
		found = true
		if event.Scope.AgentID != "child-1" {
			t.Fatalf("Scope.AgentID = %q, want %q", event.Scope.AgentID, "child-1")
		}
		if event.Scope.AgentType != "code" {
			t.Fatalf("Scope.AgentType = %q, want %q", event.Scope.AgentType, "code")
		}
	}
	if !found {
		t.Fatal("assistant_message event not found in log")
	}
}

func TestFileLogSinkAppendsAcrossRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.log")

	sink1, err := NewFileLogSink(path, FileLogOptions{})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	sink1.Emit(NewRunStartedEvent("exec", "model-1", "first run", 1, 1))
	if err := sink1.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	sink2, err := NewFileLogSink(path, FileLogOptions{})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	t.Cleanup(func() { _ = sink2.Close() })
	sink2.Emit(NewRunStartedEvent("exec", "model-2", "second run", 1, 1))

	events := readLines(t, path)
	// log_started, run_started, log_started, run_started
	if len(events) != 4 {
		t.Fatalf("got %d lines, want 4 across two runs", len(events))
	}
	if events[0].Type != EventTypeLogStarted || events[2].Type != EventTypeLogStarted {
		t.Fatalf("expected log_started at the start of each run, got types %q and %q", events[0].Type, events[2].Type)
	}
}

func TestFileLogSinkAPIRequestBoundsPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.log")
	sink, err := NewFileLogSink(path, FileLogOptions{})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	event := NewAPIRequestEvent("test-model", nil, nil, nil, nil, prompt.ModelTokenBudget{}, 0, 0)
	sink.Emit(event)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	got := string(raw)
	for _, forbidden := range []string{`"messages"`, `"tools"`, `"blocks"`} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("log output should not contain %q with CaptureAPIRequestBodies off\n%s", forbidden, got)
		}
	}
}

func TestFileLogSinkAPIRequestCaptureBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.log")
	sink, err := NewFileLogSink(path, FileLogOptions{CaptureAPIRequestBodies: true})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	messages := []provider.Message{{Role: provider.MessageRoleUser, Content: "remember this sentence"}}
	sink.Emit(NewAPIRequestEvent("test-model", messages, nil, nil, nil, prompt.ModelTokenBudget{}, 0, 0))

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, `"messages"`) || !strings.Contains(got, "remember this sentence") {
		t.Fatalf("log output should contain full messages with CaptureAPIRequestBodies on\n%s", got)
	}
	if !strings.Contains(got, `"message_count":1`) {
		t.Fatalf("log output should keep the bounded scalars alongside the bodies\n%s", got)
	}
}

func TestFileLogSinkEmitDefaultPayload(t *testing.T) {
	t.Run("JSON serializable payload", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.log")
		sink, err := NewFileLogSink(path, FileLogOptions{})
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })
		sink.Emit(NewRunStartedEvent("exec", "test-model", "test prompt", 5, 64))
		raw, _ := os.ReadFile(path)
		if !strings.Contains(string(raw), `"mode":"exec"`) {
			t.Fatalf("default payload log missing mode\n%s", raw)
		}
	})

	t.Run("non-JSON-serializable payload is dropped", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.log")
		sink, err := NewFileLogSink(path, FileLogOptions{})
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })

		before := readLines(t, path)
		sink.Emit(Event{
			Type:      "test",
			Timestamp: time.Now().UTC(),
			Payload:   map[string]any{"ch": make(chan int)},
		})
		after := readLines(t, path)
		if len(after) != len(before) {
			t.Fatalf("expected non-marshalable event to be dropped, got %d lines (had %d before)", len(after), len(before))
		}
	})
}

func TestFileLogSinkThinkingChunkSuppression(t *testing.T) {
	t.Run("suppressed when flag is false", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.log")
		sink, err := NewFileLogSink(path, FileLogOptions{})
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })

		before := readLines(t, path)
		sink.Emit(NewThinkingChunkEventWithSource(1, "some reasoning", ChunkSourceAssistant))
		after := readLines(t, path)
		if len(after) != len(before) {
			t.Fatalf("expected thinking chunk to be dropped when thinking_chunk=false, got %d new lines", len(after)-len(before))
		}
	})

	t.Run("emitted when flag is true", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.log")
		sink, err := NewFileLogSink(path, FileLogOptions{ThinkingChunk: true})
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })

		sink.Emit(NewThinkingChunkEventWithSource(1, "some reasoning", ChunkSourceAssistant))
		raw, _ := os.ReadFile(path)
		got := string(raw)
		if !strings.Contains(got, "thinking_chunk") {
			t.Fatalf("expected thinking_chunk in log, got:\n%s", got)
		}
		if !strings.Contains(got, "some reasoning") {
			t.Fatalf("expected thinking content in log, got:\n%s", got)
		}
	})
}

func TestFileLogSinkAssistantChunkSuppression(t *testing.T) {
	t.Run("suppressed when flag is false", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.log")
		sink, err := NewFileLogSink(path, FileLogOptions{})
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })

		before := readLines(t, path)
		sink.Emit(NewAssistantChunkEventWithSource(1, "some content", ChunkSourceAssistant))
		after := readLines(t, path)
		if len(after) != len(before) {
			t.Fatalf("expected assistant chunk to be dropped when assistant_chunk=false, got %d new lines", len(after)-len(before))
		}
	})

	t.Run("emitted when flag is true", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.log")
		sink, err := NewFileLogSink(path, FileLogOptions{AssistantChunk: true})
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })

		sink.Emit(NewAssistantChunkEventWithSource(1, "some content", ChunkSourceAssistant))
		raw, _ := os.ReadFile(path)
		if !strings.Contains(string(raw), "some content") {
			t.Fatalf("expected assistant chunk content in log, got:\n%s", raw)
		}
	})
}

func TestFileLogSinkRotatesOnSizeCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.log")
	sink, err := NewFileLogSink(path, FileLogOptions{})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	// Force a tiny cap so a handful of records trigger rotation.
	sink.mu.Lock()
	sink.written = maxFileLogBytes
	sink.mu.Unlock()

	sink.Emit(NewRunStartedEvent("exec", "model", "trigger rotation", 1, 1))

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated generation at %s.1: %v", path, err)
	}
	events := readLines(t, path)
	if len(events) == 0 || events[0].Type != EventTypeLogStarted {
		t.Fatalf("expected new file to start with log_started, got %+v", events)
	}
}

func TestNewMultiSink(t *testing.T) {
	t.Run("nil sink returns NoopSink", func(t *testing.T) {
		s := NewMultiSink(nil)
		if _, ok := s.(NoopSink); !ok {
			t.Fatalf("NewMultiSink(nil) = %T, want NoopSink", s)
		}
	})
	t.Run("all nil returns NoopSink", func(t *testing.T) {
		s := NewMultiSink(nil, nil, nil)
		if _, ok := s.(NoopSink); !ok {
			t.Fatalf("NewMultiSink(nil, nil, nil) = %T, want NoopSink", s)
		}
	})
	t.Run("single sink returned directly", func(t *testing.T) {
		sink := &FileLogSink{}
		s := NewMultiSink(sink)
		if s != sink {
			t.Fatalf("NewMultiSink(single) should return the sink directly")
		}
	})
	t.Run("multiple sinks fan out events", func(t *testing.T) {
		var emitted1, emitted2 int
		sink1 := SinkFunc(func(Event) { emitted1++ })
		sink2 := SinkFunc(func(Event) { emitted2++ })
		s := NewMultiSink(sink1, sink2)
		s.Emit(NewStopReasonEvent(1, "complete", nil))
		if emitted1 != 1 || emitted2 != 1 {
			t.Fatalf("emitted1=%d, emitted2=%d, want both 1", emitted1, emitted2)
		}
	})
	t.Run("mixed nil and non-nil sinks", func(t *testing.T) {
		var emitted int
		sink1 := SinkFunc(func(Event) { emitted++ })
		s := NewMultiSink(nil, sink1, nil)
		s.Emit(NewStopReasonEvent(1, "complete", nil))
		if emitted != 1 {
			t.Fatalf("emitted=%d, want 1", emitted)
		}
	})
}

func TestNewMultiSinkTypes(t *testing.T) {
	tests := []struct {
		name  string
		sinks []EventSink
		check func(t *testing.T, sink EventSink)
	}{
		{
			name:  "no sinks",
			sinks: []EventSink{},
			check: func(t *testing.T, sink EventSink) {
				if _, ok := sink.(NoopSink); !ok {
					t.Fatalf("expected NoopSink, got %T", sink)
				}
			},
		},
		{
			name:  "one sink",
			sinks: []EventSink{NoopSink{}},
			check: func(t *testing.T, sink EventSink) {
				if _, ok := sink.(NoopSink); !ok {
					t.Fatalf("expected NoopSink, got %T", sink)
				}
			},
		},
		{
			name:  "multiple sinks",
			sinks: []EventSink{NoopSink{}, NoopSink{}},
			check: func(t *testing.T, sink EventSink) {
				if _, ok := sink.(MultiSink); !ok {
					t.Fatalf("expected MultiSink, got %T", sink)
				}
			},
		},
		{
			name:  "nil sinks are filtered",
			sinks: []EventSink{nil, NoopSink{}, nil},
			check: func(t *testing.T, sink EventSink) {
				if _, ok := sink.(NoopSink); !ok {
					t.Fatalf("expected NoopSink (single), got %T", sink)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := NewMultiSink(tt.sinks...)
			tt.check(t, sink)
		})
	}
}

func TestMultiSinkEmit(t *testing.T) {
	tmpdir := t.TempDir()
	path1 := filepath.Join(tmpdir, "sink1.log")
	path2 := filepath.Join(tmpdir, "sink2.log")

	sink1, err := NewFileLogSink(path1, FileLogOptions{})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	t.Cleanup(func() { _ = sink1.Close() })

	sink2, err := NewFileLogSink(path2, FileLogOptions{})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	t.Cleanup(func() { _ = sink2.Close() })

	multi := NewMultiSink(sink1, sink2)

	event := NewAssistantMessageEvent(1, "assistant", "test")
	multi.Emit(event)

	for _, path := range []string{path1, path2} {
		events := readLines(t, path)
		if len(events) < 2 {
			t.Fatalf("path %s: got %d lines, want at least 2 (log_started + event)", path, len(events))
		}
	}
}

func TestFileLogSinkPermissions(t *testing.T) {
	// Nest under a directory that does not exist yet so MkdirAll's mode is
	// actually exercised; t.TempDir() itself is already 0o700, which would
	// make the directory assertion pass for the wrong reason.
	path := filepath.Join(t.TempDir(), "nested", "session.log")
	sink, err := NewFileLogSink(path, FileLogOptions{})
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 0o600", info.Mode().Perm())
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o, want 0o700", dirInfo.Mode().Perm())
	}
}
