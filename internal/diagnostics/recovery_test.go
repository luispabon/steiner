package diagnostics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openFileMatchesPath reports whether the writer's descriptor for kind is the
// file the active path names. A stale descriptor appends to an unlinked inode
// that no reader can see, and a missing active path matches nothing.
func openFileMatchesPath(t *testing.T, w *Writer, kind Kind) bool {
	t.Helper()
	s := w.files[kind]
	if s == nil || s.file == nil {
		t.Fatalf("stream %s has no open file", kind)
	}
	open, err := s.file.Stat()
	if err != nil {
		t.Fatalf("stat open %s: %v", kind, err)
	}
	active, err := os.Stat(s.path)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatalf("stat %s: %v", s.path, err)
	}
	return os.SameFile(open, active)
}

// readStream returns the contents of one stream file, where generation 0 is the
// active file.
func readStream(t *testing.T, path string, generation int) string {
	t.Helper()
	p := path
	if generation > 0 {
		p = fmt.Sprintf("%s.%d", path, generation)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(data)
}

// readTailRecords parses the records that follow the blank-line padding written
// by padToCap. Splitting the whole padded file instead would build millions of
// empty lines.
func readTailRecords(t *testing.T, path string) []Record {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	trimmed := strings.TrimLeft(string(data), "\n")
	var out []Record
	for _, line := range strings.Split(strings.TrimSuffix(trimmed, "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal tail line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// padToCap fills the active stream file with blank lines up to the rotation cap,
// so a test can cross the cap without writing megabytes of records.
func padToCap(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Repeat("\n", maxStreamBytes)), 0o600); err != nil {
		t.Fatalf("pad %s: %v", path, err)
	}
}

// blockRotation makes every generation name a non-empty directory, so a
// rotation can neither shift a generation nor rename the active file.
func blockRotation(t *testing.T, path string) {
	t.Helper()
	for generation := 1; generation <= maxStreamGenerations; generation++ {
		blocker := fmt.Sprintf("%s.%d", path, generation)
		if err := os.Mkdir(blocker, 0o700); err != nil {
			t.Fatalf("create blocker %s: %v", blocker, err)
		}
		if err := os.WriteFile(filepath.Join(blocker, "keep"), []byte("x"), 0o600); err != nil {
			t.Fatalf("seed blocker %s: %v", blocker, err)
		}
	}
}

// unblockRotation removes the directories blockRotation created.
func unblockRotation(t *testing.T, path string) {
	t.Helper()
	for generation := 1; generation <= maxStreamGenerations; generation++ {
		p := fmt.Sprintf("%s.%d", path, generation)
		if err := os.RemoveAll(p); err != nil {
			t.Fatalf("remove blocker %s: %v", p, err)
		}
	}
}

func TestWriterReopensAfterRetentionRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.jsonl")
	seedStream(t, path, 90*24*time.Hour)

	w, err := New(Options{Dir: dir, Streams: Streams{Cache: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if !openFileMatchesPath(t, w, KindCache) {
		t.Fatal("setup: writer descriptor is not the active file")
	}

	// Another process pruning the stream rewrites the active file through a
	// temp file and rename, leaving this writer on the replaced inode.
	pruner, err := New(Options{Dir: dir, Streams: Streams{Cache: true}, RetentionDays: 30})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := pruner.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if openFileMatchesPath(t, w, KindCache) {
		t.Fatal("setup: retention did not replace the active file")
	}

	w.Write(Record{Kind: KindCache, AgentID: "after-rewrite"})

	if !openFileMatchesPath(t, w, KindCache) {
		t.Error("stream still writes to the replaced file, want a reopened descriptor")
	}
	records := readRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "after-rewrite" {
		t.Fatalf("records = %+v, want only the record written after the rewrite", records)
	}
}

func TestWriterReopensAfterManualRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	w.Write(Record{Kind: KindTool, AgentID: "before"})
	moved := path + ".1"
	if err := os.Rename(path, moved); err != nil {
		t.Fatalf("rename active stream: %v", err)
	}
	if openFileMatchesPath(t, w, KindTool) {
		t.Fatal("setup: the descriptor still matches the active path")
	}

	w.Write(Record{Kind: KindTool, AgentID: "after"})

	if !openFileMatchesPath(t, w, KindTool) {
		t.Error("stream still writes to the renamed file, want a reopened active file")
	}
	active := readRecords(t, path)
	if len(active) != 1 || active[0].AgentID != "after" {
		t.Fatalf("active records = %+v, want only the record written after the rename", active)
	}
	renamed := readRecords(t, moved)
	if len(renamed) != 1 || renamed[0].AgentID != "before" {
		t.Fatalf("renamed records = %+v, want the record written before the rename", renamed)
	}
}

func TestWriterRecoversFromFailedRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	blockRotation(t, path)
	padToCap(t, path)

	w.Write(Record{Kind: KindTool, AgentID: "past-cap"})

	if s := w.files[KindTool]; s.file == nil {
		t.Fatal("stream left closed after a failed rotation, want appends to continue")
	}
	if !w.warned {
		t.Error("warned = false, want the rotation failure reported once")
	}
	records := readTailRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "past-cap" {
		t.Fatalf("tail records = %+v, want the record appended past the cap", records)
	}

	// With the obstruction gone, a writer that retried on every record would
	// rotate and shift the generations immediately. The failed rotation defers
	// the next attempt by another cap worth of appends, so these records append
	// past the cap instead.
	unblockRotation(t, path)
	for i := 0; i < 5; i++ {
		w.Write(Record{Kind: KindTool, AgentID: fmt.Sprintf("suppressed-%d", i)})
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("generation 1 stat error = %v, want rotation suppressed until another cap is written", err)
	}
	if got := len(readTailRecords(t, path)); got != 6 {
		t.Errorf("tail records = %d, want every record appended past the cap", got)
	}

	// The suppression is bounded: after another cap worth of appends the
	// deferred rotation runs.
	payload := strings.Repeat("x", 64*1024)
	for i := 0; i < maxStreamBytes/len(payload)+40; i++ {
		w.Write(Record{Kind: KindTool, Payload: payload})
	}

	generation, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("stat generation 1: %v, want the deferred rotation to land", err)
	}
	if !generation.Mode().IsRegular() {
		t.Fatalf("generation 1 mode = %v, want a rotated stream file", generation.Mode())
	}
	active, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat active stream: %v", err)
	}
	if active.Size() >= maxStreamBytes {
		t.Errorf("active size = %d, want a fresh file below the cap", active.Size())
	}
}

func TestWriterWriteAfterCloseIsDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	w.Write(Record{Kind: KindTool, AgentID: "after-close"})

	if s := w.files[KindTool]; s.file != nil {
		t.Error("stream reopened after Close, want the record dropped")
	}
	if got := readStream(t, path, 0); got != "" {
		t.Errorf("active file = %q, want nothing written after Close", got)
	}
}
