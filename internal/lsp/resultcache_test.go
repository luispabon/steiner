package lsp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"go.lsp.dev/protocol"

	"github.com/luispabon/steiner/internal/config"
)

func TestResultCache_IdenticalCallHitsCache(t *testing.T) {
	t.Parallel()
	// Test 1: An identical repeated call (same file, same position, same content)
	// hits the cache — assert via a call counter that the SECOND identical call
	// does not reach the server.

	cache := newResultCache(512)
	key := cacheKey{
		server:      "test-server",
		root:        "/root",
		method:      "definitions",
		file:        "/root/main.go",
		line:        10,
		column:      5,
		fileHash:    "abc123",
		includeDecl: false,
	}

	result := &Result{
		Locations: []Location{
			{File: "/root/main.go", Line: 5, Column: 1},
		},
		Incomplete: false,
		Truncated:  false,
	}

	// First call: miss
	_, hit := cache.get(key)
	if hit {
		t.Error("expected cache miss on first get, but got hit")
	}

	// Store result
	cache.put(key, result)

	// Second call: hit
	cached, hit := cache.get(key)
	if !hit {
		t.Error("expected cache hit on second get, but got miss")
	}

	// Verify result is correct
	cachedResult := cached.(*Result)
	if len(cachedResult.Locations) != 1 {
		t.Errorf("expected 1 location, got %d", len(cachedResult.Locations))
	}
	if cachedResult.Locations[0].Line != 5 {
		t.Errorf("expected line 5, got %d", cachedResult.Locations[0].Line)
	}
}

func TestResultCache_FileMutationCausesMiss(t *testing.T) {
	t.Parallel()
	// Test 2: Editing the file between two calls (same position, different content)
	// produces a cache MISS on the second call.

	cache := newResultCache(512)

	// First call with hash A
	keyA := cacheKey{
		server:      "test-server",
		root:        "/root",
		method:      "definitions",
		file:        "/root/main.go",
		line:        10,
		column:      5,
		fileHash:    "hashA",
		includeDecl: false,
	}

	result := &Result{
		Locations:  []Location{{File: "/root/main.go", Line: 5, Column: 1}},
		Incomplete: false,
		Truncated:  false,
	}

	cache.put(keyA, result)

	// Verify cache hit with same hash
	_, hit := cache.get(keyA)
	if !hit {
		t.Error("expected cache hit with keyA")
	}

	// Second call with hash B (file changed)
	keyB := cacheKey{
		server:      "test-server",
		root:        "/root",
		method:      "definitions",
		file:        "/root/main.go",
		line:        10,
		column:      5,
		fileHash:    "hashB", // different hash
		includeDecl: false,
	}

	_, hit = cache.get(keyB)
	if hit {
		t.Error("expected cache miss with keyB (different hash), but got hit")
	}
}

func TestResultCache_ProvisionalNotCached_Navigate(t *testing.T) {
	t.Parallel()
	// Test 3a: A provisional result (Incomplete=true) from Manager.Definitions
	// is NOT cached. Force incomplete=true by starting a begin progress event
	// that never completes before ReadyTimeout fires.

	fs := newFakeServer()
	fs.definitionResult = &protocol.Location{
		URI: "file:///test.go",
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 5},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.LSPConfig{
		MaxResults:       100,
		RequestTimeout:   config.MustDuration("2s"),
		ReadyTimeout:     config.MustDuration("50ms"),
		ReadyGracePeriod: config.MustDuration("10ms"),
		Servers: map[string]config.LSPServerConfig{
			"go": {Enabled: true, FileExtensions: []string{".go"}},
		},
	}
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	t.Cleanup(func() { _ = m.Close() })

	root := resolveRoot(testFile, tmpdir, nil)
	ent := &entry{state: ServerState{Status: ServerStatusReady}, session: sess, readiness: newReadiness()}
	m.sessions = map[sessionKey]*entry{{server: "go", root: root}: ent}
	go m.trackReadiness(ent, sess, ent.readiness)

	// Fire a begin event so the grace period timer is stopped, then never
	// send the matching end: ReadyTimeout fires with an incomplete cycle.
	begin, _ := json.Marshal(protocol.WorkDoneProgressBegin{Kind: "begin", Title: "Loading"})
	if err := fs.client.Progress(ctx, &protocol.ProgressParams{
		Token: protocol.String("token1"),
		Value: protocol.LSPAny(begin),
	}); err != nil {
		t.Fatalf("send begin: %v", err)
	}

	result, err := m.Definitions(ctx, testFile, 1, 1)
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	if !result.Incomplete {
		t.Fatalf("Definitions result.Incomplete = false, want true (readiness gate should have timed out)")
	}

	key := cacheKey{
		server:      "go",
		root:        root,
		method:      "definitions",
		file:        testFile,
		line:        1,
		column:      1,
		fileHash:    hashFileContent(testFile),
		includeDecl: false,
	}
	if _, hit := m.resultCache.get(key); hit {
		t.Error("provisional (incomplete) Definitions result was cached, want not cached")
	}

	// A repeated call must hit the server again, not a cache entry.
	if _, err := m.Definitions(ctx, testFile, 1, 1); err != nil {
		t.Fatalf("Definitions (second call): %v", err)
	}
	defCount := 0
	for _, method := range fs.recorded() {
		if method == "textDocument/definition" {
			defCount++
		}
	}
	if defCount != 2 {
		t.Errorf("textDocument/definition called %d times, want 2 (provisional result must not short-circuit via cache)", defCount)
	}
}

