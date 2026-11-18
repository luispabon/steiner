package diagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// subprocessHelperEnv marks a re-executed test binary as a diagnostics writer
// helper. Its value encodes "dir|run_id|count|payload_bytes".
const subprocessHelperEnv = "STEINER_DIAGNOSTICS_HELPER"

// subprocessHelperTimeout bounds one helper process, so a stream lock that never
// frees fails the test with the child's stderr instead of hanging the suite.
const subprocessHelperTimeout = 120 * time.Second

// TestDiagnosticsSubprocessHelper is the child half of the cross-process tests.
// The parent re-executes this binary with subprocessHelperEnv set; without it
// the test returns immediately and the normal suite ignores it.
func TestDiagnosticsSubprocessHelper(t *testing.T) {
	spec := os.Getenv(subprocessHelperEnv)
	if spec == "" {
		return
	}
	if err := runSubprocessHelper(spec); err != nil {
		fmt.Fprintf(os.Stderr, "diagnostics helper: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// runSubprocessHelper writes spec's records through a real Writer and closes it.
// Write reports nothing and a dropped record is indistinguishable from a written
// one, so the parent's record counts are the check that the writes landed.
func runSubprocessHelper(spec string) error {
	parts := strings.Split(spec, "|")
	if len(parts) != 4 {
		return fmt.Errorf("spec %q: want dir|run_id|count|payload_bytes", spec)
	}
	dir, runID := parts[0], parts[1]
	count, err := strconv.Atoi(parts[2])
	if err != nil {
		return fmt.Errorf("spec %q: count: %w", spec, err)
	}
	payloadBytes, err := strconv.Atoi(parts[3])
	if err != nil {
		return fmt.Errorf("spec %q: payload bytes: %w", spec, err)
	}
	w, err := New(Options{
		Dir:           dir,
		RunID:         runID,
		Streams:       Streams{Tool: true},
		RetentionDays: 0,
	})
	if err != nil {
		return fmt.Errorf("open writer in %s: %w", dir, err)
	}
	filler := strings.Repeat("x", payloadBytes)
	for i := 0; i < count; i++ {
		w.Write(Record{Kind: KindTool, Payload: map[string]any{"filler": filler}})
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer in %s: %w", dir, err)
	}
	return nil
}

// subprocessHelper is one child writer, started but not yet waited for. Both
// helpers of a test are started before either is waited for, so they really do
// race each other.
type subprocessHelper struct {
	label  string
	cmd    *exec.Cmd
	stderr bytes.Buffer
	done   chan error
}

// startSubprocessHelper re-executes the test binary in helper mode writing count
// records of payloadBytes to dir's tool stream under runID.
func startSubprocessHelper(t *testing.T, dir, runID string, count, payloadBytes int) *subprocessHelper {
	t.Helper()
	h := &subprocessHelper{label: runID}
	h.cmd = exec.Command(os.Args[0], "-test.run=^TestDiagnosticsSubprocessHelper$")
	spec := fmt.Sprintf("%s|%s|%d|%d", dir, runID, count, payloadBytes)
	h.cmd.Env = append(os.Environ(), subprocessHelperEnv+"="+spec)
	h.cmd.Stderr = &h.stderr
	if err := h.cmd.Start(); err != nil {
		t.Fatalf("start helper %s: %v", runID, err)
	}
	h.done = make(chan error, 1)
	go func() { h.done <- h.cmd.Wait() }()
	return h
}

// wait blocks until the helper exits, killing it and failing with its stderr
// when it outlives subprocessHelperTimeout.
func (h *subprocessHelper) wait(t *testing.T) {
	t.Helper()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("helper %s failed: %v\nstderr:\n%s", h.label, err, h.stderr.String())
		}
	case <-time.After(subprocessHelperTimeout):
		_ = h.cmd.Process.Kill()
		err := <-h.done
		t.Fatalf("helper %s still running after %s (killed: %v)\nstderr:\n%s", h.label, subprocessHelperTimeout, err, h.stderr.String())
	}
}

// streamGenerationPaths lists the existing files of kind's stream in dir, the
// active file first and then its rotated generations.
func streamGenerationPaths(t *testing.T, dir string, kind Kind) []string {
	t.Helper()
	path := filepath.Join(dir, kind.fileName())
	var paths []string
	for gen := 0; gen <= maxStreamGenerations; gen++ {
		p := generationPath(path, gen)
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		paths = append(paths, p)
	}
	return paths
}

// decodeStreamLines decodes one stream file line by line, reporting every line
// that is not a complete record with its file and line number rather than
// stopping at the first one.
func decodeStreamLines(t *testing.T, path string) []Record {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []Record
	for i, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Errorf("%s line %d: invalid record: %v", path, i+1, err)
			continue
		}
		out = append(out, rec)
	}
	return out
}

