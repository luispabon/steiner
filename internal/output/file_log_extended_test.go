package output

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewFileLogSinkWithEmptyPath(t *testing.T) {
	sink, err := NewFileLogSink("", false)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
	if sink != nil {
		t.Fatal("sink should be nil on error")
	}
	if err.Error() != "log file path is required" {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestNewFileLogSinkWithWhitespacePath(t *testing.T) {
	sink, err := NewFileLogSink("   ", false)
	if err == nil {
		t.Fatal("expected error for whitespace path")
	}
	if sink != nil {
		t.Fatal("sink should be nil on error")
	}
}

func TestNewFileLogSinkSuccess(t *testing.T) {
	tmpdir := t.TempDir()
	path := filepath.Join(tmpdir, "test.log")

	sink, err := NewFileLogSink(path, false)
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	if sink == nil {
		t.Fatal("sink should not be nil")
	}
	defer func() { _ = sink.Close() }()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log file not created: %v", err)
	}
}

func TestFileLogSinkEmitWithNilReceiver(_ *testing.T) {
	var sink *FileLogSink
	sink.Emit(Event{Type: EventTypeRunStarted})
}

func TestFileLogSinkEmitAfterClose(t *testing.T) {
	tmpdir := t.TempDir()
	path := filepath.Join(tmpdir, "test.log")

	sink, err := NewFileLogSink(path, false)
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}

	err = sink.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	sink.Emit(Event{Type: EventTypeRunStarted})
}

func TestFileLogSinkEmitThinkingChunk(t *testing.T) {
	tmpdir := t.TempDir()

	tests := []struct {
		name          string
		thinkingChunk bool
		checkFile     func(t *testing.T, path string)
	}{
		{
			name:          "thinking disabled - should not write",
			thinkingChunk: false,
			checkFile: func(t *testing.T, path string) {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatalf("stat: %v", err)
				}
				if info.Size() != 0 {
					t.Errorf("file should be empty, but size = %d", info.Size())
				}
			},
		},
		{
			name:          "thinking enabled - should write",
			thinkingChunk: true,
			checkFile: func(t *testing.T, path string) {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatalf("stat: %v", err)
				}
				if info.Size() == 0 {
					t.Error("file should not be empty")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset path for this test
			testPath := filepath.Join(tmpdir, tt.name+".log")
			sink, err := NewFileLogSink(testPath, tt.thinkingChunk)
			if err != nil {
				t.Fatalf("NewFileLogSink() error = %v", err)
			}
			defer func() { _ = sink.Close() }()

			event := NewThinkingChunkEventWithSource(1, "test thinking", ChunkSourceAssistant)
			sink.Emit(event)

			tt.checkFile(t, testPath)
		})
	}
}

func TestFileLogSinkEmitUserInput(t *testing.T) {
	tmpdir := t.TempDir()
	path := filepath.Join(tmpdir, "test.log")

	sink, err := NewFileLogSink(path, false)
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	defer func() { _ = sink.Close() }()

	event := NewUserInputEvent("test content", "interactive", nil)
	sink.Emit(event)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if len(content) == 0 {
		t.Error("file should not be empty after emit")
	}
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
				_, isNoop := sink.(NoopSink)
				if !isNoop {
					t.Fatalf("expected NoopSink, got %T", sink)
				}
			},
		},
		{
			name:  "one sink",
			sinks: []EventSink{NoopSink{}},
			check: func(t *testing.T, sink EventSink) {
				_, isNoop := sink.(NoopSink)
				if !isNoop {
					t.Fatalf("expected NoopSink, got %T", sink)
				}
			},
		},
		{
			name:  "multiple sinks",
			sinks: []EventSink{NoopSink{}, NoopSink{}},
			check: func(t *testing.T, sink EventSink) {
				_, isMulti := sink.(MultiSink)
				if !isMulti {
					t.Fatalf("expected MultiSink, got %T", sink)
				}
			},
		},
		{
			name:  "nil sinks are filtered",
			sinks: []EventSink{nil, NoopSink{}, nil},
			check: func(t *testing.T, sink EventSink) {
				_, isNoop := sink.(NoopSink)
				if !isNoop {
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

	sink1, err := NewFileLogSink(path1, false)
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	defer func() { _ = sink1.Close() }()

	sink2, err := NewFileLogSink(path2, false)
	if err != nil {
		t.Fatalf("NewFileLogSink() error = %v", err)
	}
	defer func() { _ = sink2.Close() }()

	multi := NewMultiSink(sink1, sink2)

	event := NewAssistantMessageEvent(1, "assistant", "test")
	multi.Emit(event)

	for _, path := range []string{path1, path2} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Size() == 0 {
			t.Errorf("file %s should not be empty", path)
		}
	}
}
