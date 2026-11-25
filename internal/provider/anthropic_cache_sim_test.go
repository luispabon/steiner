package provider

// Test-only Anthropic prompt-cache simulator. It models the documented cache
// rules closely enough to compare request layouts: entries exist only at
// cache_control breakpoints, a breakpoint reads back at most 20 positions, a run
// of consecutive tool_use or tool_result blocks counts as one position, and a
// request never reads entries it writes itself. TTLs are not modelled because
// the synthetic sequences run well inside the 5m window.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

const (
	// anthropicCacheLookbackPositions is the provider's documented backward
	// lookback for a cache breakpoint, counted in content positions with the
	// breakpoint's own position first.
	anthropicCacheLookbackPositions = 20
	// anthropicCacheBytesPerToken is the rough bytes-per-token ratio used to
	// turn canonical JSON sizes into token counts for the cost proxy.
	anthropicCacheBytesPerToken = 4
)

// simPosition is one cacheable position of a request: a system block, a tool
// definition, a content block, or a coalesced run of tool_use/tool_result
// blocks.
type simPosition struct {
	key         string // sha256 of the canonical bytes from the request start through this position
	prefixBytes int    // canonical byte length of that prefix
	breakpoint  bool   // some block in the position carries cache_control
	boundary    bool   // the position holds the CacheBoundary block
}

// simResult is the simulated cache accounting for one request.
type simResult struct {
	totalTokens    int
	cachedTokens   int
	writtenTokens  int
	uncachedTokens int
	cachedBytes    int
	boundaryBytes  int // prefix bytes at the segment boundary position, 0 when absent
	breakpoints    int
}

// simUnit is one canonical cacheable unit before tool runs are coalesced.
type simUnit struct {
	canonical   []byte
	breakpoint  bool
	boundary    bool
	coalesceRun string
}

// simulateAnthropicCache runs reqs through the wire serializer and returns the
// simulated cache accounting for each request. Entries written by request i are
// only visible to requests after i.
func simulateAnthropicCache(t *testing.T, reqs []ChatRequest) []simResult {
	t.Helper()
	written := make(map[string]struct{})
	results := make([]simResult, 0, len(reqs))
	for _, req := range reqs {
		wire := anthropicRequestWire(req, "claude-sim", false)
		positions := simPositions(t, wire)
		var res simResult
		for _, pos := range positions {
			if pos.breakpoint {
				res.breakpoints++
			}
			if pos.boundary {
				res.boundaryBytes = pos.prefixBytes
			}
		}
		if len(positions) > 0 {
			res.totalTokens = positions[len(positions)-1].prefixBytes / anthropicCacheBytesPerToken
		}
		cachedBytes, _ := simRead(positions, written)
		writtenBytes, keys := simWrites(positions, cachedBytes)
		res.cachedBytes = cachedBytes
		res.cachedTokens = cachedBytes / anthropicCacheBytesPerToken
		res.writtenTokens = writtenBytes / anthropicCacheBytesPerToken
		res.uncachedTokens = res.totalTokens - res.cachedTokens - res.writtenTokens
		for _, key := range keys {
			written[key] = struct{}{}
		}
		results = append(results, res)
	}
	return results
}

// simCostProxy is the relative input cost of a simulated run: uncached input
// at 1.0, cache writes at 1.25 and cache reads at 0.1, the Anthropic ratios
// used by the live bench.
func simCostProxy(results []simResult) float64 {
	total := 0.0
	for _, res := range results {
		total += float64(res.uncachedTokens) + 1.25*float64(res.writtenTokens) + 0.1*float64(res.cachedTokens)
	}
	return total
}

// simRead returns the byte length of the longest cached prefix reachable from
// the request's breakpoints and each breakpoint's hit position (-1 for a miss).
func simRead(positions []simPosition, written map[string]struct{}) (int, []int) {
	cachedBytes := 0
	hits := make([]int, 0, len(positions))
	for p, pos := range positions {
		if !pos.breakpoint {
			continue
		}
		hit := -1
		oldest := p - anthropicCacheLookbackPositions + 1
		if oldest < 0 {
			oldest = 0
		}
		for q := p; q >= oldest; q-- {
			if _, ok := written[positions[q].key]; !ok {
				continue
			}
			hit = q
			if positions[q].prefixBytes > cachedBytes {
				cachedBytes = positions[q].prefixBytes
			}
			break
		}
		hits = append(hits, hit)
	}
	return cachedBytes, hits
}

