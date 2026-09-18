package usagestats

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestTelemetrySeqMatchesLineOrderUnderConcurrency proves the sequence number
// and its append happen in one critical section: the persisted line order must
// match the seq order, not the goroutine schedule.
func TestTelemetrySeqMatchesLineOrderUnderConcurrency(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	t.Setenv(TelemetryEnvVar, path)
	t.Setenv(TelemetryRunEnvVar, "run-seq")

	tel := newTelemetryFromEnv()
	if tel == nil {
		t.Fatal("telemetry disabled, want enabled")
	}
	t.Cleanup(func() {
		_ = tel.file.Close()
	})

	const goroutines = 8
	const perGoroutine = 25

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				tel.record(Observation{Source: SourceParent}, time.Now())
			}
		}()
	}
	wg.Wait()

	lines := readTelemetryLines(t, path)
	if got, want := len(lines), goroutines*perGoroutine; got != want {
		t.Fatalf("telemetry lines = %d, want %d", got, want)
	}
	for i, line := range lines {
		if line.Seq != int64(i+1) {
			t.Fatalf("line %d has seq %d, want %d", i, line.Seq, i+1)
		}
	}
}
