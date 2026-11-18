package diagnostics

import (
	"encoding/json"
	"errors"
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

// fakeClock is a fixed clock the stall retry tests advance by hand.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// useClock drives the rotation retry interval from c for the rest of the test.
func useClock(t *testing.T, c *fakeClock) {
	t.Helper()
	restore := timeNow
	timeNow = c.Now
	t.Cleanup(func() { timeNow = restore })
}

// blockRotation makes the active file's stage path a non-empty directory, so the
// first staging rename fails before any generation moves. The obstruction sits
// before the transaction records any progress, so the failed rotation is
// abandoned and the deferred retry and stall tests can still append past the
// cap.
func blockRotation(t *testing.T, path string) {
	t.Helper()
	blocker := rotationStagePath(path, 0)
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatalf("create blocker %s: %v", blocker, err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocker %s: %v", blocker, err)
	}
}

// rotationArtefacts lists the marker and stage files left in dir.
func rotationArtefacts(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), rotationStageSuffix) || strings.HasSuffix(entry.Name(), rotationMarkerSuffix) {
			out = append(out, entry.Name())
		}
	}
	return out
}

// seedGeneration writes content to one generation of a stream, where generation
// 0 is the active file.
func seedGeneration(t *testing.T, path string, generation int, content string) {
	t.Helper()
	seedFile(t, generationPath(path, generation), content)
}

// seedFile writes content to target.
func seedFile(t *testing.T, target, content string) {
	t.Helper()
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatalf("seed %s: %v", target, err)
	}
}

// readFileContent returns the contents of target.
func readFileContent(t *testing.T, target string) string {
	t.Helper()
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	return string(data)
}

// recordLine encodes one tool record as the JSONL line a stream file holds.
func recordLine(t *testing.T, agentID string) string {
	t.Helper()
	data, err := json.Marshal(Record{Kind: KindTool, AgentID: agentID, RunID: "run"})
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return string(data) + "\n"
}

// fileSize returns the size of path.
func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

// unblockRotation removes the directory blockRotation created.
func unblockRotation(t *testing.T, path string) {
	t.Helper()
	if err := os.RemoveAll(rotationStagePath(path, 0)); err != nil {
		t.Fatalf("remove blocker %s: %v", rotationStagePath(path, 0), err)
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

func TestStalledRotationRetriesAfterInterval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	clock := &fakeClock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	useClock(t, clock)

	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	blockRotation(t, path)
	padToCap(t, path)
	payload := strings.Repeat("x", 64*1024)

	// The first failure allows one more cap worth of appends.
	w.Write(Record{Kind: KindTool, AgentID: "past-cap"})
	if s := w.files[KindTool]; s.rotateStalled {
		t.Fatal("stream stalled after the first failed rotation, want one cap of grace")
	}
	for i := 0; i < maxStreamBytes/len(payload)+40; i++ {
		w.Write(Record{Kind: KindTool, Payload: payload})
	}

	if s := w.files[KindTool]; !s.rotateStalled {
		t.Fatal("stream not stalled after the deferred retry failed")
	}
	size := fileSize(t, path)
	if size <= maxStreamBytes {
		t.Errorf("size = %d, want the grace window to append past the cap", size)
	}
	if limit := int64(2*maxStreamBytes + len(payload)); size > limit {
		t.Fatalf("size = %d, want at most %d with a stuck rotation", size, limit)
	}

	// A stalled stream drops records instead of growing the file.
	for i := 0; i < 5; i++ {
		w.Write(Record{Kind: KindTool, Payload: payload})
	}
	if got := fileSize(t, path); got != size {
		t.Errorf("size = %d after dropped records, want it unchanged at %d", got, size)
	}

	// Recovery waits for the retry interval, not for dropped volume.
	unblockRotation(t, path)
	w.Write(Record{Kind: KindTool, AgentID: "before-interval"})
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("generation 1 stat error = %v, want no rotation before the retry interval", err)
	}
	clock.advance(stallRetryInterval)
	w.Write(Record{Kind: KindTool, AgentID: "after-interval"})

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("stat generation 1: %v, want a rotation once the retry interval elapsed", err)
	}
	if s := w.files[KindTool]; s.rotateStalled {
		t.Error("stream still stalled after a successful rotation")
	}
	records := readTailRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "after-interval" {
		t.Fatalf("records = %+v, want only the record written after the retry", records)
	}
}

