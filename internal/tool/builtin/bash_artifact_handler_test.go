package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func runBashInDir(t *testing.T, dir string, input map[string]any) *BashResult {
	t.Helper()
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	toolDef := NewBashTool(Env{WorkDir: dir, PathPolicy: &policy})
	resI, err := toolDef.Handler(withUnsandboxedWrapper(context.Background()), input)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	return resI.(*BashResult)
}

func skipUnlessUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("artifact store is unix only")
	}
}

func TestBashHandlerNoArtifactUnderLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	res := runBashInDir(t, dir, map[string]any{"command": "echo hi; echo e >&2", "max_output_bytes": 100})
	if res.ArtifactStatus != "none" || res.FilePath != "" || res.Truncated || res.ArtifactLimitHit || res.Message != "" {
		t.Errorf("unexpected artifact fields: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, ".steiner")); err == nil {
		t.Error(".steiner created without an artifact")
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"artifact_status":"none"`) {
		t.Errorf("JSON %s lacks artifact_status none", data)
	}
}

func TestBashHandlerCompleteArtifact(t *testing.T) {
	t.Parallel()
	skipUnlessUnix(t)
	for _, raw := range []bool{false, true} {
		dir := t.TempDir()
		res := runBashInDir(t, dir, map[string]any{"command": "seq 1 20000; echo oops >&2", "max_output_bytes": 300, "raw_output": raw})
		if res.ArtifactStatus != "complete" || res.FilePath == "" || res.ArtifactLimitHit || !res.Truncated {
			t.Fatalf("raw=%v: %+v", raw, res)
		}
		if len(res.Output) > 300 {
			t.Errorf("raw=%v: output len %d > 300", raw, len(res.Output))
		}
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(res.FilePath)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(body), "[stdout]\n1\n2\n") || !strings.Contains(string(body), "20000\n\n[stderr]\noops\n") {
			t.Errorf("raw=%v: artifact body wrong (len %d)", raw, len(body))
		}
		for _, want := range []string{res.FilePath, "complete", "stdout "} {
			if !strings.Contains(res.Message, want) {
				t.Errorf("raw=%v: message %q lacks %q", raw, res.Message, want)
			}
		}
		if !strings.Contains(res.Message, "clips lines") {
			t.Errorf("message lacks long-line limitation: %q", res.Message)
		}
	}
}

func TestBashHandlerNonzeroExitStderrPriority(t *testing.T) {
	t.Parallel()
	skipUnlessUnix(t)
	res := runBashInDir(t, t.TempDir(), map[string]any{"command": "seq 1 5000; echo FATAL >&2; (exit 4)", "max_output_bytes": 200})
	if res.ExitCode != 4 || !strings.Contains(res.Output, "[stderr]\nFATAL\n") || len(res.Output) > 200 {
		t.Errorf("exit=%d output=%q", res.ExitCode, res.Output)
	}
}

func TestBashHandlerCaptureLossIsPartial(t *testing.T) {
	t.Parallel()
	skipUnlessUnix(t)
	dir := t.TempDir()
	res := runBashInDir(t, dir, map[string]any{"command": "yes x | head -c 5000000; echo LAST", "max_output_bytes": 500})
	if res.ArtifactStatus != "partial" || res.FilePath == "" || res.ExitCode != 0 {
		t.Fatalf("status=%q path=%q exit=%d", res.ArtifactStatus, res.FilePath, res.ExitCode)
	}
	fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(res.FilePath)))
	if err != nil || fi.Size() > bashArtifactMaxBytes {
		t.Errorf("artifact stat: %v size=%v", err, fi)
	}
	if !strings.HasSuffix(res.Output, "LAST\n") || !strings.Contains(res.Message, "capture gap") || !strings.Contains(res.Message, "raw source bytes") || strings.Contains(res.Message, "Full output saved") {
		t.Errorf("output tail %q message %q", res.Output[max(0, len(res.Output)-20):], res.Message)
	}
}

func TestBashHandlerArtifactCapHit(t *testing.T) {
	t.Parallel()
	skipUnlessUnix(t)
	// 1.5 MiB is fully captured (head and tail cover it) but exceeds the 1 MiB artifact cap.
	res := runBashInDir(t, t.TempDir(), map[string]any{"command": "yes x | head -c 1572864", "max_output_bytes": 500})
	if res.ArtifactStatus != "partial" || !res.ArtifactLimitHit {
		t.Errorf("status=%q limitHit=%v message=%q", res.ArtifactStatus, res.ArtifactLimitHit, res.Message)
	}
	if !strings.Contains(res.Message, "artifact size cap") || strings.Contains(res.Message, "capture gap") {
		t.Errorf("message %q", res.Message)
	}
}

func TestBashHandlerTimeoutArtifactPartial(t *testing.T) {
	t.Parallel()
	skipUnlessUnix(t)
	res := runBashInDir(t, t.TempDir(), map[string]any{"command": "seq 1 5000; sleep 30", "timeout_seconds": 1, "max_output_bytes": 300})
	if !res.TimedOut || res.ExitCode == 0 || res.ArtifactStatus != "partial" || res.FilePath == "" || res.ArtifactLimitHit {
		t.Errorf("%+v", res)
	}
	if !strings.Contains(res.Message, "deadline exceeded") || !strings.Contains(res.Message, "did not finish normally") || len(res.Output) > 300 {
		t.Errorf("message %q output len %d", res.Message, len(res.Output))
	}
}

func TestBashHandlerSaveFailureKeepsOutcome(t *testing.T) {
	t.Parallel()
	skipUnlessUnix(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".steiner"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := runBashInDir(t, dir, map[string]any{"command": "seq 1 5000; (exit 5)", "max_output_bytes": 200})
	if res.ArtifactStatus != "failed" || res.FilePath != "" || res.ExitCode != 5 || !res.Truncated {
		t.Errorf("%+v", res)
	}
	if !strings.Contains(res.Message, "not saved") || !strings.Contains(res.Output, "bytes omitted") {
		t.Errorf("message %q output %q", res.Message, res.Output)
	}
}

func TestBashHandlerTinyBudgetMessageNamesStreams(t *testing.T) {
	t.Parallel()
	res := runBashInDir(t, t.TempDir(), map[string]any{"command": "echo some out; echo some err >&2", "max_output_bytes": 3})
	if len(res.Output) > 3 || !strings.Contains(res.Message, "stdout ") || !strings.Contains(res.Message, "stderr ") {
		t.Errorf("output %q message %q", res.Output, res.Message)
	}
}
