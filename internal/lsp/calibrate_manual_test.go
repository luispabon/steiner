//go:build lspcalib

// Package lsp's calibration harness. Manual only: it drives a real gopls
// process against this repository to derive the timeout defaults in
// internal/config/defaults.go. Guarded by the lspcalib build tag so `go test
// ./...` never depends on a language server being installed.
//
//	go test -tags lspcalib ./internal/lsp/ -run TestCalibrate -v -timeout 30m
package lsp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

const (
	// calibRuns is the sample count for every timed category.
	calibRuns = 5
	// calibLoadCeiling bounds a single workspace load before the run is failed.
	calibLoadCeiling = 3 * time.Minute
	// calibDiagCeiling is how long we keep watching for further publications
	// after the first one, to test the D13 second-pass claim.
	calibDiagCeiling = 10 * time.Second
)

// TestCalibrate records the measurements behind the LSP timeout defaults.
// Every subtest logs raw per-run numbers; the transcript is transcribed into
// docs/lsp.md under "Timeout calibration".
func TestCalibrate(t *testing.T) {
	gopls := resolveGopls(t)
	root := repoRoot(t)
	t.Logf("gopls=%s root=%s", gopls, root)
	t.Logf("gopls version: %s", goplsVersion(t, gopls))

	t.Run("WorkspaceLoad", func(t *testing.T) { calibWorkspaceLoad(t, gopls, root) })
	t.Run("Diagnostics", func(t *testing.T) { calibDiagnostics(t, gopls, root) })
	t.Run("Definition", func(t *testing.T) { calibDefinition(t, gopls, root) })
	t.Run("NoProgressGrace", func(t *testing.T) { calibNoProgressGrace(t, gopls, root) })
}

// calibWorkspaceLoad times spawn-to-first-complete-progress-cycle on a cold
// cache (fresh per-root cache dir, as a first-ever run for a workspace sees)
// and on a warm cache (the dir left behind by the preceding run).
func calibWorkspaceLoad(t *testing.T, gopls, root string) {
	coldCycle := make([]time.Duration, 0, calibRuns)
	warmCycle := make([]time.Duration, 0, calibRuns)
	coldBegin := make([]time.Duration, 0, calibRuns)
	warmBegin := make([]time.Duration, 0, calibRuns)

	for i := 0; i < calibRuns; i++ {
		cacheDir := t.TempDir()

		l := timeOneLoad(t, gopls, root, cacheDir)
		coldCycle = append(coldCycle, l.cycle)
		coldBegin = append(coldBegin, l.firstBegin)
		t.Logf("cold run %d: first begin %s, complete cycle %s", i+1, l.firstBegin, l.cycle)

		l = timeOneLoad(t, gopls, root, cacheDir)
		warmCycle = append(warmCycle, l.cycle)
		warmBegin = append(warmBegin, l.firstBegin)
		t.Logf("warm run %d: first begin %s, complete cycle %s", i+1, l.firstBegin, l.cycle)
	}

	report(t, "cold workspace load (begin->end)", coldCycle)
	report(t, "warm workspace load (begin->end)", warmCycle)
	report(t, "cold spawn to first begin", coldBegin)
	report(t, "warm spawn to first begin", warmBegin)
}

// load holds the two timings of one workspace load: how long the server took
// to announce any work at all (which ReadyGracePeriod races against) and how
// long the first complete begin/end cycle took (which ReadyTimeout bounds).
type load struct {
	firstBegin time.Duration
	cycle      time.Duration
}

// timeOneLoad spawns a server, times its workspace load, and shuts it down.
func timeOneLoad(t *testing.T, gopls, root, cacheDir string) load {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sess, l := loadUntilReady(t, ctx, gopls, root, cacheDir)
	closeSession(t, sess)
	return l
}

// loadUntilReady spawns a server through the production transport and blocks
// until a begin/end progress cycle completes, mirroring trackReadiness.
func loadUntilReady(t *testing.T, ctx context.Context, gopls, root, cacheDir string) (*impl, load) {
	t.Helper()

	start := time.Now()
	sess, err := newTransport(ctx, ctx, TransportSpec{
		Command:  gopls,
		Env:      calibEnv(cacheDir),
		RootPath: root,
	})
	if err != nil {
		t.Fatalf("spawn gopls: %v", err)
	}

	open := make(map[string]struct{})
	var l load
	ceiling := time.After(calibLoadCeiling)

	for {
		select {
		case ev := <-sess.Progress():
			switch ev.Kind {
			case "begin":
				if len(open) == 0 && l.firstBegin == 0 {
					l.firstBegin = time.Since(start)
				}
				open[ev.Token] = struct{}{}
			case "end":
				if _, ok := open[ev.Token]; ok {
					l.cycle = time.Since(start)
					return sess.(*impl), l
				}
			}
		case <-sess.Diagnostics():
			// Drained so the notification handler never blocks on a full channel.
		case <-sess.Exited():
			t.Fatalf("gopls exited during workspace load")
		case <-ceiling:
			t.Fatalf("workspace load did not complete within %s", calibLoadCeiling)
		}
	}
}