// TestSeparateProcessesAppendWithoutCorruption runs two real processes writing
// below the rotation cap. Every record both wrote must survive as its own line:
// no interleaved bytes, no lost record and no rotation at this size.
func TestSeparateProcessesAppendWithoutCorruption(t *testing.T) {
	dir := t.TempDir()
	skipUnlessStreamLocksSupported(t, dir, KindTool)

	const (
		perProcess   = 150
		payloadBytes = 1024
	)
	first := startSubprocessHelper(t, dir, "run-a", perProcess, payloadBytes)
	second := startSubprocessHelper(t, dir, "run-b", perProcess, payloadBytes)
	first.wait(t)
	second.wait(t)

	paths := streamGenerationPaths(t, dir, KindTool)
	if len(paths) != 1 {
		t.Fatalf("stream files = %v, want only the active file below the rotation cap", paths)
	}
	records := readRecords(t, paths[0])
	if len(records) != 2*perProcess {
		t.Fatalf("records = %d, want %d (one per write from both processes)", len(records), 2*perProcess)
	}
	for _, rec := range records {
		if rec.RunID != "run-a" && rec.RunID != "run-b" {
			t.Errorf("record seq %d run id = %q, want one of the two helpers", rec.Seq, rec.RunID)
		}
		payload, ok := rec.Payload.(map[string]any)
		if !ok {
			t.Errorf("record seq %d payload = %#v, want an object", rec.Seq, rec.Payload)
			continue
		}
		filler, _ := payload["filler"].(string)
		if len(filler) != payloadBytes {
			t.Errorf("record seq %d filler = %d bytes, want %d", rec.Seq, len(filler), payloadBytes)
		}
	}
}

// TestSeparateProcessesRotateWithoutCorruption runs two real processes far past
// the rotation cap, so their rotations overlap. Every record in the active file
// and in each surviving generation must decode, must belong to one of the two
// processes, and must appear once; a rotation interrupted or fought over by the
// other process would leave a torn line, a repeated record or a stage artefact.
// An exact total is not asserted because rotation discards the oldest
// generation.
func TestSeparateProcessesRotateWithoutCorruption(t *testing.T) {
	dir := t.TempDir()
	skipUnlessStreamLocksSupported(t, dir, KindTool)

	const (
		perProcess   = 400
		payloadBytes = 64 * 1024
	)
	first := startSubprocessHelper(t, dir, "run-a", perProcess, payloadBytes)
	second := startSubprocessHelper(t, dir, "run-b", perProcess, payloadBytes)
	first.wait(t)
	second.wait(t)

	paths := streamGenerationPaths(t, dir, KindTool)
	if len(paths) < 2 {
		t.Fatalf("stream files = %v, want at least one rotated generation", paths)
	}
	seen := make(map[string]struct{}, 2*perProcess)
	for _, path := range paths {
		for _, rec := range decodeStreamLines(t, path) {
			if rec.RunID != "run-a" && rec.RunID != "run-b" {
				t.Errorf("%s: run id = %q, want one of the two helpers", path, rec.RunID)
			}
			key := fmt.Sprintf("%s/%d", rec.RunID, rec.Seq)
			if _, ok := seen[key]; ok {
				t.Errorf("%s: duplicate record %s", path, key)
			}
			seen[key] = struct{}{}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no records decoded from any stream file")
	}
	if artefacts := rotationArtefacts(t, dir); len(artefacts) > 0 {
		t.Errorf("leftover rotation artefacts = %v, want a completed transaction", artefacts)
	}
}
