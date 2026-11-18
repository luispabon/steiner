package diagnostics

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/filelock"
)

func allStreams() Streams {
	return Streams{Cache: true, Provider: true, Tool: true, TUI: true, Internal: true}
}

func readRecords(t *testing.T, path string) []Record {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []Record
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestWriterRoundTrip(t *testing.T) {
	dir := t.TempDir()
	w, err := New(Options{
		Dir:      dir,
		Streams:  allStreams(),
		RunID:    "run-1",
		BuildSHA: "abc123",
		Dirty:    true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	ts := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	w.Write(Record{
		Timestamp: ts,
		SessionID: "sess-1",
		Kind:      KindCache,
		Source:    SourceSubAgent,
		AgentID:   "agent-1",
		AgentType: "code",
		Turn:      3,
		Payload:   map[string]any{"hit": true},
	})
	w.Write(Record{Kind: KindProvider, Source: SourceParent})

	cache := readRecords(t, filepath.Join(dir, "cache.jsonl"))
	if len(cache) != 1 {
		t.Fatalf("cache records = %d, want 1", len(cache))
	}
	got := cache[0]
	if !got.Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, ts)
	}
	if got.RunID != "run-1" || got.BuildSHA != "abc123" || !got.Dirty {
		t.Errorf("identity = %q/%q/%v, want run-1/abc123/true", got.RunID, got.BuildSHA, got.Dirty)
	}
	if got.SessionID != "sess-1" || got.Kind != KindCache || got.Source != SourceSubAgent {
		t.Errorf("envelope = %q/%q/%q", got.SessionID, got.Kind, got.Source)
	}
	if got.AgentID != "agent-1" || got.AgentType != "code" || got.Turn != 3 {
		t.Errorf("scope = %q/%q/%d", got.AgentID, got.AgentType, got.Turn)
	}
	if got.Seq == 0 {
		t.Error("Seq = 0, want a monotonic counter value")
	}
	payload, ok := got.Payload.(map[string]any)
	if !ok || payload["hit"] != true {
		t.Errorf("Payload = %#v, want map with hit=true", got.Payload)
	}

	prov := readRecords(t, filepath.Join(dir, "provider.jsonl"))
	if len(prov) != 1 {
		t.Fatalf("provider records = %d, want 1", len(prov))
	}
	if prov[0].Seq <= got.Seq {
		t.Errorf("Seq = %d, want greater than %d", prov[0].Seq, got.Seq)
	}
	if prov[0].Timestamp.IsZero() {
		t.Error("Timestamp is zero, want it filled in by the writer")
	}
}

func TestWriterStreamGating(t *testing.T) {
	tests := []struct {
		name    string
		streams Streams
		want    map[Kind]bool
	}{
		{name: "cache only", streams: Streams{Cache: true}, want: map[Kind]bool{KindCache: true}},
		{name: "provider only", streams: Streams{Provider: true}, want: map[Kind]bool{KindProvider: true}},
		{name: "tool only", streams: Streams{Tool: true}, want: map[Kind]bool{KindTool: true}},
		{name: "tui only", streams: Streams{TUI: true}, want: map[Kind]bool{KindTUI: true}},
		{name: "internal only", streams: Streams{Internal: true}, want: map[Kind]bool{KindInternal: true}},
		{name: "all", streams: allStreams(), want: map[Kind]bool{KindCache: true, KindProvider: true, KindTool: true, KindTUI: true, KindInternal: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			w, err := New(Options{Dir: dir, Streams: tt.streams})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(func() { _ = w.Close() })
			for _, kind := range Kinds() {
				w.Write(Record{Kind: kind})
				if w.Enabled(kind) != tt.want[kind] {
					t.Errorf("Enabled(%s) = %v, want %v", kind, w.Enabled(kind), tt.want[kind])
				}
				_, err := os.Stat(filepath.Join(dir, kind.fileName()))
				if tt.want[kind] && err != nil {
					t.Errorf("stat %s: %v, want the file to exist", kind.fileName(), err)
				}
				if !tt.want[kind] && err == nil {
					t.Errorf("%s exists, want no file for a disabled stream", kind.fileName())
				}
			}
		})
	}
}

func TestWriterInternalStream(t *testing.T) {
	tests := []struct {
		name    string
		streams Streams
		want    bool
	}{
		{name: "enabled writes the record", streams: Streams{Internal: true}, want: true},
		{name: "disabled writes nothing", streams: Streams{Cache: true}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			w, err := New(Options{Dir: dir, Streams: tt.streams})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(func() { _ = w.Close() })
			w.Write(Record{Kind: KindInternal, Payload: map[string]any{"msg": "boom"}})

			path := filepath.Join(dir, "internal.jsonl")
			if !tt.want {
				if _, err := os.Stat(path); err == nil {
					t.Fatal("internal.jsonl exists, want no file for a disabled stream")
				}
				return
			}
			records := readRecords(t, path)
			if len(records) != 1 {
				t.Fatalf("internal records = %d, want 1", len(records))
			}
			if records[0].Kind != KindInternal {
				t.Errorf("Kind = %q, want %q", records[0].Kind, KindInternal)
			}
		})
	}
}

func TestNewNoStreamsCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "diagnostics")
	w, err := New(Options{Dir: dir, RetentionDays: 30})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if w != nil {
		t.Fatalf("New() = %#v, want nil writer when no stream is enabled", w)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("stat %s error = %v, want the directory not to exist", dir, err)
	}
}

func TestWriterPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "diagnostics")
	w, err := New(Options{Dir: dir, Streams: allStreams()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Write(Record{Kind: KindTool})

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 700", info.Mode().Perm())
	}
	for _, kind := range Kinds() {
		fi, err := os.Stat(filepath.Join(dir, kind.fileName()))
		if err != nil {
			t.Fatalf("stat %s: %v", kind.fileName(), err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %o, want 600", kind.fileName(), fi.Mode().Perm())
		}
	}
}

func TestWriterUpgradesExistingPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "diagnostics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	for _, kind := range Kinds() {
		path := filepath.Join(dir, kind.fileName())
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("create %s: %v", kind.fileName(), err)
		}
	}

	w, err := New(Options{Dir: dir, Streams: allStreams()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if info, err := os.Stat(dir); err != nil {
		t.Fatalf("stat dir: %v", err)
	} else if info.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 700", info.Mode().Perm())
	}
	for _, kind := range Kinds() {
		path := filepath.Join(dir, kind.fileName())
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", kind.fileName(), err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %o, want 600", kind.fileName(), info.Mode().Perm())
		}
	}
}

func TestWriterAppendsAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		w.Write(Record{Kind: KindTool})
		if err := w.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
	if got := len(readRecords(t, filepath.Join(dir, "tool.jsonl"))); got != 2 {
		t.Fatalf("records = %d, want 2 (the second open must append)", got)
	}
}

func TestWriterConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	w, err := New(Options{Dir: dir, Streams: allStreams()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	const writers, perWriter = 8, 25
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				w.Write(Record{Kind: KindProvider, AgentID: fmt.Sprintf("agent-%d-%d", id, j)})
			}
		}(i)
	}
	wg.Wait()

	file, err := os.Open(filepath.Join(dir, "provider.jsonl"))
	if err != nil {
		t.Fatalf("open provider.jsonl: %v", err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	lines := 0
	for scanner.Scan() {
		var rec Record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("line %d is not a whole record (%v): %q", lines+1, err, scanner.Text())
		}
		if rec.Kind != KindProvider {
			t.Errorf("line %d kind = %q, want provider", lines+1, rec.Kind)
		}
		lines++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if lines != writers*perWriter {
		t.Fatalf("lines = %d, want %d", lines, writers*perWriter)
	}
}

func TestWriterRotation(t *testing.T) {
	dir := t.TempDir()
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	payload := strings.Repeat("x", 64*1024)
	for i := 0; i < (maxStreamBytes/len(payload))+2; i++ {
		w.Write(Record{Kind: KindTool, Payload: payload})
	}

	rotated := filepath.Join(dir, "tool.jsonl.1")
	info, err := os.Stat(rotated)
	if err != nil {
		t.Fatalf("stat %s: %v, want a rotated generation", rotated, err)
	}
	if info.Size() < maxStreamBytes/2 {
		t.Errorf("rotated size = %d, want a full generation", info.Size())
	}
	active, err := os.Stat(filepath.Join(dir, "tool.jsonl"))
	if err != nil {
		t.Fatalf("stat active file: %v", err)
	}
	if active.Size() >= maxStreamBytes {
		t.Errorf("active size = %d, want a fresh file below the cap", active.Size())
	}
	if active.Mode().Perm() != 0o600 {
		t.Errorf("active mode = %o, want 600", active.Mode().Perm())
	}
	// Every cooperating process must lock the same inode, so the lock file stays
	// put when the generations it guards move.
	if _, err := os.Stat(streamLockPath(dir, KindTool)); err != nil {
		t.Errorf("stream lock stat error = %v, want the lock file kept across rotation", err)
	}
}

func TestNilWriterMethodsAreNoOps(t *testing.T) {
	var w *Writer
	w.Write(Record{Kind: KindCache})
	if w.Enabled(KindCache) {
		t.Error("Enabled() = true on nil writer, want false")
	}
	if w.CaptureBodies() {
		t.Error("CaptureBodies() = true on nil writer, want false")
	}
	if w.RunID() != "" {
		t.Errorf("RunID() = %q on nil writer, want empty", w.RunID())
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close() error = %v on nil writer, want nil", err)
	}
}

func TestWriterCaptureBodies(t *testing.T) {
	dir := t.TempDir()
	w, err := New(Options{Dir: dir, Streams: Streams{Cache: true}, CaptureBodies: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if !w.CaptureBodies() {
		t.Error("CaptureBodies() = false, want true")
	}
	if w.RunID() == "" {
		t.Error("RunID() = empty, want a minted run id")
	}
}

func TestNewEmptyDir(t *testing.T) {
	if _, err := New(Options{Streams: allStreams()}); err == nil {
		t.Fatal("New() error = nil, want an error for an empty directory")
	}
}

// partialRecord is an encoded record cut off mid-line, as a process killed
// during a write leaves it.
const partialRecord = `{"ts":"2026-11-18T00:00:00Z","kind":"cache","agent_id":"frag-`

// failStreamLock replaces the stream lock seam with a failing acquire.
func failStreamLock(t *testing.T) {
	t.Helper()

	orig := acquireStreamFileLock
	acquireStreamFileLock = func(context.Context, string) (streamLock, error) {
		return nil, errors.New("boom")
	}
	t.Cleanup(func() { acquireStreamFileLock = orig })
}

// unsupportedTestLock stands in for the degraded lock filelock returns when the
// platform or filesystem cannot lock a stream.
type unsupportedTestLock struct {
	released bool
}

func (l *unsupportedTestLock) Release() error {
	l.released = true
	return nil
}

func (l *unsupportedTestLock) Unsupported() bool { return true }

func (l *unsupportedTestLock) Warning() string { return "stream lock unavailable in test" }

// appendFragment appends raw bytes to path, leaving the file without a trailing
// newline the way a killed writer does.
func appendFragment(t *testing.T, path, fragment string) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if _, err := file.WriteString(fragment); err != nil {
		_ = file.Close()
		t.Fatalf("write fragment to %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// skipUnlessStreamLocksSupported skips a test whose assertions need real
// cross-process exclusion, which the platform or filesystem may refuse.
func skipUnlessStreamLocksSupported(t *testing.T, dir string, kind Kind) {
	t.Helper()

	lock, err := filelock.Acquire(context.Background(), streamLockPath(dir, kind))
	if err != nil {
		t.Fatalf("acquire stream lock: %v", err)
	}
	unsupported := lock.Unsupported()
	// Best-effort: the probe holds nothing but the descriptor.
	_ = lock.Release()
	if unsupported {
		t.Skip("stream locks are unsupported on this platform")
	}
}

func TestWriterRepairsIncompleteTrailingRecord(t *testing.T) {
	tests := []struct {
		name     string
		fragment string
		marker   string
	}{
		{
			// A fragment longer than the backwards scan window must not take the
			// complete lines before it with it.
			name:     "fragment longer than the scan window",
			fragment: `{"ts":"2026-11-18T00:00:00Z","payload":"` + strings.Repeat("x", 2*tailScanBytes),
			marker:   `"payload":"`,
		},
		{name: "short fragment", fragment: partialRecord, marker: `"frag-`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "cache.jsonl")
			seeded := 2 * 24 * time.Hour
			seedStream(t, path, seeded)
			appendFragment(t, path, tt.fragment)

			w, err := New(Options{Dir: dir, Streams: Streams{Cache: true}})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(func() { _ = w.Close() })
			w.Write(Record{Kind: KindCache, AgentID: "after-repair"})

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if strings.Contains(string(data), tt.marker) {
				t.Errorf("stream = %q, want the incomplete trailing record removed", string(data))
			}
			records := readRecords(t, path)
			if len(records) != 2 {
				t.Fatalf("records = %d, want the seeded record plus the new one", len(records))
			}
			if records[0].AgentID != ageLabel(seeded) || records[1].AgentID != "after-repair" {
				t.Errorf("records = %q/%q, want %q/after-repair", records[0].AgentID, records[1].AgentID, ageLabel(seeded))
			}
		})
	}
}

func TestWriterKeepCompleteTrailingRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.jsonl")
	seeded := 2 * 24 * time.Hour
	seedStream(t, path, seeded)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	w, err := New(Options{Dir: dir, Streams: Streams{Cache: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Write(Record{Kind: KindCache, AgentID: "after-open"})

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.HasPrefix(string(after), string(before)) {
		t.Errorf("stream = %q, want the complete seeded record kept verbatim", string(after))
	}
	records := readRecords(t, path)
	if len(records) != 2 || records[0].AgentID != ageLabel(seeded) {
		t.Fatalf("records = %+v, want the seeded record kept before the new one", records)
	}
}

func TestWriterRepairsFragmentAppendedAfterOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	w.Write(Record{Kind: KindTool, AgentID: "before"})
	// A peer is killed mid-append after this writer opened, so the fragment
	// appears in the stream the writer already holds.
	appendFragment(t, path, `{"partial":`)

	w.Write(Record{Kind: KindTool, AgentID: "after"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.Contains(string(data), `"partial"`) {
		t.Errorf("stream = %q, want the fragment removed before the append", string(data))
	}
	records := readRecords(t, path)
	if len(records) != 2 || records[0].AgentID != "before" || records[1].AgentID != "after" {
		t.Fatalf("records = %+v, want the two complete records", records)
	}
	if got, written := fileSize(t, path), w.files[KindTool].written; got != written {
		t.Errorf("tracked size = %d, want the repaired file size %d", written, got)
	}
}

func TestWriterRepairsStreamThatIsOnlyAFragment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Nothing in the file is a complete line, so the repair has to empty it.
	appendFragment(t, path, `{"partial":`)
	w.Write(Record{Kind: KindTool, AgentID: "sole"})

	if got := readFileContent(t, path); strings.Contains(got, `"partial"`) {
		t.Errorf("stream = %q, want the fragment-only file emptied", got)
	}
	records := readRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "sole" {
		t.Fatalf("records = %+v, want only the record written after the repair", records)
	}
	if got, written := fileSize(t, path), w.files[KindTool].written; got != written {
		t.Errorf("tracked size = %d, want the repaired file size %d", written, got)
	}
}

func TestWriterDropsRecordWhenTailRepairFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	appendFragment(t, path, `{"partial":`)
	if err := os.Chmod(path, 0o200); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	// Root and filesystems without enforced permissions cannot make the tail
	// unreadable, so the repair cannot be shown to fail here.
	if probe, err := os.Open(path); err == nil {
		_ = probe.Close()
		t.Skip("file permissions are not enforced, so an unreadable tail cannot be set up")
	}

	w.Write(Record{Kind: KindTool, AgentID: "dropped"})

	if !w.warned {
		t.Error("warned = false, want the failed repair reported")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	if got := readFileContent(t, path); got != `{"partial":` {
		t.Errorf("stream = %q, want the record dropped rather than appended to the fragment", got)
	}
	if s := w.files[KindTool]; s.written != int64(len(`{"partial":`)) {
		t.Errorf("tracked size = %d, want it unchanged for a dropped record", s.written)
	}
}

func TestWriterSkipsAppendWhenStreamLockUnavailable(t *testing.T) {
	failStreamLock(t)

	dir := t.TempDir()
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v, want the stream to open without a lock", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Write(Record{Kind: KindTool, AgentID: "dropped"})

	if !w.warned {
		t.Error("warned = false, want the unavailable lock reported")
	}
	if got := readRecords(t, filepath.Join(dir, "tool.jsonl")); len(got) != 0 {
		t.Fatalf("records = %+v, want the record dropped while the lock is unavailable", got)
	}
}

func TestNewSkipsRetentionWhenStreamLockUnavailable(t *testing.T) {
	failStreamLock(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "cache.jsonl")
	stale := 90 * 24 * time.Hour
	seedStream(t, path, stale, 2*24*time.Hour)

	w, err := New(Options{Dir: dir, Streams: Streams{Cache: true}, RetentionDays: 30})
	if err != nil {
		t.Fatalf("New() error = %v, want a skipped retention to succeed", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if !w.warned {
		t.Error("warned = false, want the unavailable lock reported")
	}
	records := readRecords(t, path)
	if len(records) != 2 || records[0].AgentID != ageLabel(stale) {
		t.Fatalf("records = %+v, want both seeded records kept without the lock", records)
	}
}

func TestWriterSkipsMutationsWhenStreamLockDegrades(t *testing.T) {
	tests := []struct {
		name        string
		unsupported bool
	}{
		{name: "no lock returned"},
		{name: "unsupported lock", unsupported: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := acquireStreamFileLock
			var degraded *unsupportedTestLock
			acquireStreamFileLock = func(context.Context, string) (streamLock, error) {
				if !tt.unsupported {
					return nil, nil
				}
				degraded = &unsupportedTestLock{}
				return degraded, nil
			}
			t.Cleanup(func() { acquireStreamFileLock = orig })

			dir := t.TempDir()
			path := filepath.Join(dir, "tool.jsonl")
			w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
			if err != nil {
				t.Fatalf("New() error = %v, want the degraded lock to leave the writer usable", err)
			}
			t.Cleanup(func() { _ = w.Close() })
			// Opening the writer already warned; this asserts the append warns too.
			w.warned = false
			padToCap(t, path)

			w.Write(Record{Kind: KindTool, AgentID: "dropped"})

			if !w.warned {
				t.Error("warned = false, want the degraded lock reported")
			}
			if got := fileSize(t, path); got != maxStreamBytes {
				t.Errorf("size = %d, want the padded file untouched at %d", got, maxStreamBytes)
			}
			if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
				t.Errorf("generation 1 stat error = %v, want no rotation without the lock", err)
			}
			if got := rotationArtefacts(t, dir); len(got) != 0 {
				t.Errorf("rotation artefacts = %v, want no rotation without the lock", got)
			}
			if tt.unsupported && (degraded == nil || !degraded.released) {
				t.Error("unsupported lock was not released, want the degradation handled without holding it")
			}
		})
	}
}

func TestWriterSerializesAppendsAcrossWriters(t *testing.T) {
	dir := t.TempDir()
	skipUnlessStreamLocksSupported(t, dir, KindTool)

	first, err := New(Options{Dir: dir, Streams: Streams{Tool: true}, RunID: "first"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := New(Options{Dir: dir, Streams: Streams{Tool: true}, RunID: "second"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	const perWriter = 10
	var wg sync.WaitGroup
	for i, writer := range []*Writer{first, second} {
		wg.Add(1)
		go func(id int, writer *Writer) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				writer.Write(Record{Kind: KindTool, AgentID: fmt.Sprintf("writer-%d-record-%d", id, j)})
			}
		}(i, writer)
	}
	wg.Wait()

	records := readRecords(t, filepath.Join(dir, "tool.jsonl"))
	if len(records) != 2*perWriter {
		t.Fatalf("records = %d, want %d", len(records), 2*perWriter)
	}
	seen := make(map[string]bool, len(records))
	for _, rec := range records {
		seen[rec.AgentID] = true
		if rec.RunID != "first" && rec.RunID != "second" {
			t.Errorf("record %q run id = %q, want one of the two writers", rec.AgentID, rec.RunID)
		}
	}
	if len(seen) != 2*perWriter {
		t.Errorf("distinct records = %d, want %d", len(seen), 2*perWriter)
	}
}
