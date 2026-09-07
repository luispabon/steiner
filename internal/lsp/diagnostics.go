package lsp

import (
	"context"
	"sort"
	"time"
)

// DiagResult is the result of a diagnostics query.
type DiagResult struct {
	Items         []Diagnostic
	Truncated     bool   // set when results capped at MaxResults
	WindowExpired bool   // set when collection window closed (timer fired vs ctx cancelled)
	Note          string // e.g., readiness status
}

// Diagnostics collects server-pushed diagnostics for a file within the configured window.
// It returns immediately if the server is unavailable or exits during collection.
// An empty result is never an error — a silent/slow server yields zero items with
// WindowExpired=true and a nil error. Errors are reserved for real failures
// (server exit, context cancellation, network issues).
func (m *Manager) Diagnostics(ctx context.Context, file string) (DiagResult, error) {
	// Build cache key and check for cached result.
	// If key resolution fails (best-effort), skip cache and proceed.
	if sessionKey, ok := m.resolveSessionKey(file); ok {
		fileHash := hashFileContent(file)
		if fileHash != "" {
			key := cacheKey{
				server:      sessionKey.server,
				root:        sessionKey.root,
				method:      "diagnostics",
				file:        file,
				line:        0,
				column:      0,
				fileHash:    fileHash,
				includeDecl: false,
			}
			if cached, hit := m.resultCache.get(key); hit {
				return *cached.(*DiagResult), nil
			}
		}
	}

	// Get the entry so we can lock the cycle.
	ent, sess, err := m.entryFor(ctx, file)
	if err != nil {
		return DiagResult{}, err
	}

	// Now acquire the cycle lock and hold it for drain → open → collect → close.
	ent.cycleMu.Lock()
	defer ent.cycleMu.Unlock()

	result, err := m.collectDiagnostics(ctx, ent, sess, file)

	// Store in cache only if the result is not provisional.
	// WindowExpired=true means collection window closed (timer fired) — provisional.
	// ctx.Err() != nil means caller's context was cancelled — also provisional.
	// Both indicate the result is incomplete or interrupted.
	if !result.WindowExpired && ctx.Err() == nil {
		if sessionKey, ok := m.resolveSessionKey(file); ok {
			fileHash := hashFileContent(file)
			if fileHash != "" {
				key := cacheKey{
					server:      sessionKey.server,
					root:        sessionKey.root,
					method:      "diagnostics",
					file:        file,
					line:        0,
					column:      0,
					fileHash:    fileHash,
					includeDecl: false,
				}
				m.resultCache.put(key, &result)
			}
		}
	}

	return result, err
}

// collectDiagnostics holds entry.cycleMu and implements the drain-then-collect cycle.
// The caller must already hold ent.cycleMu.
func (m *Manager) collectDiagnostics(ctx context.Context, _ *entry, sess session, file string) (DiagResult, error) {
	// Drain stale notifications from a prior call so they don't leak into this result.
	// We're holding cycleMu, so no other caller can be draining concurrently.
	drainDiagnostics(sess.Diagnostics())

	var result DiagResult

	// Open the document and collect diagnostics within the window.
	err := withDocument(ctx, sess, file, func() error {
		// Start collection timer inside withDocument (while file is open).
		// This is separate from ctx's timeout.
		windowTimer := time.NewTimer(time.Duration(m.cfg.DiagnosticsWindow.Duration()))
		defer windowTimer.Stop()

		diags := make(map[string][]Diagnostic) // by file URI

		for {
			select {
			case <-ctx.Done():
				// Context cancelled; return collected-so-far, WindowExpired=false.
				result = flattenAndSortDiagnostics(diags, m.cfg.MaxResults, false, file)
				return nil
			case <-windowTimer.C:
				// Window expired. Check if we received any publication for the requested file.
				// WindowExpired=true only if the server never published for this file.
				// If a publication was received (even with zero items), WindowExpired=false.
				_, receivedPublication := diags[file]
				result = flattenAndSortDiagnostics(diags, m.cfg.MaxResults, !receivedPublication, file)
				return nil
			case <-sess.Exited():
				// Server died; return error.
				return errServerExited
			case pub := <-sess.Diagnostics():
				// Filter: keep only diagnostics for the requested file.
				// Later publications for the same file replace earlier ones (replace-not-append).
				if pub.File == file {
					diags[pub.File] = pub.Items
				}
				// Else: discard publications for other files without putting them back.
			}
		}
	})

	return result, err
}

// drainDiagnostics drains all pending notifications from the diagnostics channel
// without blocking. Used to clear stale items queued from a prior call before
// starting a new collection window.
func drainDiagnostics(diagsChan <-chan PublishedDiagnostics) {
	for {
		select {
		case <-diagsChan:
			// Consume and discard.
		default:
			// Channel is empty; stop draining.
			return
		}
	}
}

// flattenAndSortDiagnostics converts the per-file map to a sorted slice,
// filtered to the requested file only, capped at MaxResults.
func flattenAndSortDiagnostics(diags map[string][]Diagnostic, maxResults int, windowExpired bool, requestedFile string) DiagResult {
	items := diags[requestedFile]

	// Sort by (Line, Column, Severity).
	sortDiagnostics(items)

	truncated := false
	if len(items) > maxResults {
		items = items[:maxResults]
		truncated = true
	}

	return DiagResult{
		Items:         items,
		Truncated:     truncated,
		WindowExpired: windowExpired,
	}
}

// sortDiagnostics sorts diagnostics deterministically by (Line, Column, Severity).
// Severity is ranked: error < warning < information < hint < unknown.
func sortDiagnostics(diags []Diagnostic) {
	sort.SliceStable(diags, func(i, j int) bool {
		if diags[i].Line != diags[j].Line {
			return diags[i].Line < diags[j].Line
		}
		if diags[i].Column != diags[j].Column {
			return diags[i].Column < diags[j].Column
		}
		// Sort by severity rank.
		rankI := severityRank(diags[i].Severity)
		rankJ := severityRank(diags[j].Severity)
		return rankI < rankJ
	})
}

// severityRank returns a sort order for severity strings.
// Lower ranks sort first: error (0) < warning (1) < information (2) < hint (3) < unknown (4).
func severityRank(severity string) int {
	switch severity {
	case "error":
		return 0
	case "warning":
		return 1
	case "information":
		return 2
	case "hint":
		return 3
	default:
		return 4 // unknown or any other value
	}
}
