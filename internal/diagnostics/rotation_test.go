package diagnostics

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// sameOpenFile reports whether the writer's descriptor for kind still points at
// the file currently at the stream path. A stale descriptor appends to an
// unlinked inode no reader can see.
func sameOpenFile(t *testing.T, w *Writer, kind Kind) bool {
	t.Helper()
	s := w.files[kind]
	if s == nil || s.file == nil {
		t.Fatalf("stream %s has no open file", kind)
	}
	pathInfo, err := os.Stat(s.path)
	if err != nil {
		t.Fatalf("stat %s: %v", s.path, err)
	}
	fdInfo, err := s.file.Stat()
	if err != nil {
		t.Fatalf("stat open %s: %v", kind, err)
	}
	return os.SameFile(pathInfo, fdInfo)
}

// readGenerations reads the active stream file and every rotated generation
// that exists.
func readGenerations(t *testing.T, path string) []Record {
	t.Helper()
	var out []Record
	for i := 0; i <= maxStreamGenerations; i++ {
		p := path
		if i > 0 {
			p = fmt.Sprintf("%s.%d", path, i)
		}
		if _, err := os.Stat(p); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			t.Fatalf("stat %s: %v", p, err)
		}
		out = append(out, readRecords(t, p)...)
	}
	return out
}

func TestRetentionReplacesFileUnderOpenWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.jsonl")
	seedStream(t, path, 40*24*time.Hour)
	requireStreamLock(t, path)

	writer, err := New(Options{Dir: dir, Streams: Streams{Cache: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	if !sameOpenFile(t, writer, KindCache) {
		t.Fatal("setup: writer descriptor is not the active file")
	}

	// A second writer pruning the stream replaces the active file by rename, so
	// the first writer's descriptor is now stale.
	pruner, err := New(Options{Dir: dir, Streams: Streams{Cache: true}, RetentionDays: 30})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := pruner.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if sameOpenFile(t, writer, KindCache) {
		t.Fatal("setup: retention did not replace the active file")
	}

	writer.Write(Record{Kind: KindCache, AgentID: "after-retention"})

	if !sameOpenFile(t, writer, KindCache) {
		t.Error("writer kept a descriptor to the replaced file, want it reopened")
	}
	records := readRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "after-retention" {
		t.Fatalf("records = %+v, want only the record written after retention", records)
	}
}

func TestWriterReopensRemovedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove active stream: %v", err)
	}

	w.Write(Record{Kind: KindTool, AgentID: "recreated"})

	if !sameOpenFile(t, w, KindTool) {
		t.Error("writer kept the removed file open, want a fresh descriptor")
	}
	records := readRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "recreated" {
		t.Fatalf("records = %+v, want the record written after the file was removed", records)
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
		t.Error("stream file is open after Close, want the record dropped")
	}
	records := readRecords(t, path)
	if len(records) != 0 {
		t.Fatalf("records = %+v, want none written after Close", records)
	}
}

func TestWriterRecoversFromRotationFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// A non-empty directory at every shifted generation makes the active file's
	// rename fail: each shift needs to land on the next, blocked, generation.
	for _, gen := range []string{".1", ".2", ".3"} {
		blocker := path + gen
		if err := os.Mkdir(blocker, 0o700); err != nil {
			t.Fatalf("create blocker %s: %v", blocker, err)
		}
		if err := os.WriteFile(filepath.Join(blocker, "keep"), []byte("x"), 0o600); err != nil {
			t.Fatalf("seed blocker %s: %v", blocker, err)
		}
	}
	// Grow the active file to the cap without writing 20MB of records.
	if err := os.Truncate(path, maxStreamBytes); err != nil {
		t.Fatalf("truncate active stream: %v", err)
	}

	w.Write(Record{Kind: KindTool, AgentID: "dropped"})

	if w.files[KindTool].file != nil {
		t.Fatal("stream file is open after a failed rotation, want it left closed")
	}
	if !w.warned {
		t.Error("warned = false, want the rotation failure reported once")
	}

	for _, gen := range []string{".1", ".2", ".3"} {
		if err := os.RemoveAll(path + gen); err != nil {
			t.Fatalf("remove blocker %s: %v", path+gen, err)
		}
	}

	w.Write(Record{Kind: KindTool, AgentID: "recovered"})

	if !sameOpenFile(t, w, KindTool) {
		t.Error("writer kept a stale descriptor after recovering, want it reopened")
	}
	records := readRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "recovered" {
		t.Fatalf("records = %+v, want the record written after recovery", records)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("stat rotated generation: %v, want the retried rotation to land", err)
	}
}

