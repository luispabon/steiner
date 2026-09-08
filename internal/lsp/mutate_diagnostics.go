package lsp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxMutateDiagnosticsFiles bounds how many touched files PostMutateDiagnostics
// will query. This is a deliberate latency budget, not an arbitrary number: in
// the worst case each candidate file serializes on its session's cycleMu and
// waits out a full DiagnosticsWindow (2s by default, see
// internal/config/defaults.go), so the total added latency is bounded by
// maxMutateDiagnosticsFiles * DiagnosticsWindow. Raising this constant raises
// that worst case linearly; do not "just bump it" without re-deriving the
// latency budget.
const maxMutateDiagnosticsFiles = 3

// maxMutateDiagnosticsLines caps the number of rendered diagnostic lines
// (across all collected files) in PostMutateDiagnostics's output.
const maxMutateDiagnosticsLines = 40

// ReadyForFile reports whether a language server session for file's extension
// is already running and ready to serve requests. It never spawns a server:
// it is a fast, side-effect-free check meant to gate opportunistic diagnostics
// collection after a mutation, where a cold start would be too slow to be
// worth waiting for. It returns false if no server is configured for the
// file's extension, or if a session exists but has not reached
// ServerStatusReady (including a session that failed, stopped, or is still
// starting).
func (m *Manager) ReadyForFile(file string) bool {
	file, err := absWorkspacePath(m.workspace, file)
	if err != nil {
		return false
	}

	key, ok := m.resolveSessionKey(file)
	if !ok {
		return false
	}

	m.mu.Lock()
	ent, exists := m.sessions[key]
	m.mu.Unlock()

	if !exists {
		return false
	}

	ent.mu.Lock()
	status := ent.state.Status
	ent.mu.Unlock()

	return status == ServerStatusReady
}

// PostMutateDiagnostics returns a bounded, best-effort diagnostics report for
// the given files, formatted as plain text suitable for appending to a tool
// result. It is intended to run immediately after a successful file mutation:
// for each candidate file with an already-ready language server (per
// ReadyForFile), it queries m.Diagnostics and renders any published
// diagnostics. It never spawns a server — files without a running, ready
// session are silently skipped — and it never fails: on any per-file error it
// simply omits that file's diagnostics rather than returning an error.
//
// files is assumed to already be deduped and ordered by the caller; at most
// maxMutateDiagnosticsFiles of them (in the given order) are queried, and any
// remainder is silently skipped. This is a documented limitation, not a bug:
// it bounds the worst-case added latency (see maxMutateDiagnosticsFiles).
//
// PostMutateDiagnostics derives its own bounded context from ctx via
// context.WithoutCancel plus a fixed timeout, rather than using ctx directly,
// for two reasons. First, Manager.Diagnostics (via collectDiagnostics) uses
// context.WithoutCancel internally for its own DidClose cleanup once
// collection ends, so deriving a further query from an already-cancelled
// caller context could race with that cleanup. Second, the diagnostics
// query's own timeout is intentionally independent of whatever deadline the
// mutate tool call itself was given: it must never be silently truncated by,
// nor allowed to extend past, the caller's own deadline.
//
// ReadyForFile is a point-in-time check: a session can transition away from
// ServerStatusReady (e.g. the idle reaper stopping it) between the check and
// the subsequent Diagnostics call, in which case Diagnostics may itself
// trigger a respawn. This is an accepted best-effort race, not something this
// function guards against.
//
// Returns "" if m is nil, files is empty, ctx is already done, no candidate
// file has a ready server, or collection yields zero diagnostics across all
// candidate files — never a "no diagnostics found" message.
func PostMutateDiagnostics(ctx context.Context, m *Manager, files []string) string {
	if m == nil || len(files) == 0 || ctx.Err() != nil {
		return ""
	}

	candidates := mutateDiagnosticsCandidates(m, files)
	if len(candidates) == 0 {
		return ""
	}

	collectCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		time.Duration(m.cfg.DiagnosticsWindow.Duration())+500*time.Millisecond,
	)
	defer cancel()

	results := collectMutateDiagnostics(collectCtx, m, candidates)
	if len(results) == 0 {
		return ""
	}

	return renderMutateDiagnostics(results)
}