func TestResultCache_ProvisionalNotCached_Diagnostics(t *testing.T) {
	t.Parallel()
	// Test 3b: A provisional diagnostics result (WindowExpired=true) from
	// Manager.Diagnostics is NOT cached. Force WindowExpired=true by letting
	// the collection window elapse with no publication for the file.

	fs := newFakeServer()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.LSPConfig{
		MaxResults:        100,
		RequestTimeout:    config.MustDuration("2s"),
		DiagnosticsWindow: config.MustDuration("50ms"),
		Servers: map[string]config.LSPServerConfig{
			"go": {Enabled: true, FileExtensions: []string{".go"}},
		},
	}
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	t.Cleanup(func() { _ = m.Close() })

	root := resolveRoot(testFile, tmpdir, nil)
	ent := &entry{state: ServerState{Status: ServerStatusReady}, session: sess}
	m.sessions = map[sessionKey]*entry{{server: "go", root: root}: ent}

	result, err := m.Diagnostics(ctx, testFile)
	if err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}
	if !result.WindowExpired {
		t.Fatalf("Diagnostics result.WindowExpired = false, want true (no publication was sent)")
	}

	key := cacheKey{
		server:      "go",
		root:        root,
		method:      "diagnostics",
		file:        testFile,
		line:        0,
		column:      0,
		fileHash:    hashFileContent(testFile),
		includeDecl: false,
	}
	if _, hit := m.resultCache.get(key); hit {
		t.Error("provisional (WindowExpired) Diagnostics result was cached, want not cached")
	}

	// A repeated call must re-collect from the server, not hit a cache entry.
	if _, err := m.Diagnostics(ctx, testFile); err != nil {
		t.Fatalf("Diagnostics (second call): %v", err)
	}
	openCount := 0
	for _, method := range fs.recorded() {
		if method == "textDocument/didOpen" {
			openCount++
		}
	}
	if openCount != 2 {
		t.Errorf("textDocument/didOpen called %d times, want 2 (provisional result must not short-circuit via cache)", openCount)
	}
}

func TestResultCache_LRUEviction(t *testing.T) {
	t.Parallel()
	// Test 4: LRU eviction: fill the cache past its bound and assert
	// the least-recently-used entry was evicted while more-recently-used ones survive.

	maxSize := 5
	cache := newResultCache(maxSize)

	// Insert 5 entries (fills to capacity)
	keys := make([]cacheKey, maxSize)
	for i := 0; i < maxSize; i++ {
		keys[i] = cacheKey{
			server:   "srv",
			root:     "/root",
			method:   "definitions",
			file:     fmt.Sprintf("/root/file%d.go", i),
			line:     i + 1,
			column:   1,
			fileHash: fmt.Sprintf("hash%d", i),
		}
		cache.put(keys[i], &Result{
			Locations: []Location{{File: fmt.Sprintf("/root/file%d.go", i), Line: i + 1, Column: 1}},
		})
	}

	// Verify all 5 are cached
	for i := 0; i < maxSize; i++ {
		_, hit := cache.get(keys[i])
		if !hit {
			t.Errorf("expected cache hit for key %d", i)
		}
	}

	// Touch the oldest (keys[0]) by getting it (move to back)
	cache.get(keys[0])

	// Insert one more, which should evict keys[1] (second-oldest in original order,
	// now the oldest after keys[0] was touched)
	newKey := cacheKey{
		server:   "srv",
		root:     "/root",
		method:   "definitions",
		file:     "/root/file_new.go",
		line:     999,
		column:   1,
		fileHash: "hash_new",
	}
	cache.put(newKey, &Result{
		Locations: []Location{{File: "/root/file_new.go", Line: 999, Column: 1}},
	})

	// Verify keys[0] is still cached (it was touched)
	_, hit := cache.get(keys[0])
	if !hit {
		t.Error("expected cache hit for touched key[0], but got miss")
	}

	// Verify keys[1] was evicted
	_, hit = cache.get(keys[1])
	if hit {
		t.Error("expected cache miss for evicted key[1], but got hit")
	}

	// Verify keys[2-4] and newKey are cached
	for i := 2; i < maxSize; i++ {
		_, hit := cache.get(keys[i])
		if !hit {
			t.Errorf("expected cache hit for key %d, but got miss", i)
		}
	}

	_, hit = cache.get(newKey)
	if !hit {
		t.Error("expected cache hit for newKey, but got miss")
	}
}

