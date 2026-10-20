package oneshot

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// failingWriter fails every write so error propagation can be checked.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

// padCell pads s with spaces to width, matching the %-Ns verbs in RenderRuns.
func padCell(s string, width int) string {
	return s + strings.Repeat(" ", width-len(s))
}

func TestPrintManifest(t *testing.T) {
	var buf bytes.Buffer
	if err := PrintManifest(&buf, Manifest{RunID: "run-123"}); err != nil {
		t.Fatalf("PrintManifest() error = %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"run_id": "run-123"`) {
		t.Fatalf("PrintManifest() output missing run_id:\n%s", got)
	}
	if !strings.HasSuffix(got, "}\n") {
		t.Fatalf("PrintManifest() output = %q, want indented JSON with trailing newline", got)
	}
}

func TestPrintManifestWriteError(t *testing.T) {
	if err := PrintManifest(failingWriter{}, Manifest{}); err == nil {
		t.Fatal("PrintManifest() error = nil, want write error")
	}
}

func TestRenderRuns(t *testing.T) {
	updated := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	dateOnly := func(ts time.Time) string { return ts.Format("2006-01-02") }
	header := padCell("#", 4) + " " + padCell("Run ID", 16) + " " + padCell("Phase", 20) + " " +
		padCell("Status", 18) + " " + padCell("Lock", 12) + " " + padCell("Updated", 18) + " " + "Task\n"

	tests := []struct {
		name string
		runs []ResumableRun
		want string
	}{
		{
			name: "no runs",
			want: "no resumable oneshot runs\n",
		},
		{
			name: "one run",
			runs: []ResumableRun{{
				RunID:       "run-1",
				ResumePhase: PhaseReview,
				Status:      "running",
				LockState:   "held",
				UpdatedAt:   updated,
				Task:        "Build parser",
			}},
			want: header +
				padCell("0", 4) + " " + padCell("run-1", 16) + " " + padCell("review", 20) + " " +
				padCell("running", 18) + " " + padCell("held", 12) + " " + padCell("2024-01-15", 18) + " " +
				"Build parser\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := RenderRuns(&buf, tt.runs, dateOnly); err != nil {
				t.Fatalf("RenderRuns() error = %v", err)
			}
			if got := buf.String(); got != tt.want {
				t.Fatalf("RenderRuns() output mismatch\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestRenderRunsTruncatesTask(t *testing.T) {
	longTask := strings.Repeat("x", 60)
	var buf bytes.Buffer
	err := RenderRuns(&buf, []ResumableRun{{RunID: "run-2", Task: longTask}}, func(time.Time) string { return "now" })
	if err != nil {
		t.Fatalf("RenderRuns() error = %v", err)
	}
	want := strings.Repeat("x", 45) + "...\n"
	if !strings.HasSuffix(buf.String(), want) {
		t.Fatalf("RenderRuns() output = %q, want task truncated to %q", buf.String(), want)
	}
}

func TestRenderRunsNilWriter(t *testing.T) {
	if err := RenderRuns(nil, []ResumableRun{{RunID: "run-3"}}, func(time.Time) string { return "" }); err != nil {
		t.Fatalf("RenderRuns(nil) error = %v, want nil", err)
	}
}

func TestRenderRunsWriteError(t *testing.T) {
	noRuns := RenderRuns(failingWriter{}, nil, func(time.Time) string { return "" })
	if noRuns == nil {
		t.Fatal("RenderRuns() with no runs error = nil, want write error")
	}
	err := RenderRuns(failingWriter{}, []ResumableRun{{RunID: "run-4"}}, func(time.Time) string { return "" })
	if err == nil {
		t.Fatal("RenderRuns() error = nil, want write error")
	}
}
