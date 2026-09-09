package diagnostics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func allStreams() Streams {
	return Streams{Cache: true, Provider: true, Tool: true}
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
		{name: "all", streams: allStreams(), want: map[Kind]bool{KindCache: true, KindProvider: true, KindTool: true}},
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
