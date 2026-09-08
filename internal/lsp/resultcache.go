package lsp

import (
	"container/list"
	"crypto/sha256"
	"fmt"
	"os"
	"sync"
)

const resultCacheMaxEntries = 512

// resultCache is a bounded LRU cache for LSP operation results, keyed on
// content-addressed file hashes so a mutated file cannot serve a stale answer.
// The cache is guarded by its own mutex, separate from the Manager's locks,
// since cache operations are orthogonal to session lifecycle.
type resultCache struct {
	mu      sync.Mutex
	maxSize int
	entries map[cacheKey]*list.Element
	order   *list.List // LRU ordering: oldest at Front, newest at Back
}

// cacheKey uniquely identifies a (server, root, method, file, position, fileHash) tuple.
type cacheKey struct {
	server      string // server name
	root        string // workspace root
	method      string // "definitions", "references", "diagnostics", or "document_symbol"
	file        string // absolute file path
	line        int    // 1-indexed line number
	column      int    // 1-indexed column number
	fileHash    string // SHA-256 hex of file's current content
	includeDecl bool   // for references; false for definitions/diagnostics
}

// cacheEntry holds a cached result: a Result, DiagResult, or SymbolResult.
type cacheEntry struct {
	key cacheKey
	val any // *Result or *DiagResult
}

// newResultCache creates a bounded LRU cache with the given maximum size.
func newResultCache(maxSize int) *resultCache {
	return &resultCache{
		maxSize: maxSize,
		entries: make(map[cacheKey]*list.Element),
		order:   list.New(),
	}
}

// hashFileContent returns the SHA-256 hash of the file content at the given path.
// If the file cannot be read, it returns the empty string and the cache lookup
// will treat it as a miss (best-effort: the error is silently ignored).
// We hash raw bytes, not normalized content, to catch all semantic changes
// including those diagnostics might flag (e.g., trailing whitespace).
func hashFileContent(path string) string {
	data, err := readFileContent(path)
	if err != nil {
		return "" // best-effort: skip cache on read error
	}
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash)
}

// readFileContent reads the full content of a file at path.
// It is extracted as a separate function for testability.
func readFileContent(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return data, nil
}

// get returns the cached result for the given key, if present and valid.
// On hit, the entry is moved to the back of the LRU list.
// On miss, (nil, false) is returned.
func (c *resultCache) get(key cacheKey) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	// Move to back (most recently used).
	c.order.MoveToBack(elem)
	entry := elem.Value.(cacheEntry)
	// Clone the result before returning to prevent concurrent modifications.
	return cloneResult(entry.val), true
}

// put stores the result under the given key, evicting the LRU entry if necessary.
// It clones the result to prevent the caller from modifying the cached copy.
func (c *resultCache) put(key cacheKey, val any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Clone the result before storing.
	val = cloneResult(val)

	elem, ok := c.entries[key]
	if ok {
		// Update existing entry and move to back.
		elem.Value = cacheEntry{key: key, val: val}
		c.order.MoveToBack(elem)
		return
	}

	// New entry: add to back.
	elem = c.order.PushBack(cacheEntry{key: key, val: val})
	c.entries[key] = elem

	// Evict LRU if over capacity.
	if len(c.entries) > c.maxSize {
		oldest := c.order.Front()
		c.order.Remove(oldest)
		oldKey := oldest.Value.(cacheEntry).key
		delete(c.entries, oldKey)
	}
}

// clear removes all entries from the cache.
func (c *resultCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[cacheKey]*list.Element)
	c.order.Init()
}

// cloneResult creates a deep copy of a cached result to prevent concurrent
// modification of slice backing arrays. It handles Result, DiagResult, and SymbolResult.
func cloneResult(val any) any {
	switch v := val.(type) {
	case *SymbolResult:
		if v == nil {
			return nil
		}
		symbols := make([]SymbolInfo, len(v.Symbols))
		copy(symbols, v.Symbols)
		return &SymbolResult{
			Symbols:    symbols,
			Incomplete: v.Incomplete,
			Truncated:  v.Truncated,
			Total:      v.Total,
		}
	case *Result:
		if v == nil {
			return nil
		}
		locs := make([]Location, len(v.Locations))
		copy(locs, v.Locations)
		return &Result{
			Locations:  locs,
			Incomplete: v.Incomplete,
			Truncated:  v.Truncated,
			Total:      v.Total,
		}
	case *DiagResult:
		if v == nil {
			return nil
		}
		diags := make([]Diagnostic, len(v.Items))
		copy(diags, v.Items)
		return &DiagResult{
			Items:         diags,
			Truncated:     v.Truncated,
			WindowExpired: v.WindowExpired,
			Total:         v.Total,
		}
	default:
		return val // unknown type, return as-is
	}
}

// stats returns the current cache statistics for testing/debugging.
func (c *resultCache) stats() (size, capacity int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.maxSize
}