// simWrites returns the byte length of the entries a request creates beyond
// its longest cached prefix and the keys of those entries, in position order.
// Only positions carrying cache_control write.
func simWrites(positions []simPosition, cachedBytes int) (int, []string) {
	writtenBytes := 0
	high := cachedBytes
	keys := make([]string, 0, len(positions))
	for _, pos := range positions {
		if !pos.breakpoint {
			continue
		}
		keys = append(keys, pos.key)
		if pos.prefixBytes > high {
			writtenBytes += pos.prefixBytes - high
			high = pos.prefixBytes
		}
	}
	return writtenBytes, keys
}

// simPositions flattens a wire request into cacheable positions. Canonical
// bytes exclude cache_control so an entry written at a breakpoint matches the
// same prefix in later requests that place other breakpoints.
func simPositions(t *testing.T, wire anthropicRequest) []simPosition {
	t.Helper()
	units := simUnits(t, wire)
	hasher := sha256.New()
	positions := make([]simPosition, 0, len(units))
	lastRun := ""
	prefixBytes := 0
	for _, unit := range units {
		_, _ = hasher.Write(unit.canonical) // sha256 never returns a write error
		key := hex.EncodeToString(hasher.Sum(nil))
		prefixBytes += len(unit.canonical)
		if unit.coalesceRun != "" && unit.coalesceRun == lastRun {
			last := &positions[len(positions)-1]
			last.key = key
			last.prefixBytes = prefixBytes
			last.breakpoint = last.breakpoint || unit.breakpoint
			last.boundary = last.boundary || unit.boundary
		} else {
			positions = append(positions, simPosition{
				key:         key,
				prefixBytes: prefixBytes,
				breakpoint:  unit.breakpoint,
				boundary:    unit.boundary,
			})
		}
		lastRun = unit.coalesceRun
	}
	return positions
}

func simUnits(t *testing.T, wire anthropicRequest) []simUnit {
	t.Helper()
	units := make([]simUnit, 0, len(wire.System)+len(wire.Tools)+len(wire.Messages))
	add := func(value any, breakpoint, boundary bool, run string) {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal cache unit: %v", err)
		}
		units = append(units, simUnit{canonical: data, breakpoint: breakpoint, boundary: boundary, coalesceRun: run})
	}
	for _, block := range wire.System {
		add(simCanonicalBlock(block), block.CacheControl != nil, false, "")
	}
	for _, tool := range wire.Tools {
		breakpoint := tool.CacheControl != nil
		tool.CacheControl = nil
		add(tool, breakpoint, false, "")
	}
	for i, msg := range wire.Messages {
		for j, block := range msg.Content {
			boundary := wire.hasSegmentBoundary && i == wire.segmentBoundaryMsg && j == wire.segmentBoundaryBlock
			add(simCanonicalBlock(block), block.CacheControl != nil, boundary, simCoalesceRun(block))
		}
	}
	return units
}

func simCanonicalBlock(block anthropicContentBlock) anthropicContentBlock {
	block.CacheControl = nil
	return block
}

func simCoalesceRun(block anthropicContentBlock) string {
	switch block.Type {
	case "tool_use", "tool_result":
		return block.Type
	default:
		return ""
	}
}

// Synthetic sequences: a static system and tool set, six compactions, and
// turns with tool calls and results. The rewrite layout keeps one summary
// message that is replaced at each compaction; the append layout appends one
// segment per compaction, marks the last segment, and follows it with a tail.
// Both layouts carry the same summarised content so the comparison isolates
// cache behaviour.
const (
	simStaticTokens    = 2000
	simSegmentTokens   = 1500
	simCompactionCount = 6
)

type simLayout int

const (
	simRewriteLayout simLayout = iota
	simAppendLayout
)