// mutateDiagnosticsCandidate pairs a normalised, absolute file path with the
// session key its language server resolves to.
type mutateDiagnosticsCandidate struct {
	file string
	key  sessionKey
}

// mutateDiagnosticsCandidates takes the first maxMutateDiagnosticsFiles of
// files (in the given order), normalises each to an absolute path, and keeps
// only those with an already-ready language server.
func mutateDiagnosticsCandidates(m *Manager, files []string) []mutateDiagnosticsCandidate {
	capped := files
	if len(capped) > maxMutateDiagnosticsFiles {
		capped = capped[:maxMutateDiagnosticsFiles]
	}

	var candidates []mutateDiagnosticsCandidate
	for _, file := range capped {
		abs, err := absWorkspacePath(m.workspace, file)
		if err != nil || !m.ReadyForFile(abs) {
			continue
		}
		key, ok := m.resolveSessionKey(abs)
		if !ok {
			continue
		}
		candidates = append(candidates, mutateDiagnosticsCandidate{file: abs, key: key})
	}
	return candidates
}

// mutateFileDiags holds one file's collected diagnostics for rendering.
type mutateFileDiags struct {
	displayPath string
	items       []Diagnostic
}

// collectMutateDiagnostics queries m.Diagnostics for each candidate, grouped
// by session key so files sharing a session run sequentially (they'd
// otherwise just queue on entry.cycleMu) while distinct sessions run
// concurrently. Files with an error or zero items are omitted.
func collectMutateDiagnostics(ctx context.Context, m *Manager, candidates []mutateDiagnosticsCandidate) []mutateFileDiags {
	groups := make(map[sessionKey][]string)
	var groupOrder []sessionKey
	for _, c := range candidates {
		if _, seen := groups[c.key]; !seen {
			groupOrder = append(groupOrder, c.key)
		}
		groups[c.key] = append(groups[c.key], c.file)
	}

	var (
		mu      sync.Mutex
		results []mutateFileDiags
		wg      sync.WaitGroup
	)

	for _, key := range groupOrder {
		filesInGroup := groups[key]
		wg.Add(1)
		go func(filesInGroup []string) {
			defer wg.Done()
			for _, file := range filesInGroup {
				result, err := m.Diagnostics(ctx, file)
				if err != nil || len(result.Items) == 0 {
					continue
				}
				mu.Lock()
				results = append(results, mutateFileDiags{
					displayPath: makeRelative(m.workspace, file),
					items:       result.Items,
				})
				mu.Unlock()
			}
		}(filesInGroup)
	}
	wg.Wait()

	return results
}

// renderMutateDiagnostics sorts results by display path and renders them as a
// bounded text section, capping the total number of lines at
// maxMutateDiagnosticsLines and appending an omission trailer if truncated.
func renderMutateDiagnostics(results []mutateFileDiags) string {
	sort.Slice(results, func(i, j int) bool {
		return results[i].displayPath < results[j].displayPath
	})

	var lines []string
	total := 0
	for _, r := range results {
		for _, diag := range r.items {
			total++
			if len(lines) >= maxMutateDiagnosticsLines {
				continue
			}
			line := fmt.Sprintf("%s:%d:%d %s: %s", r.displayPath, diag.Line, diag.Column, diag.Severity, diag.Message)
			if diag.Code != "" {
				line += " [" + diag.Code + "]"
			}
			lines = append(lines, line)
		}
	}

	var b strings.Builder
	b.WriteString("Diagnostics after mutation:\n")
	b.WriteString(strings.Join(lines, "\n"))
	if total > len(lines) {
		fmt.Fprintf(&b, "\n... %d more diagnostics omitted", total-len(lines))
	}

	return b.String()
}
