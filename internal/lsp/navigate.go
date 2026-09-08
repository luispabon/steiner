package lsp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
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

// HoverResult is the result of a hover query.
type HoverResult struct {
	Content    HoverContent
	Incomplete bool
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

// Hover returns hover information at the given position in a file.
func (m *Manager) Hover(ctx context.Context, file string, line, col int) (HoverResult, error) {
	if line < 1 || col < 1 {
		return HoverResult{}, fmt.Errorf("invalid position: line %d col %d", line, col)
	}

	file, err := absWorkspacePath(m.workspace, file)
	if err != nil {
		return HoverResult{}, err
	}

	// Get the entry so we can lock the cycle.
	ent, sess, err := m.entryFor(ctx, file)
	if err != nil {
		return HoverResult{}, err
	}

	// Await readiness (but don't hold the lock during the wait).
	incomplete, err := m.awaitReady(ctx, ent)
	if err != nil {
		return HoverResult{}, err
	}

	// Now acquire the cycle lock and hold it for open → request → close.
	ent.cycleMu.Lock()
	defer ent.cycleMu.Unlock()

	// Define the request with a timeout scoped to the request only.
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(m.cfg.RequestTimeout.Duration()))
	defer cancel()

	var hoverContent HoverContent
	err = withDocument(ctx, sess, file, func() error {
		// Issue the request within the request timeout.
		content, err := sess.Hover(reqCtx, file, line, col)
		if err != nil {
			return fmt.Errorf("hover request: %w", err)
		}
		hoverContent = content
		return nil
	})
	if err != nil {
		return HoverResult{}, err
	}

	return HoverResult{
		Content:    hoverContent,
		Incomplete: incomplete,
	}, nil
}

// SymbolResult is the result of a symbol query (workspace or document).
type SymbolResult struct {
	Symbols    []SymbolInfo
	Incomplete bool
	Truncated  bool
	// Total is the number of symbols before truncation at MaxResults.
	Total int
}

// DocumentSymbols returns the outline of symbols in file, optionally filtered
// by a case-insensitive substring match on symbol name.
func (m *Manager) DocumentSymbols(ctx context.Context, file, query string) (SymbolResult, error) {
	file, err := absWorkspacePath(m.workspace, file)
	if err != nil {
		return SymbolResult{}, err
	}

	documentCacheKey, haveCacheKey := m.documentSymbolCacheKey(file)

	var all []SymbolInfo
	var incomplete bool
	if haveCacheKey {
		if cached, hit := m.resultCache.get(documentCacheKey); hit {
			cachedResult := cached.(*SymbolResult)
			all = cachedResult.Symbols
			incomplete = cachedResult.Incomplete
		}
	}

	if all == nil {
		all, incomplete, err = m.fetchDocumentSymbols(ctx, file)
		if err != nil {
			return SymbolResult{}, err
		}
		if !incomplete && haveCacheKey {
			m.resultCache.put(documentCacheKey, &SymbolResult{Symbols: all, Total: len(all)})
		}
	}

	filtered := all
	if query != "" {
		filtered = filterSymbolsByName(all, query)
	}

	total := len(filtered)
	truncated := false
	if len(filtered) > m.cfg.MaxResults {
		filtered = filtered[:m.cfg.MaxResults]
		truncated = true
	}

	return SymbolResult{
		Symbols:    filtered,
		Incomplete: incomplete,
		Truncated:  truncated,
		Total:      total,
	}, nil
}

// documentSymbolCacheKey builds the cache key for a document_symbol lookup on
// file. It returns ok=false (best-effort: skip the cache) when the session key
// or file hash cannot be resolved. Unlike Definitions/References, this key
// deliberately excludes query: the cached value always holds the unfiltered,
// unsorted-by-query symbol list, and filtering is applied fresh on every call
// so two different queries against the same file never collide on one entry.
func (m *Manager) documentSymbolCacheKey(file string) (cacheKey, bool) {
	sk, ok := m.resolveSessionKey(file)
	if !ok {
		return cacheKey{}, false
	}
	fileHash := hashFileContent(file)
	if fileHash == "" {
		return cacheKey{}, false
	}
	return cacheKey{
		server:   sk.server,
		root:     sk.root,
		method:   "document_symbol",
		file:     file,
		fileHash: fileHash,
	}, true
}