// publication records one publishDiagnostics arrival for the file under test.
type publication struct {
	offset  time.Duration
	items   int
	version string
}

// calibDiagnostics tests the D13 claim that gopls publishes a second, slower
// diagnostics pass for a file after the first. It opens a file and records
// every publication for that file within calibDiagCeiling of didOpen, both for
// the file as it is on disk (clean) and for an overlay carrying a real error.
func calibDiagnostics(t *testing.T, gopls, root string) {
	target := filepath.Join(root, "internal", "lsp", "session.go")
	clean, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	broken := string(clean) + "\n\nfunc calibUndefined() { calibNoSuchSymbol() }\n"

	cases := []struct {
		name string
		text string
	}{
		{name: "clean", text: string(clean)},
		{name: "error", text: broken},
	}

	cacheDir := t.TempDir()
	// Prime the cache so every sample is a warm-server measurement; diagnostics
	// latency on a still-indexing server measures the load, not the pass.
	timeOneLoad(t, gopls, root, cacheDir)

	for _, tc := range cases {
		firsts := make([]time.Duration, 0, calibRuns)
		lasts := make([]time.Duration, 0, calibRuns)
		multi := 0

		for i := 0; i < calibRuns; i++ {
			pubs := watchPublications(t, gopls, root, cacheDir, target, tc.text)
			if len(pubs) == 0 {
				t.Fatalf("%s run %d: no publication for %s within %s", tc.name, i+1, target, calibDiagCeiling)
			}
			if len(pubs) > 1 {
				multi++
			}
			firsts = append(firsts, pubs[0].offset)
			lasts = append(lasts, pubs[len(pubs)-1].offset)

			parts := make([]string, len(pubs))
			for j, p := range pubs {
				parts[j] = fmt.Sprintf("+%s(%d items, version %q)", p.offset.Round(time.Millisecond), p.items, p.version)
			}
			t.Logf("%s run %d: %d publication(s): %s", tc.name, i+1, len(pubs), strings.Join(parts, " "))
		}

		report(t, tc.name+" first publication", firsts)
		report(t, tc.name+" last publication", lasts)
		t.Logf("%s: runs with more than one publication: %d/%d (observation window %s)", tc.name, multi, calibRuns, calibDiagCeiling)
	}
}

// watchPublications opens target with the given text on a ready server and
// records every publishDiagnostics for that file until calibDiagCeiling
// elapses after didOpen. It never stops at the first arrival, so a second pass
// is observed rather than assumed.
func watchPublications(t *testing.T, gopls, root, cacheDir, target, text string) []publication {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sess, _ := loadUntilReady(t, ctx, gopls, root, cacheDir)
	defer closeSession(t, sess)

	go drainProgress(ctx, sess)
	drainDiagnostics(sess.Diagnostics())

	start := time.Now()
	if err := sess.DidOpen(ctx, target, "go", text, 1); err != nil {
		t.Fatalf("did open: %v", err)
	}
	defer func() {
		if err := sess.DidClose(context.Background(), target); err != nil {
			t.Errorf("did close: %v", err)
		}
	}()

	var pubs []publication
	ceiling := time.After(calibDiagCeiling)

	for {
		select {
		case pub := <-sess.Diagnostics():
			if pub.File != target {
				continue
			}
			version := "none"
			if pub.Version != nil {
				version = fmt.Sprintf("%d", *pub.Version)
			}
			pubs = append(pubs, publication{offset: time.Since(start), items: len(pub.Items), version: version})
		case <-sess.Exited():
			t.Fatalf("gopls exited while collecting diagnostics")
		case <-ceiling:
			return pubs
		}
	}
}

