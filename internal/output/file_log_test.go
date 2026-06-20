package output

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileLogSinkWritesUpdatedEventModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.log")

	sink, err := NewFileLogSink(path, true)
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	t.Cleanup(func() {
		_ = sink.Close()
	})

	sink.Emit(NewRunStartedEvent("exec", "test-model", "fix the bug", 4, 64))
	sink.Emit(NewUserInputEvent("fix the bug", "exec"))
	sink.Emit(NewRunFinishedEvent(1, "complete", "run complete after 1 turn", "", nil))

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	got := string(data)
	for _, want := range []string{
		"=== ",
		"run_started",
		`"mode": "exec"`,
		`"prompt": "fix the bug"`,
		"mode: exec",
		"fix the bug",
		"run_finished",
		`"summary": "run complete after 1 turn"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("log output missing %q\n%s", want, got)
		}
	}
}

func TestNewFileLogSinkEmptyPath(t *testing.T) {
	_, err := NewFileLogSink("", false)
	if err == nil {
		t.Fatal("NewFileLogSink(\"\") expected error")
	}
	_, err = NewFileLogSink("  ", false)
	if err == nil {
		t.Fatal("NewFileLogSink(\"  \") expected error")
	}
}

func TestNewFileLogSinkCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "nested", "session.log")
	sink, err := NewFileLogSink(path, true)
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

func TestFileLogSinkEmitUserInputEvent(t *testing.T) {
	t.Run("with mode", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session.log")
		sink, err := NewFileLogSink(path, true)
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })
		sink.Emit(NewUserInputEvent("fix the bug", "exec"))
		data, _ := os.ReadFile(path)
		got := string(data)
		if !strings.Contains(got, "mode: exec") {
			t.Fatalf("user input log missing mode line\n%s", got)
		}
		if !strings.Contains(got, "fix the bug") {
			t.Fatalf("user input log missing content\n%s", got)
		}
	})
	t.Run("without mode", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session.log")
		sink, err := NewFileLogSink(path, true)
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })
		sink.Emit(NewUserInputEvent("just text", ""))
		data, _ := os.ReadFile(path)
		got := string(data)
		if strings.Contains(got, "mode:") {
			t.Fatalf("user input log should not contain mode line\n%s", got)
		}
		if !strings.Contains(got, "just text") {
			t.Fatalf("user input log missing content\n%s", got)
		}
	})
}

func TestFileLogSinkEmitDefaultPayload(t *testing.T) {
	t.Run("JSON serializable payload", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session.log")
		sink, err := NewFileLogSink(path, true)
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })
		sink.Emit(NewRunStartedEvent("exec", "test-model", "test prompt", 5, 64))
		data, _ := os.ReadFile(path)
		got := string(data)
		if !strings.Contains(got, `"mode": "exec"`) {
			t.Fatalf("default payload log missing mode\n%s", got)
		}
	})
	t.Run("non-JSON-serializable payload uses fallback", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session.log")
		sink, err := NewFileLogSink(path, true)
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })
		sink.Emit(Event{
			Type:      "test",
			Timestamp: time.Now().UTC(),
			Payload:   map[string]any{"ch": make(chan int)},
		})
		data, _ := os.ReadFile(path)
		got := string(data)
		if !strings.Contains(got, "===") || !strings.Contains(got, "test") {
			t.Fatalf("fallback output didn't write expected content\n%s", got)
		}
	})
}

func TestFileLogSinkThinkingChunkSuppression(t *testing.T) {
	t.Run("suppressed when flag is false", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session.log")
		sink, err := NewFileLogSink(path, false)
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })

		sink.Emit(NewThinkingChunkEventWithSource(1, "some reasoning", ChunkSourceAssistant))
		data, _ := os.ReadFile(path)
		if len(data) != 0 {
			t.Fatalf("expected empty file when thinking_chunk=false, got:\n%s", data)
		}
	})

	t.Run("emitted when flag is true", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session.log")
		sink, err := NewFileLogSink(path, true)
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })

		sink.Emit(NewThinkingChunkEventWithSource(1, "some reasoning", ChunkSourceAssistant))
		data, _ := os.ReadFile(path)
		got := string(data)
		if !strings.Contains(got, "thinking_chunk") {
			t.Fatalf("expected thinking_chunk in log, got:\n%s", got)
		}
		if !strings.Contains(got, "some reasoning") {
			t.Fatalf("expected thinking content in log, got:\n%s", got)
		}
	})

	t.Run("non-thinking events pass through regardless", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session.log")
		sink, err := NewFileLogSink(path, false)
		if err != nil {
			t.Fatalf("NewFileLogSink() error = %v", err)
		}
		t.Cleanup(func() { _ = sink.Close() })

		sink.Emit(NewRunStartedEvent("exec", "test-model", "test prompt", 5, 64))
		data, _ := os.ReadFile(path)
		got := string(data)
		if !strings.Contains(got, "run_started") {
			t.Fatalf("expected non-thinking event to pass through, got:\n%s", got)
		}
	})
}
