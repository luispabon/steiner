package diagnostics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedStream writes raw JSONL lines so retention can be exercised against real
// on-disk timestamps without injecting a clock into the writer.
func seedStream(t *testing.T, path string, ages ...time.Duration) {
	t.Helper()
	var data []byte
	for i, age := range ages {
		rec := Record{
			Timestamp: time.Now().UTC().Add(-age),
			Kind:      KindCache,
			Seq:       int64(i + 1),
			AgentID:   ageLabel(age),
		}
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal seed record: %v", err)
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
}

func ageLabel(age time.Duration) string {
	return age.Truncate(time.Hour).String()
}

func TestRetentionDropsStaleRecordsOnOpen(t *testing.T) {
	tests := []struct {
		name          string
		retentionDays int
		ages          []time.Duration
		wantLabels    []string
	}{
		{
			name:          "drops only records past the cutoff",
			retentionDays: 30,
			ages:          []time.Duration{90 * 24 * time.Hour, 40 * 24 * time.Hour, 2 * 24 * time.Hour},
			wantLabels:    []string{ageLabel(2 * 24 * time.Hour)},
		},
		{
			name:          "keeps everything inside the window",
			retentionDays: 30,
			ages:          []time.Duration{5 * 24 * time.Hour, time.Hour},
			wantLabels:    []string{ageLabel(5 * 24 * time.Hour), ageLabel(time.Hour)},
		},
		{
			name:          "zero retention disables pruning",
			retentionDays: 0,
			ages:          []time.Duration{400 * 24 * time.Hour},
			wantLabels:    []string{ageLabel(400 * 24 * time.Hour)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "cache.jsonl")
			seedStream(t, path, tt.ages...)

			w, err := New(Options{Dir: dir, Streams: Streams{Cache: true}, RetentionDays: tt.retentionDays})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			records := readRecords(t, path)
			if len(records) != len(tt.wantLabels) {
				t.Fatalf("records = %d, want %d", len(records), len(tt.wantLabels))
			}
			for i, want := range tt.wantLabels {
				if records[i].AgentID != want {
					t.Errorf("record %d = %q, want %q", i, records[i].AgentID, want)
				}
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("mode after retention = %o, want 600", info.Mode().Perm())
			}
		})
	}
}

func TestRetentionSurvivesUndatableLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.jsonl")
	if err := os.WriteFile(path, []byte("not json\n"), 0o600); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	w, err := New(Options{Dir: dir, Streams: Streams{Cache: true}, RetentionDays: 30})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	w.Write(Record{Kind: KindCache, AgentID: "fresh"})
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	records := readRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "fresh" {
		t.Fatalf("records = %+v, want only the freshly written record", records)
	}
}