// calibDefinition times the full open → definition → close cycle that
// navigate.go performs, on a warm and ready server.
func calibDefinition(t *testing.T, gopls, root string) {
	target := filepath.Join(root, "internal", "lsp", "transport.go")
	line, col := findSymbol(t, target, "newSession(ctx, stream", "newSession")

	cacheDir := t.TempDir()
	timeOneLoad(t, gopls, root, cacheDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sess, _ := loadUntilReady(t, ctx, gopls, root, cacheDir)
	defer closeSession(t, sess)

	go drainProgress(ctx, sess)
	go drainDiagnosticsLoop(ctx, sess)

	cycles := make([]time.Duration, 0, calibRuns)
	bare := make([]time.Duration, 0, calibRuns)

	for i := 0; i < calibRuns; i++ {
		var reqElapsed time.Duration
		cycleStart := time.Now()

		err := withDocument(ctx, sess, target, func() error {
			reqStart := time.Now()
			locs, err := sess.Definition(ctx, target, line, col)
			reqElapsed = time.Since(reqStart)
			if err != nil {
				return err
			}
			if len(locs) == 0 {
				return fmt.Errorf("no definition found at %s:%d:%d", target, line, col)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("definition run %d: %v", i+1, err)
		}

		cycle := time.Since(cycleStart)
		cycles = append(cycles, cycle)
		bare = append(bare, reqElapsed)
		t.Logf("definition run %d: request %s, open+request+close %s", i+1, reqElapsed, cycle)
	}

	report(t, "warm definition request", bare)
	report(t, "warm open+definition+close cycle", cycles)
}

// calibNoProgressGrace measures D16: what a server that emits no progress at
// all costs the readiness gate. gopls is initialized without the
// window.workDoneProgress capability, which is a real server doing real
// indexing while staying silent. The subtest asserts the silence, then times
// how long a definition issued immediately after `initialized` takes to return
// a correct answer — that is the work ReadyGracePeriod lets through.
func calibNoProgressGrace(t *testing.T, gopls, root string) {
	target := filepath.Join(root, "internal", "lsp", "transport.go")
	line, col := findSymbol(t, target, "newSession(ctx, stream", "newSession")

	answers := make([]time.Duration, 0, calibRuns)

	for i := 0; i < calibRuns; i++ {
		cacheDir := t.TempDir()

		func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sess, err := spawnWithoutProgress(ctx, gopls, root, calibEnv(cacheDir))
			if err != nil {
				t.Fatalf("spawn gopls without progress capability: %v", err)
			}
			defer closeSession(t, sess)

			go drainDiagnosticsLoop(ctx, sess)

			// The watcher gets its own context: cancelling ctx would tear the
			// server down before the session is closed gracefully.
			watchCtx, stopWatch := context.WithCancel(context.Background())
			progress := 0
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					select {
					case <-sess.Progress():
						progress++
					case <-watchCtx.Done():
						return
					}
				}
			}()

			start := time.Now()
			err = withDocument(ctx, sess, target, func() error {
				locs, defErr := sess.Definition(ctx, target, line, col)
				if defErr != nil {
					return defErr
				}
				if len(locs) == 0 {
					return fmt.Errorf("no definition found at %s:%d:%d", target, line, col)
				}
				return nil
			})
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("no-progress run %d: %v", i+1, err)
			}

			stopWatch()
			<-done
			if progress != 0 {
				t.Fatalf("no-progress run %d: server emitted %d progress events; the measurement is not a no-progress measurement", i+1, progress)
			}

			answers = append(answers, elapsed)
			t.Logf("no-progress run %d: first correct answer after %s (0 progress events)", i+1, elapsed)
		}()
	}

	report(t, "no-progress first correct answer (cold)", answers)
}

// spawnWithoutProgress mirrors newTransport but initializes without the
// window.workDoneProgress client capability, so the server never reports
// workspace-load progress. Only the initialize parameters differ; the process,
// stream, handler, and session types are the production ones.
func spawnWithoutProgress(ctx context.Context, command, root string, env []string) (*impl, error) {
	cmd := exec.CommandContext(ctx, command)
	cmd.Env = env
	cmd.Stderr = io.Discard
	setProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start server: %w", err)
	}
	proc := newExecProcess(cmd)

	diagnosticsChan := make(chan PublishedDiagnostics, notifyBuffer)
	progressChan := make(chan ProgressEvent, notifyBuffer)
	handler := &clientHandler{diagnostics: diagnosticsChan, progress: progressChan}

	stream := jsonrpc2.NewStream(&readWriteCloser{r: stdout, w: stdin})
	connCtx, connCancel := context.WithCancel(context.WithoutCancel(ctx))
	_, conn, server := protocol.NewClient(connCtx, handler, stream)

	rootURI := uri.File(root)
	initParams := protocol.InitializeParams{
		RootPath: protocol.NewNullable(root), //nolint:staticcheck // sent for older servers
		RootURI:  &rootURI,                   //nolint:staticcheck // sent for older servers
		WorkspaceFoldersInitializeParams: protocol.WorkspaceFoldersInitializeParams{
			WorkspaceFolders: protocol.NewNullable([]protocol.WorkspaceFolder{{URI: rootURI, Name: "root"}}),
		},
		Capabilities: protocol.ClientCapabilities{
			TextDocument: &protocol.TextDocumentClientCapabilities{
				Definition:         &protocol.DefinitionClientCapabilities{LinkSupport: ptrBool(true)},
				References:         &protocol.ReferenceClientCapabilities{},
				PublishDiagnostics: &protocol.PublishDiagnosticsClientCapabilities{},
			},
		},
	}

	result, err := server.Initialize(ctx, &initParams)
	if err != nil {
		connCancel()
		_ = conn.Close() // best-effort: the session is being abandoned
		proc.Kill()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if err := server.Initialized(ctx, &protocol.InitializedParams{}); err != nil {
		connCancel()
		_ = conn.Close() // best-effort: the session is being abandoned
		proc.Kill()
		return nil, fmt.Errorf("initialized notification: %w", err)
	}

	return &impl{
		conn:            conn,
		connCancel:      connCancel,
		server:          server,
		proc:            proc,
		capabilities:    result.Capabilities,
		shutdownTimeout: defaultShutdownTimeout,
		diagnosticsChan: diagnosticsChan,
		progressChan:    progressChan,
	}, nil
}