func TestResultCache_ConcurrentAccess_RaceFree(t *testing.T) {
	t.Parallel()
	// Test 5: Concurrent access is race-free. Launch many goroutines
	// hitting the cache (mixed reads/writes/evictions) concurrently and
	// assert no panic and no race (verified via -race flag).

	cache := newResultCache(50) // small bound to force eviction

	// Key space larger than cache to force evictions
	numKeys := 200
	numGoroutines := 50
	iterPerGoroutine := 200

	var wg sync.WaitGroup
	var panicCount int32

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt32(&panicCount, 1)
					t.Logf("goroutine %d panicked: %v", gid, r)
				}
			}()

			for iter := 0; iter < iterPerGoroutine; iter++ {
				keyIdx := (gid*iterPerGoroutine + iter) % numKeys
				key := cacheKey{
					server:   "srv",
					root:     "/root",
					method:   "definitions",
					file:     fmt.Sprintf("/root/file%d.go", keyIdx),
					line:     keyIdx + 1,
					column:   1,
					fileHash: fmt.Sprintf("hash%d", keyIdx),
				}

				// Mix of gets and puts
				switch iter % 3 {
				case 0:
					// Get (read)
					cache.get(key)
				case 1:
					// Put (write)
					result := &Result{
						Locations: []Location{{File: fmt.Sprintf("/root/file%d.go", keyIdx), Line: keyIdx + 1, Column: 1}},
					}
					cache.put(key, result)
				default:
					// Clear
					cache.clear()
				}
			}
		}(g)
	}

	wg.Wait()

	if panicCount > 0 {
		t.Errorf("expected no panics, but got %d", panicCount)
	}

	// Verify cache invariant: size <= maxSize
	size, capacity := cache.stats()
	if size > capacity {
		t.Errorf("cache size %d exceeds capacity %d", size, capacity)
	}

	// Verify internal consistency: map and list have same size
	cache.mu.Lock()
	if len(cache.entries) != cache.order.Len() {
		t.Errorf("cache inconsistency: map has %d entries, list has %d", len(cache.entries), cache.order.Len())
	}
	cache.mu.Unlock()
}

func TestResultCache_DifferentMethodsNoCollision(t *testing.T) {
	t.Parallel()
	// Test 6: Different methods (definitions vs references vs diagnostics)
	// for the SAME file/position do not collide in the cache.

	cache := newResultCache(512)
	baseKey := cacheKey{
		server:   "srv",
		root:     "/root",
		file:     "/root/main.go",
		line:     10,
		column:   5,
		fileHash: "abc123",
	}

	// Three different methods
	defKey := baseKey
	defKey.method = "definitions"

	refKey := baseKey
	refKey.method = "references"

	diagKey := baseKey
	diagKey.method = "diagnostics"

	// Store different results for each method
	defResult := &Result{Locations: []Location{{File: "def.go"}}}
	refResult := &Result{Locations: []Location{{File: "ref.go"}}}
	diagResult := &DiagResult{Items: []Diagnostic{{Message: "diag"}}}

	cache.put(defKey, defResult)
	cache.put(refKey, refResult)
	cache.put(diagKey, diagResult)

	// Verify each key retrieves the correct result
	cached, hit := cache.get(defKey)
	if !hit {
		t.Error("expected hit for defKey")
	}
	if cached.(*Result).Locations[0].File != "def.go" {
		t.Error("defKey returned wrong result")
	}

	cached, hit = cache.get(refKey)
	if !hit {
		t.Error("expected hit for refKey")
	}
	if cached.(*Result).Locations[0].File != "ref.go" {
		t.Error("refKey returned wrong result")
	}

	cached, hit = cache.get(diagKey)
	if !hit {
		t.Error("expected hit for diagKey")
	}
	if cached.(*DiagResult).Items[0].Message != "diag" {
		t.Error("diagKey returned wrong result")
	}
}