// simTurn holds one user task, its tool loop and the assistant reply.
type simTurn struct {
	task        Message
	toolCalls   Message
	toolResults []Message
	reply       Message
}

func simMakeTurn(tag string, gen int) simTurn {
	callA := ToolCall{ID: fmt.Sprintf("call_%s_%d_a", tag, gen), Name: "read_file", Arguments: map[string]any{"path": fmt.Sprintf("file_%s_%d.txt", tag, gen)}}
	callB := ToolCall{ID: fmt.Sprintf("call_%s_%d_b", tag, gen), Name: "grep", Arguments: map[string]any{"pattern": fmt.Sprintf("needle_%s_%d", tag, gen)}}
	return simTurn{
		task:      Message{Role: MessageRoleUser, Content: fmt.Sprintf("Task %s %d. %s", tag, gen, simFiller(fmt.Sprintf("task %s %d", tag, gen), 40))},
		toolCalls: Message{Role: MessageRoleAssistant, Content: "Inspecting files.", ToolCalls: []ToolCall{callA, callB}},
		toolResults: []Message{
			{Role: MessageRoleTool, ToolCallID: callA.ID, Content: simFiller(fmt.Sprintf("result %s %d a", tag, gen), 60)},
			{Role: MessageRoleTool, ToolCallID: callB.ID, Content: simFiller(fmt.Sprintf("result %s %d b", tag, gen), 60)},
		},
		reply: Message{Role: MessageRoleAssistant, Content: fmt.Sprintf("Task %s %d complete. %s", tag, gen, simFiller(fmt.Sprintf("reply %s %d", tag, gen), 50))},
	}
}

func simPrefix(layout simLayout, bodies []string) []Message {
	if layout == simRewriteLayout {
		return []Message{{Role: MessageRoleUser, Content: "Summary of the conversation so far.\n\n" + strings.Join(bodies, "\n\n")}}
	}
	prefix := make([]Message, 0, len(bodies)+1)
	for i, body := range bodies {
		prefix = append(prefix, Message{
			Role:    MessageRoleUser,
			Content: fmt.Sprintf("Segment %d. Later segments override earlier conflicting state.\n\n%s", i+1, body),
		})
	}
	prefix[len(prefix)-1].CacheBoundary = true
	return append(prefix, Message{Role: MessageRoleUser, Content: "History note. " + simFiller("history note", 30)})
}

// simSequence builds the requests of one layout. Each generation runs two
// turns and ends with a compaction that summarises the first turn into a new
// segment (append) or rewrites the whole summary (rewrite); the second turn is
// retained. The rewrite summary is an ordinary user message, so the real
// assignCacheBreakpoints decides whether a rolling breakpoint lands on it,
// exactly as it does today.
func simSequence(layout simLayout) []ChatRequest {
	var reqs []ChatRequest
	var prefix []Message
	var retained []Message
	var bodies []string
	system := Message{Role: MessageRoleSystem, Content: simFiller("static system", simStaticTokens)}
	for gen := 1; gen <= simCompactionCount+1; gen++ {
		first := simMakeTurn("first", gen)
		second := simMakeTurn("second", gen)
		base := make([]Message, 0, 1+len(prefix)+len(retained)+1)
		base = append(base, system)
		base = append(base, prefix...)
		base = append(base, retained...)
		base = append(base, first.task)
		reqs = append(reqs, simRequest(base))
		base = append(base, first.toolCalls)
		base = append(base, first.toolResults...)
		reqs = append(reqs, simRequest(base))
		base = append(base, first.reply, second.task)
		reqs = append(reqs, simRequest(base))
		base = append(base, second.toolCalls)
		base = append(base, second.toolResults...)
		reqs = append(reqs, simRequest(base))
		retained = []Message{second.task, second.toolCalls, second.toolResults[0], second.toolResults[1], second.reply}
		if gen <= simCompactionCount {
			bodies = append(bodies, simFiller(fmt.Sprintf("segment %d fact", gen), simSegmentTokens))
			prefix = simPrefix(layout, bodies)
		}
	}
	return reqs
}