// calibEnv mirrors Manager.spawnServer's environment: HOME, XDG_CACHE_HOME and
// GOCACHE all rooted at the per-workspace cache dir, so a fresh dir reproduces
// a first-ever run. PATH, GOPATH and GOMODCACHE come from the real environment,
// as they do for a spawned server, which inherits steiner's environment; without
// GOMODCACHE gopls would re-download the module graph and the measurement would
// be of the network.
func calibEnv(cacheDir string) []string {
	env := []string{
		"HOME=" + cacheDir,
		"XDG_CACHE_HOME=" + cacheDir,
		"GOCACHE=" + filepath.Join(cacheDir, "go"),
	}
	for _, name := range []string{"PATH", "GOPATH", "GOMODCACHE", "GOPROXY", "GOFLAGS"} {
		if v := os.Getenv(name); v != "" {
			env = append(env, name+"="+v)
			continue
		}
		if v := goEnv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// resolveGopls finds gopls, skipping the calibration when it is absent.
func resolveGopls(t *testing.T) string {
	t.Helper()

	if p := os.Getenv("STEINER_LSP_CALIB_GOPLS"); p != "" {
		return p
	}
	if p, err := exec.LookPath("gopls"); err == nil {
		return p
	}
	for _, dir := range []string{goEnv("GOBIN"), filepath.Join(goEnv("GOPATH"), "bin")} {
		if dir == "" || dir == "bin" {
			continue
		}
		p := filepath.Join(dir, "gopls")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}

	t.Skip("gopls not found: set STEINER_LSP_CALIB_GOPLS, or install gopls on PATH, $GOBIN or $GOPATH/bin")
	return ""
}

func goplsVersion(t *testing.T, gopls string) string {
	t.Helper()
	out, err := exec.Command(gopls, "version").Output()
	if err != nil {
		t.Fatalf("gopls version: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func goEnv(name string) string {
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// repoRoot returns the module root, which is the workspace the servers load.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

// findSymbol returns the 1-based line and column of symbol within the first
// line containing anchor, so the measurement does not hard-code line numbers.
func findSymbol(t *testing.T, file, anchor, symbol string) (int, int) {
	t.Helper()

	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	for i, line := range strings.Split(string(content), "\n") {
		idx := strings.Index(line, anchor)
		if idx < 0 {
			continue
		}
		return i + 1, idx + strings.Index(anchor, symbol) + 1
	}
	t.Fatalf("anchor %q not found in %s", anchor, file)
	return 0, 0
}

func drainProgress(ctx context.Context, sess session) {
	for {
		select {
		case <-sess.Progress():
		case <-ctx.Done():
			return
		}
	}
}

func drainDiagnosticsLoop(ctx context.Context, sess session) {
	for {
		select {
		case <-sess.Diagnostics():
		case <-ctx.Done():
			return
		}
	}
}

func closeSession(t *testing.T, sess *impl) {
	t.Helper()
	if err := sess.Close(context.Background()); err != nil {
		t.Errorf("close session: %v", err)
	}
}

// report logs the distribution of a sample set. With calibRuns samples the
// maximum is the practical p95, so it is labelled as the value to size against.
func report(t *testing.T, label string, samples []time.Duration) {
	t.Helper()

	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration
	for _, s := range sorted {
		total += s
	}

	t.Logf("RESULT %-42s n=%d min=%s median=%s max=%s mean=%s",
		label, len(sorted),
		sorted[0].Round(time.Millisecond),
		sorted[len(sorted)/2].Round(time.Millisecond),
		sorted[len(sorted)-1].Round(time.Millisecond),
		(total / time.Duration(len(sorted))).Round(time.Millisecond),
	)
}
