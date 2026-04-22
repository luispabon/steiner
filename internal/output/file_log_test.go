package output

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileLogSinkWritesUpdatedEventModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.log")

	sink, err := NewFileLogSink(path)
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
