package history

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
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

func TestNewWriter_CreatesDirAndFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "nested", "history.log")

	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

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

	for i := 0; i < 55; i++ {
		if err := w.Record(fmt.Sprintf("prompt-%d", i)); err != nil {
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

	want := make([]string, 0, 50)
	for i := 5; i < 55; i++ {
		want = append(want, fmt.Sprintf("prompt-%d", i))
	}
	if !slices.Equal(prompts, want) {
		t.Errorf("retained prompts = %v, want the newest 50 in original order %v", prompts, want)
	}
}

func TestLoad_ReturnsPrompts(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())

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

	prompts, err := w.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prompts) != 0 {
		t.Errorf("got %d prompts, want 0", len(prompts))
	}
}

func TestLoad_CapsAtMaxEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.log")

	var sb strings.Builder
	for i := 0; i < 70; i++ {
		fmt.Fprintf(&sb, "2024-01-01T00:00:00Z\tprompt-%d\n", i)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	prompts, err := w.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prompts) != 50 {
		t.Fatalf("got %d prompts, want 50", len(prompts))
	}
	want := make([]string, 0, 50)
	for i := 20; i < 70; i++ {
		want = append(want, fmt.Sprintf("prompt-%d", i))
	}
	if !slices.Equal(prompts, want) {
		t.Errorf("retained prompts = %v, want %v", prompts, want)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())

	if err := os.Remove(w.Path()); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	prompts, err := w.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prompts) != 0 {
		t.Errorf("got %d prompts, want 0", len(prompts))
	}
}

func TestRecord_TwoWritersSamePathLoseNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.log")

	a, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter a: %v", err)
	}
	b, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter b: %v", err)
	}

	for i := 0; i < 60; i++ {
		if err := a.Record(fmt.Sprintf("seed-%d", i)); err != nil {
			t.Fatalf("Record seed(%d): %v", i, err)
		}
	}

	var want []string
	// After the 60 seeds the file retains the newest 50 (seed-10..seed-59).
	// Appending the 10 alternating entries trims the oldest 10 again, so the
	// final 50 are seed-20..seed-59 followed by the alternating entries.
	for i := 20; i < 60; i++ {
		want = append(want, fmt.Sprintf("seed-%d", i))
	}
	for i := 0; i < 5; i++ {
		promptA := fmt.Sprintf("A%d", i)
		if err := a.Record(promptA); err != nil {
			t.Fatalf("Record A%d: %v", i, err)
		}
		want = append(want, promptA)

		promptB := fmt.Sprintf("B%d", i)
		if err := b.Record(promptB); err != nil {
			t.Fatalf("Record B%d: %v", i, err)
		}
		want = append(want, promptB)
	}

	c, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter c: %v", err)
	}
	got, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 50 {
		t.Fatalf("got %d prompts, want 50", len(got))
	}
	if !slices.Equal(got, want) {
		t.Errorf("prompts = %v, want %v", got, want)
	}
}

func TestRecord_ConcurrentWritersGoroutines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.log")

	const numWriters = 4
	const numPrompts = 40

	submitted := make(map[string]bool)
	var mu sync.Mutex

	var wg sync.WaitGroup
	for wi := 0; wi < numWriters; wi++ {
		w, err := NewWriter(path)
		if err != nil {
			t.Fatalf("NewWriter(%d): %v", wi, err)
		}
		wg.Add(1)
		go func(wi int, w *Writer) {
			defer wg.Done()
			for i := 0; i < numPrompts; i++ {
				prompt := fmt.Sprintf("writer-%d-seq-%d", wi, i)
				if err := w.Record(prompt); err != nil {
					t.Errorf("Record(writer=%d, seq=%d): %v", wi, i, err)
					return
				}
				mu.Lock()
				submitted[prompt] = true
				mu.Unlock()
			}
		}(wi, w)
	}
	wg.Wait()

	fresh, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter fresh: %v", err)
	}
	prompts, err := fresh.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prompts) != 50 {
		t.Fatalf("got %d prompts, want 50", len(prompts))
	}
	for _, p := range prompts {
		if !submitted[p] {
			t.Errorf("loaded prompt %q was never submitted", p)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 50 {
		t.Errorf("file has %d lines, want 50", len(lines))
	}
}

func TestRecord_NoTmpFilesLeftBehind(t *testing.T) {
	dir := t.TempDir()
	w := mustOpenWriter(t, dir)

	for i := 0; i < 120; i++ {
		if err := w.Record(fmt.Sprintf("prompt-%d", i)); err != nil {
			t.Fatalf("Record(%d): %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	want := []string{"history.log", "history.log.lock"}
	if !slices.Equal(names, want) {
		t.Errorf("directory entries = %v, want %v", names, want)
	}
}

func TestRecord_PreservesFileMode(t *testing.T) {
	w := mustOpenWriter(t, t.TempDir())

	for i := 0; i < 60; i++ {
		if err := w.Record(fmt.Sprintf("prompt-%d", i)); err != nil {
			t.Fatalf("Record(%d): %v", i, err)
		}
	}

	info, err := os.Stat(w.Path())
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := info.Mode(); mode.Perm() != 0o644 {
		t.Errorf("file mode = %v, want 0644", mode.Perm())
	}
}