func simRequest(messages []Message) ChatRequest {
	cloned := make([]Message, len(messages))
	copy(cloned, messages)
	return ChatRequest{Model: "claude-sim", Messages: cloned, Tools: simTools()}
}

func simTools() []ToolSpec {
	return []ToolSpec{
		{
			Type: "function",
			Function: ToolFunctionSpec{
				Name:        "read_file",
				Description: simFiller("read tool", 120),
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
			},
		},
		{
			Type: "function",
			Function: ToolFunctionSpec{
				Name:        "grep",
				Description: simFiller("grep tool", 120),
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{"pattern": map[string]any{"type": "string"}}},
			},
		},
	}
}

// simFiller returns roughly tokens*4 bytes of deterministic filler text.
func simFiller(tag string, tokens int) string {
	unit := tag + " detail. "
	return strings.Repeat(unit, tokens*anthropicCacheBytesPerToken/len(unit)+1)
}

func TestCacheSimWritesOnlyAtBreakpoints(t *testing.T) {
	positions := []simPosition{
		{key: "a", prefixBytes: 100},
		{key: "b", prefixBytes: 250, breakpoint: true},
		{key: "c", prefixBytes: 400},
		{key: "d", prefixBytes: 600, breakpoint: true},
		{key: "e", prefixBytes: 1000},
	}
	writtenBytes, keys := simWrites(positions, 0)
	if want := 600; writtenBytes != want {
		t.Fatalf("simWrites() wrote %d bytes, want %d: only breakpoint prefixes are written", writtenBytes, want)
	}
	if want := []string{"b", "d"}; !slices.Equal(keys, want) {
		t.Fatalf("simWrites() keys %v, want %v", keys, want)
	}
}

func TestCacheSimReadLookbackWindow(t *testing.T) {
	tests := []struct {
		name       string
		breakpoint int
		wantCached int
		wantHit    int
	}{
		{name: "breakpoint 19 positions after the entry hits", breakpoint: 19, wantCached: 100, wantHit: 0},
		{name: "breakpoint 20 positions after the entry is out of reach", breakpoint: 20, wantCached: 0, wantHit: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			positions := make([]simPosition, 21)
			for i := range positions {
				positions[i] = simPosition{key: fmt.Sprintf("p%d", i), prefixBytes: (i + 1) * 100}
			}
			positions[tt.breakpoint].breakpoint = true
			written := map[string]struct{}{"p0": {}}
			cached, hits := simRead(positions, written)
			if cached != tt.wantCached {
				t.Fatalf("simRead() cached %d bytes, want %d", cached, tt.wantCached)
			}
			if len(hits) != 1 || hits[0] != tt.wantHit {
				t.Fatalf("simRead() hits %v, want [%d]", hits, tt.wantHit)
			}
		})
	}
}

func TestCacheSimCoalescesToolRuns(t *testing.T) {
	request := ChatRequest{Model: "claude-sim", Tools: simTools(), Messages: []Message{
		{Role: MessageRoleSystem, Content: "sys"},
		{Role: MessageRoleUser, Content: "task"},
		{Role: MessageRoleAssistant, Content: "calling", ToolCalls: []ToolCall{{ID: "a", Name: "read_file"}, {ID: "b", Name: "grep"}}},
		{Role: MessageRoleTool, ToolCallID: "a", Content: "result a"},
		{Role: MessageRoleTool, ToolCallID: "b", Content: "result b"},
	}}
	positions := simPositions(t, anthropicRequestWire(request, "claude-sim", false))

	// system + two tool definitions + user text + assistant text + one tool_use
	// run + one tool_result run. Coalescing keeps the two runs at one position
	// each instead of four positions in total.
	if want := 1 + len(simTools()) + 1 + 1 + 1 + 1; len(positions) != want {
		t.Fatalf("positions = %d, want %d", len(positions), want)
	}
	last := positions[len(positions)-1]
	if !last.breakpoint {
		t.Fatal("final coalesced tool-result run carries no breakpoint")
	}
}

