package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A failed read (for example, a file over the size cap) is returned with no
// path, so the tracker must not record it as observed.
func TestFileTrackerIgnoresFailedReadResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	failed, err := json.Marshal(readResult{Output: "Error: file too large (max 1MB, got 2.0MB)"})
	if err != nil {
		t.Fatal(err)
	}
	var tracker FileTracker
	tracker.RecordRead(1, string(failed))
	if tracker.WasObserved(path) {
		t.Fatal("failed read recorded as observed")
	}
}

// A successful read of an empty file keeps its path and counts as observed.
func TestFileTrackerRecordsEmptyFileRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := json.Marshal(readResult{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	var tracker FileTracker
	tracker.RecordRead(1, string(ok))
	if !tracker.WasObserved(path) {
		t.Fatal("successful empty-file read not recorded as observed")
	}
}
