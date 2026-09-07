package lsp

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Result is the result of a navigation query (definitions or references).
type Result struct {
	Locations  []Location
	Incomplete bool
	Truncated  bool
	// Total is the number of locations before truncation at MaxResults.
	Total int
}

// Definitions returns all definitions for a symbol at the given position in a file.
func (m *Manager) Definitions(ctx context.Context, file string, line, col int) (Result, error) {
	if line < 1 || col < 1 {
		return Result{}, fmt.Errorf("invalid position: line %d col %d", line, col)
	}

	file, err := absWorkspacePath(m.workspace, file)
	if err != nil {
		return Result{}, err
	}

	// Build cache key and check for cached result.
	// If key resolution fails (best-effort), skip cache and proceed.
	if sessionKey, ok := m.resolveSessionKey(file); ok {
		fileHash := hashFileContent(file)
		if fileHash != "" {
			key := cacheKey{
				server:      sessionKey.server,
				root:        sessionKey.root,
				method:      "definitions",
				file:        file,
				line:        line,
				column:      col,
				fileHash:    fileHash,
				includeDecl: false,
			}
			if cached, hit := m.resultCache.get(key); hit {
				return *cached.(*Result), nil
			}
		}
	}

	// Get the entry so we can lock the cycle.
	ent, sess, err := m.entryFor(ctx, file)
	if err != nil {
		return Result{}, err
	}

	// Await readiness (but don't hold the lock during the wait).
	incomplete, err := m.awaitReady(ctx, ent)
	if err != nil {
		return Result{}, err
	}

	// Now acquire the cycle lock and hold it for open → request → close.
	ent.cycleMu.Lock()
	defer ent.cycleMu.Unlock()

	// Define the request with a timeout scoped to the request only.
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(m.cfg.RequestTimeout.Duration()))
	defer cancel()

	var locations []Location
	err = withDocument(ctx, sess, file, func() error {
		// Issue the request within the request timeout.
		locs, err := sess.Definition(reqCtx, file, line, col)
		if err != nil {
			return fmt.Errorf("definition request: %w", err)
		}
		locations = locs
		return nil
	})
	if err != nil {
		return Result{}, err
	}

	// Sort deterministically and cap at MaxResults.
	sortLocations(locations)
	total := len(locations)
	truncated := false
	if len(locations) > m.cfg.MaxResults {
		locations = locations[:m.cfg.MaxResults]
		truncated = true
	}

	result := Result{
		Locations:  locations,
		Incomplete: incomplete,
		Truncated:  truncated,
		Total:      total,
	}

	// Store in cache only if the result is not provisional.
	// Incomplete=true means readiness gate timed out, so the result is provisional.
	if !incomplete {
		if sessionKey, ok := m.resolveSessionKey(file); ok {
			fileHash := hashFileContent(file)
			if fileHash != "" {
				key := cacheKey{
					server:      sessionKey.server,
					root:        sessionKey.root,
					method:      "definitions",
					file:        file,
					line:        line,
					column:      col,
					fileHash:    fileHash,
					includeDecl: false,
				}
				m.resultCache.put(key, &result)
			}
		}
	}

	return result, nil
}

// References returns all references to a symbol at the given position in a file.
func (m *Manager) References(ctx context.Context, file string, line, col int, includeDecl bool) (Result, error) {
	if line < 1 || col < 1 {
		return Result{}, fmt.Errorf("invalid position: line %d col %d", line, col)
	}

	file, err := absWorkspacePath(m.workspace, file)
	if err != nil {
		return Result{}, err
	}

	// Build cache key and check for cached result.
	// If key resolution fails (best-effort), skip cache and proceed.
	if sessionKey, ok := m.resolveSessionKey(file); ok {
		fileHash := hashFileContent(file)
		if fileHash != "" {
			key := cacheKey{
				server:      sessionKey.server,
				root:        sessionKey.root,
				method:      "references",
				file:        file,
				line:        line,
				column:      col,
				fileHash:    fileHash,
				includeDecl: includeDecl,
			}
			if cached, hit := m.resultCache.get(key); hit {
				return *cached.(*Result), nil
			}
		}
	}

	// Get the entry so we can lock the cycle.
	ent, sess, err := m.entryFor(ctx, file)
	if err != nil {
		return Result{}, err
	}

	// Await readiness (but don't hold the lock during the wait).
	incomplete, err := m.awaitReady(ctx, ent)
	if err != nil {
		return Result{}, err
	}

	// Now acquire the cycle lock and hold it for open → request → close.
	ent.cycleMu.Lock()
	defer ent.cycleMu.Unlock()

	// Define the request with a timeout scoped to the request only.
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(m.cfg.RequestTimeout.Duration()))
	defer cancel()

	var locations []Location
	err = withDocument(ctx, sess, file, func() error {
		// Issue the request within the request timeout.
		locs, err := sess.References(reqCtx, file, line, col, includeDecl)
		if err != nil {
			return fmt.Errorf("references request: %w", err)
		}
		locations = locs
		return nil
	})
	if err != nil {
		return Result{}, err
	}

	// Sort deterministically and cap at MaxResults.
	sortLocations(locations)
	total := len(locations)
	truncated := false
	if len(locations) > m.cfg.MaxResults {
		locations = locations[:m.cfg.MaxResults]
		truncated = true
	}

	result := Result{
		Locations:  locations,
		Incomplete: incomplete,
		Truncated:  truncated,
		Total:      total,
	}

	// Store in cache only if the result is not provisional.
	// Incomplete=true means readiness gate timed out, so the result is provisional.
	if !incomplete {
		if sessionKey, ok := m.resolveSessionKey(file); ok {
			fileHash := hashFileContent(file)
			if fileHash != "" {
				key := cacheKey{
					server:      sessionKey.server,
					root:        sessionKey.root,
					method:      "references",
					file:        file,
					line:        line,
					column:      col,
					fileHash:    fileHash,
					includeDecl: includeDecl,
				}
				m.resultCache.put(key, &result)
			}
		}
	}

	return result, nil
}

// sortLocations sorts locations deterministically by (File, Line, Column).
func sortLocations(locs []Location) {
	sort.SliceStable(locs, func(i, j int) bool {
		if locs[i].File != locs[j].File {
			return locs[i].File < locs[j].File
		}
		if locs[i].Line != locs[j].Line {
			return locs[i].Line < locs[j].Line
		}
		return locs[i].Column < locs[j].Column
	})
}