func TestCacheSimDoesNotReadOwnWrites(t *testing.T) {
	request := ChatRequest{Model: "claude-sim", Tools: simTools(), Messages: []Message{
		{Role: MessageRoleSystem, Content: simFiller("system", 500)},
		{Role: MessageRoleUser, Content: "task"},
		{Role: MessageRoleAssistant, Content: "calling", ToolCalls: []ToolCall{{ID: "a", Name: "read_file"}, {ID: "b", Name: "grep"}}},
		{Role: MessageRoleTool, ToolCallID: "a", Content: "result a"},
		{Role: MessageRoleTool, ToolCallID: "b", Content: "result b"},
		{Role: MessageRoleUser, Content: "follow up"},
	}}

	single := simulateAnthropicCache(t, []ChatRequest{request})
	if single[0].cachedTokens != 0 {
		t.Fatalf("first request read %d cached tokens, want 0", single[0].cachedTokens)
	}
	if single[0].writtenTokens == 0 {
		t.Fatal("first request wrote no cache entries")
	}

	repeated := simulateAnthropicCache(t, []ChatRequest{request, request})
	if repeated[0].cachedTokens != 0 {
		t.Fatalf("first request of a run read %d cached tokens, want 0", repeated[0].cachedTokens)
	}
	if repeated[1].cachedTokens != repeated[0].totalTokens {
		t.Fatalf("second identical request cached %d tokens, want the whole %d-token prefix", repeated[1].cachedTokens, repeated[0].totalTokens)
	}
	if repeated[1].writtenTokens != 0 || repeated[1].uncachedTokens != 0 {
		t.Fatalf("second identical request wrote %d and left %d uncached, want 0 and 0", repeated[1].writtenTokens, repeated[1].uncachedTokens)
	}
}

func TestCacheSimSyntheticAppendBeatsRewrite(t *testing.T) {
	rewrite := simulateAnthropicCache(t, simSequence(simRewriteLayout))
	appended := simulateAnthropicCache(t, simSequence(simAppendLayout))
	if len(rewrite) != len(appended) {
		t.Fatalf("request counts differ: rewrite %d, append %d", len(rewrite), len(appended))
	}
	if want := 4 * (simCompactionCount + 1); len(rewrite) != want {
		t.Fatalf("request count = %d, want %d for %d compactions", len(rewrite), want, simCompactionCount)
	}

	t.Log("request layout  total cached written uncached breakpoints")
	for i := range rewrite {
		t.Logf("%7d rewrite %6d %6d %7d %8d %6d", i, rewrite[i].totalTokens, rewrite[i].cachedTokens, rewrite[i].writtenTokens, rewrite[i].uncachedTokens, rewrite[i].breakpoints)
		t.Logf("%7d append  %6d %6d %7d %8d %6d", i, appended[i].totalTokens, appended[i].cachedTokens, appended[i].writtenTokens, appended[i].uncachedTokens, appended[i].breakpoints)
		if appended[i].cachedTokens < rewrite[i].cachedTokens {
			t.Errorf("request %d: append cached %d tokens < rewrite cached %d tokens", i, appended[i].cachedTokens, rewrite[i].cachedTokens)
		}
	}

	// Each generation is four requests, and compaction g runs between
	// generation g and g+1. The first request after compaction g (index 4g) must
	// read back through the end of the segment the previous generation ended
	// with (its boundary position in request 4g-1).
	for gen := 2; gen <= simCompactionCount; gen++ {
		firstAfter := 4 * gen
		previous := firstAfter - 1
		if appended[previous].boundaryBytes == 0 {
			t.Fatalf("request %d has no segment boundary", previous)
		}
		if appended[firstAfter].cachedBytes < appended[previous].boundaryBytes {
			t.Errorf("request %d cached %d bytes, want at least the %d bytes through the previous last segment", firstAfter, appended[firstAfter].cachedBytes, appended[previous].boundaryBytes)
		}
	}

	rewriteCost := simCostProxy(rewrite)
	appendCost := simCostProxy(appended)
	t.Logf("cost proxy: rewrite %.1f, append %.1f", rewriteCost, appendCost)
	if appendCost > rewriteCost {
		t.Errorf("append cost proxy %.1f > rewrite cost proxy %.1f", appendCost, rewriteCost)
	}
}
