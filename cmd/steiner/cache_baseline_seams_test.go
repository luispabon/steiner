package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// TestWireInteractiveRunnerInstallsSessionCacheBaseline calls the real
// interactive runner construction seam and asserts the wired session runner
// carries a non-nil cache baseline store, with one store per session.
func TestWireInteractiveRunnerInstallsSessionCacheBaseline(t *testing.T) {
	newSession := func(t *testing.T) *interactive.Session {
		t.Helper()
		rt := cliRuntime{events: output.NoopSink{}, workDir: t.TempDir(), homeDir: t.TempDir()}
		sess, err := buildInteractiveSession(rt)
		if err != nil {
			t.Fatalf("buildInteractiveSession() error = %v", err)
		}
		wireInteractiveRunner(rt, sess)
		return sess
	}

	firstRunner := wiredSessionRunner(t, newSession(t))
	if firstRunner.runner.cacheBaseline == nil {
		t.Fatal("interactive session runner cacheBaseline = nil, want session-owned store")
	}

	secondRunner := wiredSessionRunner(t, newSession(t))
	if secondRunner.runner.cacheBaseline == nil {
		t.Fatal("second interactive session runner cacheBaseline = nil, want session-owned store")
	}
	if firstRunner.runner.cacheBaseline == secondRunner.runner.cacheBaseline {
		t.Fatal("two interactive sessions share one baseline store, want per-session stores")
	}
}

// wiredSessionRunner reads back the run executor wireInteractiveRunner installed
// on sess. interactive.Session keeps its runner unexported (deps.Runner) and
// offers no accessor, so the test reads the field through unsafe rather than
// adding production API surface for tests. Nothing outside this test reads it.
func wiredSessionRunner(t *testing.T, sess *interactive.Session) sessionRunner {
	t.Helper()
	field := reflect.ValueOf(sess).Elem().FieldByName("deps").FieldByName("Runner")
	if !field.IsValid() {
		t.Fatal("interactive.Session has no deps.Runner field to inspect")
	}
	value := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()
	runner, ok := value.(sessionRunner)
	if !ok {
		t.Fatalf("wired runner = %T, want sessionRunner", value)
	}
	return runner
}

// TestRunOneshotTaskPhaseFactorySharesCacheBaseline drives the real
// runOneshotTask construction path (with runtime construction stubbed) and
// asserts every phase runner it hands the orchestrator carries the same
// non-nil baseline store.
func TestRunOneshotTaskPhaseFactorySharesCacheBaseline(t *testing.T) {
	factory := captureOneshotPhaseFactory(t, func() error {
		cmd := newRootCommand()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		return runOneshotTask(cmd, &cliFlags{}, "baseline task")
	})

	first := oneshotPhaseRunnerStore(t, factory, oneshot.PhasePlan)
	second := oneshotPhaseRunnerStore(t, factory, oneshot.PhaseImplement)
	if first == nil {
		t.Fatal("plan phase runner cacheBaseline = nil, want oneshot-owned store")
	}
	if first != second {
		t.Fatalf("phase runners got different stores (%p vs %p), want one per oneshot execution", first, second)
	}
}

// TestRunOneshotResumePhaseFactorySharesCacheBaseline is the resume-path
// counterpart of the task test above.
func TestRunOneshotResumePhaseFactorySharesCacheBaseline(t *testing.T) {
	projectRoot := t.TempDir()
	identity := oneshot.RunIdentity{ID: "run-resume", Slug: "resume-slug"}
	store := oneshot.NewManifestStore(identity.ManifestPath(projectRoot))
	if err := store.Write(oneshot.Manifest{RunID: identity.ID, Slug: identity.Slug, Task: "resume task"}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	factory := captureOneshotPhaseFactory(t, func() error {
		cmd := newRootCommand()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		return runOneshotResume(cmd, &cliFlags{}, identity.ID)
	}, projectRoot)

	first := oneshotPhaseRunnerStore(t, factory, oneshot.PhasePlan)
	second := oneshotPhaseRunnerStore(t, factory, oneshot.PhaseReview)
	if first == nil {
		t.Fatal("resume plan phase runner cacheBaseline = nil, want oneshot-owned store")
	}
	if first != second {
		t.Fatalf("resume phase runners got different stores (%p vs %p), want one per oneshot execution", first, second)
	}
}

// captureOneshotPhaseFactory runs invoke (a real oneshot entrypoint) with the
// runtime builder and orchestrator stubbed, and returns the phase runner
// factory the entrypoint handed to the orchestrator.
func captureOneshotPhaseFactory(t *testing.T, invoke func() error, projectRoots ...string) oneshot.PhaseRunnerFactory {
	t.Helper()
	projectRoot := ""
	if len(projectRoots) > 0 {
		projectRoot = projectRoots[0]
	}

	oldBuildRuntime := buildRuntime
	t.Cleanup(func() { buildRuntime = oldBuildRuntime })
	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
		return cliRuntime{
			cfg:         config.Config{SubAgent: config.SubAgentConfig{Enabled: true}},
			projectRoot: projectRoot,
			events:      output.NoopSink{},
		}, nil
	}

	oldBuildPhaseRuntime := buildPhaseRuntime
	t.Cleanup(func() { buildPhaseRuntime = oldBuildPhaseRuntime })
	buildPhaseRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags, _, _, _ string) (cliRuntime, error) {
		return cliRuntime{}, nil
	}

	var captured oneshot.PhaseRunnerFactory
	oldOrchestrator := newOneshotOrchestrator
	t.Cleanup(func() { newOneshotOrchestrator = oldOrchestrator })
	newOneshotOrchestrator = func(deps oneshot.Dependencies) (oneshotOrchestrator, error) {
		captured = deps.RunnerFactory
		return &fakeOneshotOrchestrator{}, nil
	}

	if err := invoke(); err != nil {
		t.Fatalf("oneshot entrypoint error = %v", err)
	}
	if captured == nil {
		t.Fatal("oneshot entrypoint did not hand a phase runner factory to the orchestrator")
	}
	return captured
}