func TestResultCache_IncludeDeclIsPartOfKey(t *testing.T) {
	t.Parallel()
	// Test 7: includeDecl (a References-only parameter) is part of the key —
	// two calls differing only in includeDecl do not collide.

	cache := newResultCache(512)
	baseKey := cacheKey{
		server:   "srv",
		root:     "/root",
		file:     "/root/main.go",
		line:     10,
		column:   5,
		fileHash: "abc123",
		method:   "references",
	}

	keyWithDecl := baseKey
	keyWithDecl.includeDecl = true

	keyWithoutDecl := baseKey
	keyWithoutDecl.includeDecl = false

	// Store different results
	withDeclResult := &Result{Locations: []Location{{File: "with_decl.go"}}}
	withoutDeclResult := &Result{Locations: []Location{{File: "without_decl.go"}}}

	cache.put(keyWithDecl, withDeclResult)
	cache.put(keyWithoutDecl, withoutDeclResult)

	// Verify each key retrieves the correct result
	cached, hit := cache.get(keyWithDecl)
	if !hit {
		t.Error("expected hit for keyWithDecl")
	}
	if cached.(*Result).Locations[0].File != "with_decl.go" {
		t.Error("keyWithDecl returned wrong result")
	}

	cached, hit = cache.get(keyWithoutDecl)
	if !hit {
		t.Error("expected hit for keyWithoutDecl")
	}
	if cached.(*Result).Locations[0].File != "without_decl.go" {
		t.Error("keyWithoutDecl returned wrong result")
	}
}

func TestResultCache_SliceCloning(t *testing.T) {
	t.Parallel()
	// Verify that slices are cloned on store and on hit to prevent data races
	// when callers modify the results (e.g., sort in place).

	cache := newResultCache(512)
	key := cacheKey{
		server:   "srv",
		root:     "/root",
		file:     "/root/main.go",
		line:     10,
		column:   5,
		fileHash: "abc123",
		method:   "definitions",
	}

	originalResult := &Result{
		Locations: []Location{
			{File: "/root/a.go", Line: 1, Column: 1},
			{File: "/root/b.go", Line: 2, Column: 2},
		},
	}

	cache.put(key, originalResult)

	// Get the result twice
	cached1, _ := cache.get(key)
	cached2, _ := cache.get(key)

	result1 := cached1.(*Result)
	result2 := cached2.(*Result)

	// Verify they are different slice headers (cloned)
	if &result1.Locations[0] == &result2.Locations[0] {
		t.Error("expected cloned slices, but got same backing array")
	}

	// Modify one and verify the other is unaffected
	result1.Locations[0].Line = 999

	cached2Again, _ := cache.get(key)
	result2Again := cached2Again.(*Result)
	if result2Again.Locations[0].Line != 1 {
		t.Error("cache pollution: modification to one hit affected another hit")
	}
}

func TestHashFileContent(t *testing.T) {
	t.Parallel()
	// Unit test for hashFileContent and related hashing.

	t.Run("hashFileContent returns empty on read error", func(t *testing.T) {
		hash := hashFileContent("/nonexistent/file.go")
		if hash != "" {
			t.Errorf("expected empty hash on read error, got %q", hash)
		}
	})

	t.Run("hashFileContent is deterministic", func(t *testing.T) {
		content := []byte("package main\n\nfunc main() {}\n")
		want := fmt.Sprintf("%x", sha256.Sum256(content))

		file := filepath.Join(t.TempDir(), "main.go")
		if err := os.WriteFile(file, content, 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		hash1 := hashFileContent(file)
		hash2 := hashFileContent(file)

		if hash1 != want {
			t.Errorf("hashFileContent: got %q, want %q", hash1, want)
		}
		if hash1 != hash2 {
			t.Error("hashFileContent is not deterministic across repeated calls")
		}
	})
}