func TestReplacementClearsRotationDeferral(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	blockRotation(t, path)
	padToCap(t, path)
	payload := strings.Repeat("x", 64*1024)
	w.Write(Record{Kind: KindTool, AgentID: "past-cap"})
	for i := 0; i < maxStreamBytes/len(payload)+40; i++ {
		w.Write(Record{Kind: KindTool, Payload: payload})
	}
	if s := w.files[KindTool]; !s.rotateStalled {
		t.Fatal("setup: stream not stalled after the deferred retry failed")
	}

	// Another process rotates the stream away. The new active file has its own
	// rotation history, so the stall recorded against the old inode must not
	// drop this writer's records.
	unblockRotation(t, path)
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatalf("rename active stream: %v", err)
	}

	w.Write(Record{Kind: KindTool, AgentID: "after-replacement"})

	if s := w.files[KindTool]; s.rotateStalled || s.rotateAfter != 0 || !s.retryAt.IsZero() {
		t.Errorf("deferral = %d/%v/%v after replacement, want it cleared", s.rotateAfter, s.rotateStalled, s.retryAt)
	}
	records := readRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "after-replacement" {
		t.Fatalf("records = %+v, want the record written after the replacement", records)
	}
}

func TestStatErrorKeepsRotationState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	clock := &fakeClock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	useClock(t, clock)

	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	s := w.files[KindTool]
	s.rotateAfter = maxStreamBytes
	s.rotateStalled = true
	s.retryAt = clock.Now().Add(stallRetryInterval)

	// A self-referential symlink makes the path stat fail with an error that is
	// neither a replacement nor a missing file.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove active stream: %v", err)
	}
	if err := os.Symlink(filepath.Base(path), path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := os.Stat(path); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat %s = %v, want a stat error that is not a missing file", path, err)
	}

	w.Write(Record{Kind: KindTool, AgentID: "stat-error"})

	if !w.warned {
		t.Error("warned = false, want the stat error reported")
	}
	if !s.rotateStalled || s.rotateAfter != maxStreamBytes || !s.retryAt.Equal(clock.Now().Add(stallRetryInterval)) {
		t.Errorf("state = %d/%v/%v after a stat error, want the stall preserved", s.rotateAfter, s.rotateStalled, s.retryAt)
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

func TestNewLeavesStreamClosedWhileRotationMarkerPending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	seedFile(t, rotationStagePath(path, 0), "staged-active\n")
	// A marker this build cannot act on keeps the transaction unfinished however
	// often recovery is retried.
	data, err := json.Marshal(rotationMarker{Version: rotationMarkerVersion + 1, Phase: rotationPhaseStaging, Generations: []int{0}})
	if err != nil {
		t.Fatalf("marshal marker: %v", err)
	}
	marker := rotationMarkerPath(path)
	if err := os.WriteFile(marker, data, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v, want the pending rotation to leave the writer usable", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("active stat error = %v, want no active file created while the rotation is pending", err)
	}
	if s := w.files[KindTool]; s == nil || s.file != nil {
		t.Fatalf("stream = %+v, want it registered without a file", s)
	}

	w.Write(Record{Kind: KindTool, AgentID: "pending"})

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("active stat error = %v, want the dropped record to create nothing", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker stat error = %v, want the unfinished transaction kept", err)
	}

	// Once the marker is gone, the next record opens the active file lazily.
	if err := os.Remove(marker); err != nil {
		t.Fatalf("remove marker: %v", err)
	}
	w.Write(Record{Kind: KindTool, AgentID: "after-marker"})

	records := readTailRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "after-marker" {
		t.Fatalf("active records = %+v, want only the record written once the marker was gone", records)
	}
	if got := readFileContent(t, rotationStagePath(path, 0)); got != "staged-active\n" {
		t.Errorf("stage = %q, want the unfinished transaction's generation left untouched", got)
	}
}

func TestRotateStreamMovesGenerations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	seedGeneration(t, path, 0, "active\n")
	seedGeneration(t, path, 1, "one\n")
	seedGeneration(t, path, 2, "two\n")

	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if err := rotateStream(w.files[KindTool]); err != nil {
		t.Fatalf("rotateStream() error = %v", err)
	}

	for generation, want := range map[int]string{1: "active\n", 2: "one\n", 3: "two\n"} {
		if got := readStream(t, path, generation); got != want {
			t.Errorf("generation %d = %q, want %q", generation, got, want)
		}
	}
	if got := fileSize(t, path); got != 0 {
		t.Errorf("active size = %d, want a fresh empty file", got)
	}
	if got := rotationArtefacts(t, dir); len(got) != 0 {
		t.Errorf("rotation artefacts = %v, want none", got)
	}
}

func TestRotateStreamDiscardsOldestGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	seedGeneration(t, path, 0, "active\n")
	seedGeneration(t, path, 1, "one\n")
	seedGeneration(t, path, 2, "two\n")
	seedGeneration(t, path, 3, "three\n")

	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if err := rotateStream(w.files[KindTool]); err != nil {
		t.Fatalf("rotateStream() error = %v", err)
	}

	if got := readStream(t, path, 3); got != "two\n" {
		t.Errorf("generation 3 = %q, want the previous generation 2", got)
	}
	if _, err := os.Stat(generationPath(path, maxStreamGenerations+1)); !os.IsNotExist(err) {
		t.Errorf("generation %d stat error = %v, want the rotation never to keep past the policy", maxStreamGenerations+1, err)
	}
	for generation := 1; generation <= maxStreamGenerations; generation++ {
		if got := readStream(t, path, generation); strings.Contains(got, "three") {
			t.Errorf("generation %d = %q, want the oldest generation discarded", generation, got)
		}
	}
}

func TestRecoverRotationCompletesStaging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	seedGeneration(t, path, 0, "active\n")
	seedGeneration(t, path, 1, "one\n")
	if err := os.Rename(path, rotationStagePath(path, 0)); err != nil {
		t.Fatalf("stage active file: %v", err)
	}
	writeMarker(t, path, rotationMarker{Version: rotationMarkerVersion, Phase: rotationPhaseStaging, Generations: []int{0, 1}})

	if err := recoverRotation(path); err != nil {
		t.Fatalf("recoverRotation() error = %v", err)
	}

	if got := readStream(t, path, 1); got != "active\n" {
		t.Errorf("generation 1 = %q, want the staged active file installed", got)
	}
	if got := readStream(t, path, 2); got != "one\n" {
		t.Errorf("generation 2 = %q, want the old generation shifted up", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("active stat error = %v, want recovery to leave opening the file to the writer", err)
	}
	if _, err := os.Stat(rotationMarkerPath(path)); !os.IsNotExist(err) {
		t.Errorf("marker stat error = %v, want the completed transaction removed", err)
	}
	if got := rotationArtefacts(t, dir); len(got) != 0 {
		t.Errorf("rotation artefacts = %v, want none", got)
	}
}

func TestRecoverRotationCompletesInstallation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	seedGeneration(t, path, 0, "staged-active\n")
	if err := os.Rename(path, rotationStagePath(path, 0)); err != nil {
		t.Fatalf("stage active file: %v", err)
	}
	seedGeneration(t, path, 2, "installed-one\n")
	writeMarker(t, path, rotationMarker{Version: rotationMarkerVersion, Phase: rotationPhaseInstalling, Generations: []int{0, 1}})

	if err := recoverRotation(path); err != nil {
		t.Fatalf("recoverRotation() error = %v", err)
	}

	if got := readStream(t, path, 1); got != "staged-active\n" {
		t.Errorf("generation 1 = %q, want the staged active file installed", got)
	}
	if got := readStream(t, path, 2); got != "installed-one\n" {
		t.Errorf("generation 2 = %q, want the already installed generation left alone", got)
	}
	if _, err := os.Stat(rotationMarkerPath(path)); !os.IsNotExist(err) {
		t.Errorf("marker stat error = %v, want the completed transaction removed", err)
	}
	if got := rotationArtefacts(t, dir); len(got) != 0 {
		t.Errorf("rotation artefacts = %v, want none", got)
	}
	if err := recoverRotation(path); err != nil {
		t.Errorf("repeated recoverRotation() error = %v, want the recovery idempotent", err)
	}
}

func TestRecoverRotationRejectsAmbiguousGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	seedFile(t, path, "active\n")
	seedFile(t, rotationStagePath(path, 0), "staged-active\n")
	writeMarker(t, path, rotationMarker{Version: rotationMarkerVersion, Phase: rotationPhaseStaging, Generations: []int{0}})

	err := recoverRotation(path)
	if err == nil {
		t.Fatal("recoverRotation() error = nil, want the ambiguous generation rejected")
	}
	if !strings.Contains(err.Error(), "generation 0") || !strings.Contains(err.Error(), "both source and stage") {
		t.Errorf("error = %v, want it to name generation 0 and the conflicting states", err)
	}
	if _, err := os.Stat(rotationMarkerPath(path)); err != nil {
		t.Errorf("marker stat error = %v, want the marker left in place", err)
	}
	if got := readStream(t, path, 0); got != "active\n" {
		t.Errorf("active file = %q, want no rename performed", got)
	}
	if got := readFileContent(t, rotationStagePath(path, 0)); got != "staged-active\n" {
		t.Errorf("generation 1 stage = %q, want no rename performed", got)
	}
}

func TestRecoverRotationRejectsMissingGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	seedGeneration(t, path, 0, "active\n")
	writeMarker(t, path, rotationMarker{Version: rotationMarkerVersion, Phase: rotationPhaseStaging, Generations: []int{0, 1}})

	err := recoverRotation(path)
	if err == nil {
		t.Fatal("recoverRotation() error = nil, want the missing generation rejected")
	}
	if !strings.Contains(err.Error(), "generation 1 is missing") {
		t.Errorf("error = %v, want it to name the missing generation", err)
	}
	if _, err := os.Stat(rotationMarkerPath(path)); err != nil {
		t.Errorf("marker stat error = %v, want the marker left in place", err)
	}
}

func TestRecoverRotationRejectsUnknownMarkerVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	seedGeneration(t, path, 1, "one\n")
	data, err := json.Marshal(rotationMarker{Version: rotationMarkerVersion + 1, Phase: rotationPhaseStaging, Generations: []int{1}})
	if err != nil {
		t.Fatalf("marshal marker: %v", err)
	}
	if err := os.WriteFile(rotationMarkerPath(path), data, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	err = recoverRotation(path)
	if err == nil {
		t.Fatal("recoverRotation() error = nil, want the unknown version rejected")
	}
	if !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("error = %v, want it to name the unsupported version", err)
	}
	if _, err := os.Stat(rotationMarkerPath(path)); err != nil {
		t.Errorf("marker stat error = %v, want the marker left in place", err)
	}
	if got := readStream(t, path, 1); got != "one\n" {
		t.Errorf("generation 1 = %q, want the generations untouched", got)
	}
	if _, err := os.Stat(rotationStagePath(path, 1)); !os.IsNotExist(err) {
		t.Errorf("stage stat error = %v, want no staging performed", err)
	}
}

func TestAppendRecoversInterruptedRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// The active file is already past the cap, so the next record rotates.
	padded := strings.Repeat("\n", maxStreamBytes) + recordLine(t, "seed")
	if err := os.WriteFile(path, []byte(padded), 0o600); err != nil {
		t.Fatalf("pad %s: %v", path, err)
	}
	seedGeneration(t, path, 1, recordLine(t, "generation-one"))

	// Staging the active file succeeds and staging generation 1 fails, leaving
	// the transaction half staged with the marker in place.
	restore := renameFile
	renameFile = func(src, dst string) error {
		if src == generationPath(path, 1) {
			return errors.New("injected rename failure")
		}
		return restore(src, dst)
	}
	t.Cleanup(func() { renameFile = restore })

	w.Write(Record{Kind: KindTool, AgentID: "during-interruption"})
	renameFile = restore

	if !w.warned {
		t.Error("warned = false, want the interrupted rotation reported")
	}
	if _, err := os.Stat(rotationMarkerPath(path)); err != nil {
		t.Fatalf("marker stat error = %v, want the interrupted transaction left in place", err)
	}
	if got := readFileContent(t, rotationStagePath(path, 0)); !strings.Contains(got, `"seed"`) {
		t.Fatalf("staged active file = %d bytes, want the padded stream with its seed record intact", len(got))
	}
	if got := readStream(t, path, 1); !strings.Contains(got, "generation-one") {
		t.Fatalf("generation 1 = %q, want the old generation untouched by the failed staging", got)
	}
	if s := w.files[KindTool]; s.file != nil {
		t.Fatal("stream file is open, want the record dropped while the transaction is pending")
	}

	// The next append completes the transaction before writing.
	w.Write(Record{Kind: KindTool, AgentID: "after-recovery"})

	if _, err := os.Stat(rotationMarkerPath(path)); !os.IsNotExist(err) {
		t.Fatalf("marker stat error = %v, want the recovery to remove it", err)
	}
	if got := rotationArtefacts(t, dir); len(got) != 0 {
		t.Errorf("rotation artefacts = %v, want none", got)
	}
	if got := readStream(t, path, 1); !strings.Contains(got, `"seed"`) {
		t.Errorf("generation 1 = %d bytes, want the interrupted active file installed", len(got))
	}
	if got := readStream(t, path, 2); !strings.Contains(got, "generation-one") {
		t.Errorf("generation 2 = %q, want the old generation shifted up", got)
	}
	records := readTailRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "after-recovery" {
		t.Fatalf("active records = %+v, want only the record written after recovery", records)
	}
}