func TestRepeatedRotationFailurePreservesGenerations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	seedStream(t, path+".1", 24*time.Hour)
	seedStream(t, path+".2", 2*24*time.Hour)
	seedStream(t, path+".3", 3*24*time.Hour)

	// The generation shift succeeds while the active rename fails, which is what
	// every retry after a blocked first attempt looks like.
	renameFile = func(oldpath, newpath string) error {
		if newpath == path+".1" {
			return errors.New("injected active rename failure")
		}
		return os.Rename(oldpath, newpath)
	}
	t.Cleanup(func() { renameFile = os.Rename })

	if err := os.Truncate(path, maxStreamBytes); err != nil {
		t.Fatalf("truncate active stream: %v", err)
	}
	for i := 0; i < 3; i++ {
		w.Write(Record{Kind: KindTool, AgentID: fmt.Sprintf("failed-%d", i)})
	}

	// Only the first attempt shifts: the 2-day generation lands in .3 and the
	// 1-day generation in .2, and later attempts must leave both in place.
	records := readRecords(t, path+".2")
	if len(records) != 1 || records[0].AgentID != ageLabel(24*time.Hour) {
		t.Fatalf(".2 records = %+v, want the generation shifted there once", records)
	}
	records = readRecords(t, path+".3")
	if len(records) != 1 || records[0].AgentID != ageLabel(2*24*time.Hour) {
		t.Fatalf(".3 records = %+v, want the generation shifted there once", records)
	}

	renameFile = os.Rename
	w.Write(Record{Kind: KindTool, AgentID: "recovered"})

	if !sameOpenFile(t, w, KindTool) {
		t.Error("writer kept a stale descriptor after recovering, want it reopened")
	}
	records = readRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "recovered" {
		t.Fatalf("records = %+v, want the record written after recovery", records)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("stat rotated generation: %v, want the retried rotation to land", err)
	}
}

func TestPartialRotationFailureKeepsGenerationOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	seedStream(t, path+".1", 24*time.Hour)
	seedStream(t, path+".2", 2*24*time.Hour)
	seedStream(t, path+".3", 3*24*time.Hour)

	// Fail only the move of generation 1, so the older generations shift while
	// generation 1 stays in place.
	renameFile = func(oldpath, newpath string) error {
		if oldpath == path+".1" {
			return errors.New("injected generation 1 move failure")
		}
		return os.Rename(oldpath, newpath)
	}
	t.Cleanup(func() { renameFile = os.Rename })

	if err := os.Truncate(path, maxStreamBytes); err != nil {
		t.Fatalf("truncate active stream: %v", err)
	}
	w.Write(Record{Kind: KindTool, AgentID: "failed"})

	if s := w.files[KindTool]; s.file != nil {
		t.Fatal("stream is open after a refused rotation, want it left closed")
	}
	// Generation 1 still holds its record, so renaming the active file now would
	// overwrite it. The older generation did shift into .3.
	records := readRecords(t, path+".1")
	if len(records) != 1 || records[0].AgentID != ageLabel(24*time.Hour) {
		t.Fatalf(".1 records = %+v, want the generation left in place", records)
	}
	records = readRecords(t, path+".3")
	if len(records) != 1 || records[0].AgentID != ageLabel(2*24*time.Hour) {
		t.Fatalf(".3 records = %+v, want the older generation shifted into .3", records)
	}

	renameFile = os.Rename
	w.Write(Record{Kind: KindTool, AgentID: "recovered"})

	if !sameOpenFile(t, w, KindTool) {
		t.Error("writer kept a stale descriptor after recovering, want it reopened")
	}
	for _, gen := range []struct {
		path string
		want string
	}{
		{path: path + ".2", want: ageLabel(24 * time.Hour)},
		{path: path + ".3", want: ageLabel(2 * 24 * time.Hour)},
	} {
		records := readRecords(t, gen.path)
		if len(records) != 1 || records[0].AgentID != gen.want {
			t.Errorf("%s records = %+v, want the %s generation", gen.path, records, gen.want)
		}
	}
	records = readRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "recovered" {
		t.Fatalf("records = %+v, want the record written after recovery", records)
	}
}

