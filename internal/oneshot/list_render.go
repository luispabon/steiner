package oneshot

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// PrintManifest writes manifest to w as indented JSON followed by a newline.
func PrintManifest(w io.Writer, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal oneshot manifest: %w", err)
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

// RenderRuns writes runs to w as a table. relTime formats each run's last
// update time. A nil w writes nothing.
func RenderRuns(w io.Writer, runs []ResumableRun, relTime func(time.Time) string) error {
	if w == nil {
		return nil
	}
	if len(runs) == 0 {
		_, err := fmt.Fprintf(w, "no resumable oneshot runs\n")
		return err
	}

	if _, err := fmt.Fprintf(w, "%-4s %-16s %-20s %-18s %-12s %-18s %s\n", "#", "Run ID", "Phase", "Status", "Lock", "Updated", "Task"); err != nil {
		return err
	}
	for i, run := range runs {
		task := run.Task
		if len(task) > 48 {
			task = task[:45] + "..."
		}
		if _, err := fmt.Fprintf(w, "%-4d %-16s %-20s %-18s %-12s %-18s %s\n", i, run.RunID, string(run.ResumePhase), run.Status, run.LockState, relTime(run.UpdatedAt), task); err != nil {
			return err
		}
	}
	return nil
}