// fetchDocumentSymbols resolves a session for file, awaits readiness, and
// issues a textDocument/documentSymbol request, returning the sorted result.
func (m *Manager) fetchDocumentSymbols(ctx context.Context, file string) ([]SymbolInfo, bool, error) {
	ent, sess, err := m.entryFor(ctx, file)
	if err != nil {
		return nil, false, err
	}

	incomplete, err := m.awaitReady(ctx, ent)
	if err != nil {
		return nil, false, err
	}

	ent.cycleMu.Lock()
	defer ent.cycleMu.Unlock()

	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(m.cfg.RequestTimeout.Duration()))
	defer cancel()

	var all []SymbolInfo
	err = withDocument(ctx, sess, file, func() error {
		symbols, err := sess.DocumentSymbol(reqCtx, file)
		if err != nil {
			return fmt.Errorf("document symbol request: %w", err)
		}
		all = symbols
		return nil
	})
	if err != nil {
		return nil, false, err
	}

	sortSymbols(all)
	return all, incomplete, nil
}

// WorkspaceSymbols searches for symbols matching query across every enabled,
// configured language server, fanning the request out concurrently. A server
// that is slow, errors, or isn't spawnable is skipped rather than failing the
// whole call; the merged result is marked Incomplete if any server was
// skipped or itself reported incomplete. Results are not cached: a
// project-wide query has no single file to hash for cache invalidation.
func (m *Manager) WorkspaceSymbols(ctx context.Context, query string) (SymbolResult, error) {
	names := m.enabledServerNames()
	if len(names) == 0 {
		return SymbolResult{}, errNoServer
	}

	var (
		mu         sync.Mutex
		all        []SymbolInfo
		incomplete bool
		wg         sync.WaitGroup
	)

	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			symbols, serverIncomplete, ok := m.workspaceSymbolsForServer(ctx, name, query)

			mu.Lock()
			defer mu.Unlock()
			if !ok {
				incomplete = true
				return
			}
			all = append(all, symbols...)
			if serverIncomplete {
				incomplete = true
			}
		}(name)
	}
	wg.Wait()

	sortSymbols(all)
	total := len(all)
	truncated := false
	if len(all) > m.cfg.MaxResults {
		all = all[:m.cfg.MaxResults]
		truncated = true
	}

	return SymbolResult{
		Symbols:    all,
		Incomplete: incomplete,
		Truncated:  truncated,
		Total:      total,
	}, nil
}

// workspaceSymbolsForServer resolves a session for the named server (reusing
// an existing one at its existing root if present, else spawning fresh at a
// workspace-resolved root), awaits readiness, and issues a workspace/symbol
// request. ok is false if the server could not be resolved, spawned, or made
// ready; that server's contribution is then skipped entirely by the caller.
func (m *Manager) workspaceSymbolsForServer(ctx context.Context, name, query string) (symbols []SymbolInfo, incomplete bool, ok bool) {
	m.mu.Lock()
	srv := m.cfg.Servers[name]
	m.mu.Unlock()

	key, hasExisting := m.existingSessionKeyForServer(name)
	if !hasExisting {
		key = sessionKey{server: name, root: resolveWorkspaceRoot(m.workspace, srv.RootMarkers)}
	}

	ent, sess, err := m.entryForKey(ctx, name, srv, key)
	if err != nil {
		return nil, false, false
	}

	incomplete, err = m.awaitReady(ctx, ent)
	if err != nil {
		return nil, false, false
	}

	ent.cycleMu.Lock()
	defer ent.cycleMu.Unlock()

	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(m.cfg.RequestTimeout.Duration()))
	defer cancel()

	result, err := sess.WorkspaceSymbol(reqCtx, query)
	if err != nil {
		return nil, false, false
	}

	return result, incomplete, true
}

// filterSymbolsByName returns the subset of symbols whose Name contains query
// as a case-insensitive substring.
func filterSymbolsByName(symbols []SymbolInfo, query string) []SymbolInfo {
	lowered := strings.ToLower(query)
	filtered := make([]SymbolInfo, 0, len(symbols))
	for _, sym := range symbols {
		if strings.Contains(strings.ToLower(sym.Name), lowered) {
			filtered = append(filtered, sym)
		}
	}
	return filtered
}

// sortSymbols sorts symbols deterministically by (File, Line, Column).
func sortSymbols(symbols []SymbolInfo) {
	sort.SliceStable(symbols, func(i, j int) bool {
		if symbols[i].File != symbols[j].File {
			return symbols[i].File < symbols[j].File
		}
		if symbols[i].Line != symbols[j].Line {
			return symbols[i].Line < symbols[j].Line
		}
		return symbols[i].Column < symbols[j].Column
	})
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