func TestExternalRotationClearsStagedRotationState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Stage a rotation: the shift runs and only the active rename fails, leaving
	// this writer with staged state and no open descriptor.
	renameFile = func(oldpath, newpath string) error {
		if newpath == path+".1" {
			return errors.New("injected active rename failure")
		}
		return os.Rename(oldpath, newpath)
	}
	t.Cleanup(func() { renameFile = os.Rename })
	if err := os.Truncate(path, maxStreamBytes); err != nil {
		t.Fatalf("truncate active stream: %v", err)
	}
	w.Write(Record{Kind: KindTool, AgentID: "failed"})
	renameFile = os.Rename

	if s := w.files[KindTool]; s.file != nil {
		t.Fatal("setup: want the stream closed after the failed rotation")
	}

	// Another writer rotates the stream: the active file becomes generation 1 and
	// a fresh active file takes its place.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatalf("rotate externally: %v", err)
	}
	seedStream(t, path+".1", time.Hour)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create fresh active stream: %v", err)
	}
	if err := os.Truncate(path, maxStreamBytes); err != nil {
		t.Fatalf("grow fresh active stream: %v", err)
	}

	w.Write(Record{Kind: KindTool, AgentID: "recovered"})

	if !sameOpenFile(t, w, KindTool) {
		t.Error("writer kept a stale descriptor after recovering, want it reopened")
	}
	// The staged shift belonged to the file another writer rotated, so it must be
	// retried: the external generation moves to .2 instead of being overwritten.
	records := readRecords(t, path+".2")
	if len(records) != 1 || records[0].AgentID != ageLabel(time.Hour) {
		t.Fatalf(".2 records = %+v, want the generation another writer rotated", records)
	}
	records = readRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "recovered" {
		t.Fatalf("records = %+v, want the record written after recovery", records)
	}
}

func TestConcurrentRotateAndAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	requireStreamLock(t, path)
	rotator, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = rotator.Close() })
	appender, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = appender.Close() })

	payload := strings.Repeat("x", 64*1024)
	rotateWrites := maxStreamBytes/len(payload) + 2
	const appendWrites = 50

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < rotateWrites; i++ {
			rotator.Write(Record{Kind: KindTool, AgentID: "rotator", Payload: payload})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < appendWrites; i++ {
			appender.Write(Record{Kind: KindTool, AgentID: "appender"})
		}
	}()
	wg.Wait()

	appender.Write(Record{Kind: KindTool, AgentID: "final"})

	for _, w := range []*Writer{rotator, appender} {
		if !sameOpenFile(t, w, KindTool) {
			t.Error("writer kept a descriptor rotated away by the other writer")
		}
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("stat rotated generation: %v, want a rotation to have happened", err)
	}

	records := readGenerations(t, path)
	if want := rotateWrites + appendWrites + 1; len(records) != want {
		t.Fatalf("records = %d, want %d (no record lost or duplicated across the rotation)", len(records), want)
	}
	seen := make(map[int64]bool, len(records))
	for _, rec := range records {
		if seen[rec.Seq] {
			t.Fatalf("seq %d appears twice, want each record written once", rec.Seq)
		}
		seen[rec.Seq] = true
	}
	active := readRecords(t, path)
	if len(active) == 0 {
		t.Fatal("active stream is empty, want the final record")
	}
	if last := active[len(active)-1]; last.AgentID != "final" {
		t.Errorf("last active record = %q, want %q", last.AgentID, "final")
	}
}

func TestConcurrentOpensWithRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.jsonl")
	seedStream(t, path, 40*24*time.Hour)
	requireStreamLock(t, path)

	const writers = 4
	opened := make([]*Writer, writers)
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w, err := New(Options{Dir: dir, Streams: Streams{Cache: true}, RetentionDays: 30})
			if err != nil {
				errs <- err
				return
			}
			opened[i] = w
			w.Write(Record{Kind: KindCache, AgentID: fmt.Sprintf("writer-%d", i)})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("New() error = %v", err)
	}
	for i, w := range opened {
		if w == nil {
			t.Fatalf("writer-%d was not opened", i)
		}
		if !sameOpenFile(t, w, KindCache) {
			t.Errorf("writer-%d kept a descriptor replaced by another writer's retention", i)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}

	records := readRecords(t, path)
	if len(records) != writers {
		t.Fatalf("records = %d, want %d (every writer's record survives concurrent retention)", len(records), writers)
	}
	seen := make(map[string]bool, writers)
	for _, rec := range records {
		seen[rec.AgentID] = true
	}
	for i := 0; i < writers; i++ {
		if want := fmt.Sprintf("writer-%d", i); !seen[want] {
			t.Errorf("record %q is missing", want)
		}
	}
}