func TestAppendDropsRecordWhenRotationCannotProceed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	padded := strings.Repeat("\n", maxStreamBytes) + recordLine(t, "seed")
	if err := os.WriteFile(path, []byte(padded), 0o600); err != nil {
		t.Fatalf("pad %s: %v", path, err)
	}
	seedGeneration(t, path, 1, recordLine(t, "generation-one"))
	seedGeneration(t, path, 2, recordLine(t, "generation-two"))

	// The oldest generation is a non-empty directory, so the transaction can
	// install every generation but can never discard that one: the marker stays
	// and no later append can finish or restart the rotation.
	oldest := generationPath(path, maxStreamGenerations)
	if err := os.Mkdir(oldest, 0o700); err != nil {
		t.Fatalf("create oldest generation %s: %v", oldest, err)
	}
	if err := os.WriteFile(filepath.Join(oldest, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed oldest generation %s: %v", oldest, err)
	}

	w.Write(Record{Kind: KindTool, AgentID: "blocked"})
	w.Write(Record{Kind: KindTool, AgentID: "dropped"})

	if !w.warned {
		t.Error("warned = false, want the rotation failure reported")
	}
	if _, err := os.Stat(rotationMarkerPath(path)); err != nil {
		t.Fatalf("marker stat error = %v, want the stuck transaction left in place", err)
	}
	if s := w.files[KindTool]; s.file != nil {
		t.Error("stream file is open, want the records dropped while the transaction is stuck")
	}
	if got := readStream(t, path, 1); got != padded {
		t.Errorf("generation 1 = %d bytes, want the previous active file intact", len(got))
	}
	if got := readStream(t, path, 2); got != recordLine(t, "generation-one") {
		t.Errorf("generation 2 = %q, want the previous generation 1 intact", got)
	}
	if got := readStream(t, path, 3); got != recordLine(t, "generation-two") {
		t.Errorf("generation 3 = %q, want the previous generation 2 intact", got)
	}
	for generation := 0; generation <= maxStreamGenerations; generation++ {
		target := generationPath(path, generation)
		if _, err := os.Stat(target); err != nil {
			continue
		}
		got := readFileContent(t, target)
		if strings.Contains(got, "blocked") || strings.Contains(got, "dropped") {
			t.Errorf("generation %d = %q, want the records dropped, not appended", generation, got)
		}
	}
}

// writeMarker writes m as the rotation marker for path.
func writeMarker(t *testing.T, path string, m rotationMarker) {
	t.Helper()
	if err := writeRotationMarker(path, m); err != nil {
		t.Fatalf("write rotation marker: %v", err)
	}
}

func TestRotateStreamKeepsActiveUnopenedWhenRecoveryFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	file, err := openStreamFile(path)
	if err != nil {
		t.Fatalf("openStreamFile() error = %v", err)
	}
	if _, err := file.WriteString("active\n"); err != nil {
		t.Fatalf("write active file: %v", err)
	}
	// A recovery that staged generation 0 and then failed leaves the active file
	// at its stage path, so reopening the active path here would create a second
	// generation 0 and every later staging attempt would be rejected.
	if err := os.Rename(path, rotationStagePath(path, 0)); err != nil {
		t.Fatalf("stage active file: %v", err)
	}
	// A marker this build cannot act on keeps the transaction unfinished however
	// often recovery is retried.
	writeMarker(t, path, rotationMarker{Version: rotationMarkerVersion + 1, Phase: rotationPhaseStaging, Generations: []int{0}})
	s := &stream{file: file, path: path}

	if err := rotateStream(s); err == nil {
		t.Fatal("rotateStream() error = nil, want the unfinished recovery reported")
	}
	if s.file != nil {
		t.Errorf("stream file = %v, want it closed so nothing appends to a staged generation", s.file)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("active stat error = %v, want no active file created", err)
	}
	if got := readFileContent(t, rotationStagePath(path, 0)); got != "active\n" {
		t.Errorf("stage = %q, want the staged generation left untouched", got)
	}
}