// oneshotPhaseRunnerStore builds one phase runner through the production
// factory and returns the cache baseline store its cliRunner carries.
func oneshotPhaseRunnerStore(t *testing.T, factory oneshot.PhaseRunnerFactory, phase oneshot.Phase) *agent.CacheBaselineStore {
	t.Helper()
	runner, err := factory.NewPhaseRunner(context.Background(), phase, "alias", nil, config.AdvisorConfig{})
	if err != nil {
		t.Fatalf("NewPhaseRunner(%s) error = %v", phase, err)
	}
	got, ok := runner.(phaseRunner)
	if !ok {
		t.Fatalf("NewPhaseRunner(%s) = %T, want phaseRunner", phase, runner)
	}
	return got.runner.cacheBaseline
}

// TestExecModeRunRequestCarriesCacheBaseline drives the real --exec command
// (with runtime construction stubbed) through two model calls: the first
// response asks for a tool call, forcing a second outbound request. Only a
// non-nil baseline store can report a known predecessor on that second request,
// so a true value proves runExecMode's inline cliRunner carried a store.
func TestExecModeRunRequestCarriesCacheBaseline(t *testing.T) {
	oldBuildRuntime := buildRuntime
	t.Cleanup(func() { buildRuntime = oldBuildRuntime })

	diagnosticsDir := t.TempDir()
	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
		writer, err := diagnostics.New(diagnostics.Options{Dir: diagnosticsDir, Streams: diagnostics.Streams{Cache: true}})
		if err != nil {
			return cliRuntime{}, err
		}
		return cliRuntime{
			cfg: testRuntimeConfig("test-model"),
			provider: &fakeProvider{responses: []provider.ChatResponse{
				{
					Message: provider.Message{
						Role:      provider.MessageRoleAssistant,
						ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "probe"}},
					},
					FinishReason: "tool_calls",
					Usage:        &provider.UsageStats{TotalTokens: 2},
				},
				{
					Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "answer"},
					FinishReason: "stop",
					Usage:        &provider.UsageStats{TotalTokens: 2},
				},
			}},
			diagnostics: writer,
			registry: tool.NewRegistry(tool.ToolDef{
				Name:            "probe",
				Description:     "test probe",
				ParameterSchema: map[string]any{"type": "object"},
				ParallelSafe:    true,
				Handler:         func(context.Context, map[string]any) (any, error) { return "probe ok", nil },
			}),
			workDir:     t.TempDir(),
			homeDir:     t.TempDir(),
			human:       output.NewStream(io.Discard),
			status:      output.NewStream(io.Discard),
			events:      output.NoopSink{},
			sharedInput: bufio.NewReader(strings.NewReader("y\n")),
		}, nil
	}

	cmd := newRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--exec", "baseline prompt"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	records := readCacheDiagnosticRecords(t, diagnosticsDir)
	if len(records) < 2 {
		t.Fatalf("cache diagnostics records = %d, want at least 2 (one per issued request)", len(records))
	}
	for i, record := range records[1:] {
		payload, ok := record["payload"].(map[string]any)
		if !ok {
			t.Fatalf("record %d payload = %#v, want object", i+1, record["payload"])
		}
		if known, _ := payload["prefix_predecessor_known"].(bool); !known {
			t.Fatalf("record %d prefix_predecessor_known = %v, want true: exec run request carried no cache baseline", i+1, payload["prefix_predecessor_known"])
		}
	}
}

// readCacheDiagnosticRecords returns every kind:"cache" record written under
// dir, parsed as generic JSON so the test does not depend on the envelope
// struct's tags.
func readCacheDiagnosticRecords(t *testing.T, dir string) []map[string]any {
	t.Helper()
	var records []map[string]any
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				continue
			}
			if record["kind"] == string(diagnostics.KindCache) {
				records = append(records, record)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read diagnostics dir: %v", err)
	}
	return records
}
