package history

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func mustOpenWriter(t *testing.T, dir string) *Writer {
	t.Helper()
	path := filepath.Join(dir, "history.log")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter(%q): %v", path, err)
	}
	return w
}

func closeWriter(t *testing.T, w *Writer) {
	t.Helper()
	if err := w.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestNewWriter_CreatesDirAndFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "nested", "history.log")

	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	if w.Path() != path {
		t.Errorf("Path() = %q, want %q", w.Path(), path)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if info.IsDir() {
		t.Errorf("%s is a directory, want regular file", path)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if !dirInfo.IsDir() {
		t.Errorf("parent path is not a directory")
	}
}

func TestRecord_EmptyPrompt(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())
	defer func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	if err := w.Record(""); err != nil {
		t.Fatalf("Record empty: %v", err)
	}

	info, err := os.Stat(w.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("file size = %d, want 0", info.Size())
	}
}

func TestRecord_WritesProperLineFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.log")

	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	prompt := "user query"
	if err := w.Record(prompt); err != nil {
		t.Fatalf("Record: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	line := strings.TrimSuffix(string(raw), "\n")

	parts := strings.SplitN(line, "\t", 2)
	if len(parts) != 2 {
		t.Fatalf("line = %q, expected tab-separated timestamp and prompt", line)
	}

	rfc3339 := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})$`)
	if !rfc3339.MatchString(parts[0]) {
		t.Errorf("timestamp = %q, doesn't match RFC3339", parts[0])
	}

	if parts[1] != prompt {
		t.Errorf("prompt part = %q, want %q", parts[1], prompt)
	}
}

func TestRecord_EscapesSpecialChars(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())
	defer func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	prompt := "col1\tcol2\nline2"
	if err := w.Record(prompt); err != nil {
		t.Fatalf("Record: %v", err)
	}

	raw, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	line := strings.TrimSuffix(string(raw), "\n")
	parts := strings.SplitN(line, "\t", 2)
	if len(parts) != 2 {
		t.Fatalf("line = %q, expected tab-separated", line)
	}

	escaped := parts[1]
	if !strings.Contains(escaped, "\\t") {
		t.Errorf("escaped prompt doesn't contain literal \\t: %q", escaped)
	}
	if !strings.Contains(escaped, "\\n") {
		t.Errorf("escaped prompt doesn't contain literal \\n: %q", escaped)
	}
}

func TestRecord_TrimsAfterWrite(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())
	defer func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	for i := 0; i < 55; i++ {
		if err := w.Record("prompt"); err != nil {
			t.Fatalf("Record(%d): %v", i, err)
		}
	}

	prompts, err := w.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prompts) != 50 {
		t.Fatalf("got %d prompts, want 50", len(prompts))
	}
}

func TestTrimAfterAppend_NoOp(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())
	defer func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	if err := w.Record("hello"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if err := w.TrimAfterAppend(0); err != nil {
		t.Errorf("TrimAfterAppend(0): %v", err)
	}
	if err := w.TrimAfterAppend(-1); err != nil {
		t.Errorf("TrimAfterAppend(-1): %v", err)
	}

	prompts, err := w.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prompts) != 1 {
		t.Errorf("got %d prompts, want 1", len(prompts))
	}
}

func TestTrimAfterAppend_Truncates(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())
	defer closeWriter(t, w)

	for i := 0; i < 10; i++ {
		if err := w.Record("line"); err != nil {
			t.Fatalf("Record(%d): %v", i, err)
		}
	}

	if err := w.TrimAfterAppend(3); err != nil {
		t.Fatalf("TrimAfterAppend(3): %v", err)
	}

	prompts, err := w.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prompts) != 3 {
		t.Errorf("got %d prompts, want 3", len(prompts))
	}
}

func TestTrimAfterAppend_NoTruncateWhenUnderMax(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())
	defer closeWriter(t, w)

	for i := 0; i < 3; i++ {
		if err := w.Record("line"); err != nil {
			t.Fatalf("Record(%d): %v", i, err)
		}
	}

	if err := w.TrimAfterAppend(10); err != nil {
		t.Fatalf("TrimAfterAppend(10): %v", err)
	}

	prompts, err := w.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prompts) != 3 {
		t.Errorf("got %d prompts, want 3", len(prompts))
	}
}

func TestLoad_ReturnsPrompts(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())
	defer closeWriter(t, w)

	prompts := []string{"first query", "second query", "third query"}
	for _, p := range prompts {
		if err := w.Record(p); err != nil {
			t.Fatalf("Record(%q): %v", p, err)
		}
	}

	got, err := w.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(prompts) {
		t.Fatalf("got %d prompts, want %d", len(got), len(prompts))
	}
	for i := range prompts {
		if got[i] != prompts[i] {
			t.Errorf("prompts[%d] = %q, want %q", i, got[i], prompts[i])
		}
	}
}

func TestLoad_UnescapesSpecialChars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	data := []byte("2024-01-01T00:00:00Z\tcol1\\tcol2\\nline2\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer closeWriter(t, w)

	prompts, err := w.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prompts) != 1 {
		t.Fatalf("got %d prompts, want 1", len(prompts))
	}

	want := "col1\tcol2\nline2"
	if prompts[0] != want {
		t.Errorf("prompt = %q, want %q", prompts[0], want)
	}
}

func TestLoad_SkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	data := []byte("2024-01-01T00:00:00Z\tvalid1\nno-tab-separator\n2024-01-01T00:00:01Z\tvalid2\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer closeWriter(t, w)

	prompts, err := w.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("got %d prompts, want 2", len(prompts))
	}
	if prompts[0] != "valid1" {
		t.Errorf("prompts[0] = %q, want %q", prompts[0], "valid1")
	}
	if prompts[1] != "valid2" {
		t.Errorf("prompts[1] = %q, want %q", prompts[1], "valid2")
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())
	defer closeWriter(t, w)

	prompts, err := w.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prompts) != 0 {
		t.Errorf("got %d prompts, want 0", len(prompts))
	}
}

func TestClose_Idempotent(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())

	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestTrimAfterAppend_FileHandleValidAndPersistsAfterTrim(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())
	defer closeWriter(t, w)

	for i := 0; i < 10; i++ {
		if err := w.Record("line"); err != nil {
			t.Fatalf("Record(%d): %v", i, err)
		}
	}

	// Force the actual truncate/rename/reopen path: Record() always calls
	// TrimAfterAppend(50), which no-ops while under 50 lines, so it alone
	// never exercises the rename+reopen logic this test targets.
	if err := w.TrimAfterAppend(3); err != nil {
		t.Fatalf("TrimAfterAppend(3): %v", err)
	}

	if w.file == nil {
		t.Fatal("w.file is nil after successful trim, want valid handle")
	}

	// Confirm the handle left behind by trim is the live file backing
	// w.path, not a stale handle to the pre-rename (now unlinked) file.
	if err := w.Record("post-trim"); err != nil {
		t.Fatalf("Record after trim: %v", err)
	}

	prompts, err := w.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prompts) == 0 || prompts[len(prompts)-1] != "post-trim" {
		t.Fatalf("post-trim write did not land on disk, got %v", prompts)
	}

	raw, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", w.Path(), err)
	}
	if !strings.Contains(string(raw), "post-trim") {
		t.Errorf("history file on disk does not contain post-trim write: %q", raw)
	}
}

func TestRecord_NilFileHandleReturnsError(t *testing.T) {
	w := &Writer{path: filepath.Join(t.TempDir(), "history.log")}

	err := w.Record("anything")
	if err == nil {
		t.Fatal("Record() with nil file handle: got nil error, want error")
	}
	if !strings.Contains(err.Error(), "file handle unavailable") {
		t.Errorf("Record() error = %q, want mention of unavailable file handle", err.Error())
	}
}

func TestClose_ClosesFile(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())
	path := w.Path()

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("file was deleted on Close")
	}
}
