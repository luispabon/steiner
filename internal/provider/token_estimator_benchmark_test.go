package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tiktoken-go/tokenizer"
)

// Sizes are semantic bytes: the sum of every text input the estimator passes
// to the counter, as recorded by claudeFixtureRecordingCounter. JSON wire bytes
// are not measured.
const (
	claudeBenchKiB = 1 << 10
	claudeBenchMiB = 1 << 20

	claudeBenchSmallTarget = 256 * claudeBenchKiB
	claudeBenchLargeTarget = claudeBenchMiB
)

// Budget for one whole-request estimate at the large target. The semantic size
// must be within [95%, 110%] of the target, one op must take at most one
// second, and allocation may be at most 24 bytes per semantic byte plus 1 MiB.
const (
	claudeBenchMinSizePercent  = 95
	claudeBenchMaxSizePercent  = 110
	claudeBenchMaxNsPerOp      = int64(time.Second)
	claudeBenchAllocPerByte    = 24
	claudeBenchAllocSlackBytes = claudeBenchMiB
)

// Scaling from the small to the large target: the large run must be at least
// 3.5x the small run in semantic bytes, its ns/byte and alloc/byte may be at
// most 2.5x and 1.5x the small run's, and the incremental allocation slope
// (extra B/op per extra semantic byte) may be at most 24.
const (
	claudeBenchMinLargeToSmallBytes = 3.5
	claudeBenchMaxNsPerByteRatio    = 2.5
	claudeBenchMaxAllocPerByteRatio = 1.5
	claudeBenchMaxIncrementalAlloc  = 24
)

// claudeBenchSizes are the request sizes for BenchmarkClaudeRequestEstimate.
var claudeBenchSizes = []struct {
	name  string
	bytes int64
}{
	{"64KiB", 64 * claudeBenchKiB},
	{"256KiB", claudeBenchSmallTarget},
	{"1MiB", claudeBenchLargeTarget},
}

// claudeBenchMeasurement is one testing.Benchmark result for a whole-request estimate.
type claudeBenchMeasurement struct {
	Bytes       int64 // semantic bytes per op, from SetBytes
	NsPerOp     int64
	BytesPerOp  int64
	AllocsPerOp int64
}

// claudeBenchCheckMeasured rejects a measurement whose metrics were not captured.
// A zero time or allocation means the benchmark did not run, so it cannot pass a gate.
func claudeBenchCheckMeasured(m claudeBenchMeasurement) error {
	switch {
	case m.Bytes <= 0:
		return fmt.Errorf("benchmark semantic bytes = %d, want > 0", m.Bytes)
	case m.NsPerOp <= 0:
		return fmt.Errorf("benchmark ns/op = %d, want > 0: unmeasured time", m.NsPerOp)
	case m.BytesPerOp <= 0 || m.AllocsPerOp <= 0:
		return fmt.Errorf("benchmark B/op = %d, allocs/op = %d, want both > 0: unmeasured allocation", m.BytesPerOp, m.AllocsPerOp)
	}
	return nil
}

// claudeBenchCheckSemanticBytes checks that got is within [95%, 110%] of target.
func claudeBenchCheckSemanticBytes(target, got int64) error {
	if got*100 < target*claudeBenchMinSizePercent || got*100 > target*claudeBenchMaxSizePercent {
		return fmt.Errorf("semantic bytes = %d, want within [%d%%, %d%%] of target %d", got, claudeBenchMinSizePercent, claudeBenchMaxSizePercent, target)
	}
	return nil
}

// claudeBenchCheckBudget gates one measurement taken at target semantic bytes.
func claudeBenchCheckBudget(target int64, m claudeBenchMeasurement) error {
	if target <= 0 {
		return fmt.Errorf("benchmark target = %d, want > 0", target)
	}
	if err := claudeBenchCheckMeasured(m); err != nil {
		return err
	}
	if err := claudeBenchCheckSemanticBytes(target, m.Bytes); err != nil {
		return err
	}
	if m.NsPerOp > claudeBenchMaxNsPerOp {
		return fmt.Errorf("benchmark ns/op = %d, want <= %d", m.NsPerOp, claudeBenchMaxNsPerOp)
	}
	limit := claudeBenchAllocPerByte*m.Bytes + claudeBenchAllocSlackBytes
	if m.BytesPerOp > limit {
		return fmt.Errorf("benchmark B/op = %d, want <= %d (24 B per semantic byte + 1 MiB)", m.BytesPerOp, limit)
	}
	return nil
}

// claudeBenchCheckScaling gates the large measurement against the small one.
func claudeBenchCheckScaling(small, large claudeBenchMeasurement) error {
	if err := claudeBenchCheckMeasured(small); err != nil {
		return fmt.Errorf("small run: %w", err)
	}
	if err := claudeBenchCheckMeasured(large); err != nil {
		return fmt.Errorf("large run: %w", err)
	}
	if float64(large.Bytes) < claudeBenchMinLargeToSmallBytes*float64(small.Bytes) {
		return fmt.Errorf("large/small semantic bytes = %d/%d, want >= %.1fx", large.Bytes, small.Bytes, claudeBenchMinLargeToSmallBytes)
	}
	smallNs := float64(small.NsPerOp) / float64(small.Bytes)
	largeNs := float64(large.NsPerOp) / float64(large.Bytes)
	if largeNs > claudeBenchMaxNsPerByteRatio*smallNs {
		return fmt.Errorf("ns/byte ratio = %.2f (%.3f vs %.3f), want <= %.1f", largeNs/smallNs, largeNs, smallNs, claudeBenchMaxNsPerByteRatio)
	}
	smallAlloc := float64(small.BytesPerOp) / float64(small.Bytes)
	largeAlloc := float64(large.BytesPerOp) / float64(large.Bytes)
	if largeAlloc > claudeBenchMaxAllocPerByteRatio*smallAlloc {
		return fmt.Errorf("alloc/byte ratio = %.2f (%.3f vs %.3f), want <= %.1f", largeAlloc/smallAlloc, largeAlloc, smallAlloc, claudeBenchMaxAllocPerByteRatio)
	}
	slope := float64(large.BytesPerOp-small.BytesPerOp) / float64(large.Bytes-small.Bytes)
	if slope > claudeBenchMaxIncrementalAlloc {
		return fmt.Errorf("incremental alloc = %.2f B per semantic byte, want <= %d", slope, claudeBenchMaxIncrementalAlloc)
	}
	return nil
}

// claudeBenchPayloads maps each fixture payload name to its text.
func claudeBenchPayloads(set claudeFixtureSet) (map[string]string, error) {
	payloads := make(map[string]string, len(set.Payloads))
	for _, p := range set.Payloads {
		payloads[p.Name] = string(p.Bytes)
	}
	for _, name := range []string{"code_json", "markdown_prose", "unicode", "mixed"} {
		if _, ok := payloads[name]; !ok {
			return nil, fmt.Errorf("claude fixture has no %q payload", name)
		}
	}
	return payloads, nil
}

// claudeBenchCycle is one repeat of a coding conversation: a user code payload,
// an assistant turn calling grep and read with nested arguments, the two tool
// results (Markdown prose and mixed text), and an assistant Unicode reply. Call
// IDs are fixed, so every repeat has the same semantic bytes.
func claudeBenchCycle(p map[string]string) []Message {
	return []Message{
		{Role: MessageRoleUser, Content: p["code_json"]},
		{
			Role: MessageRoleAssistant,
			ToolCalls: []ToolCall{
				{ID: "call_grep", Name: "grep", Arguments: map[string]any{
					"pattern": "func (l *Ledger) Add",
					"path":    "internal/ledger",
					"options": map[string]any{"context": 2, "ignore_case": false},
				}},
				{ID: "call_read", Name: "read", Arguments: map[string]any{
					"path":    "internal/ledger/ledger.go",
					"options": map[string]any{"offset": 1, "limit": 200},
				}},
			},
		},
		{Role: MessageRoleTool, ToolCallID: "call_grep", Name: "grep", Content: p["markdown_prose"]},
		{Role: MessageRoleTool, ToolCallID: "call_read", Name: "read", Content: p["mixed"]},
		{Role: MessageRoleAssistant, Content: p["unicode"]},
	}
}

// claudeBenchRequest returns the fixture system prompt, the pinned production
// core tools, and repeats copies of the realistic cycle.
func claudeBenchRequest(set claudeFixtureSet, repeats int) (ChatRequest, error) {
	payloads, err := claudeBenchPayloads(set)
	if err != nil {
		return ChatRequest{}, err
	}
	messages := []Message{{Role: MessageRoleSystem, Content: claudeFixtureSystem}}
	for range repeats {
		messages = append(messages, claudeBenchCycle(payloads)...)
	}
	return ChatRequest{Model: set.Manifest.Model, Messages: messages, Tools: set.Tools}, nil
}

// claudeBenchSemanticBytes returns the bytes of every text input the estimator
// counts for request.
func claudeBenchSemanticBytes(request ChatRequest) (int64, error) {
	recorder := &claudeFixtureRecordingCounter{}
	if _, err := countRequestWithCounter(context.Background(), request, recorder); err != nil {
		return 0, fmt.Errorf("count claude bench request: %w", err)
	}
	var total int64
	for _, input := range recorder.inputs {
		total += int64(len(input))
	}
	return total, nil
}

// claudeBenchCycleBytes returns the semantic bytes of the request with no cycles
// (base) and the bytes that one cycle adds.
func claudeBenchCycleBytes(set claudeFixtureSet) (int64, int64, error) {
	empty, err := claudeBenchRequest(set, 0)
	if err != nil {
		return 0, 0, err
	}
	base, err := claudeBenchSemanticBytes(empty)
	if err != nil {
		return 0, 0, err
	}
	one, err := claudeBenchRequest(set, 1)
	if err != nil {
		return 0, 0, err
	}
	withOne, err := claudeBenchSemanticBytes(one)
	if err != nil {
		return 0, 0, err
	}
	cycle := withOne - base
	if cycle <= 0 {
		return 0, 0, fmt.Errorf("claude bench cycle adds %d semantic bytes, want > 0", cycle)
	}
	return base, cycle, nil
}

// claudeBenchSizedRequest returns the request whose whole-cycle count is nearest
// to target, and its measured semantic bytes. Every repeat adds the same bytes,
// so the result is within half a cycle of target.
func claudeBenchSizedRequest(set claudeFixtureSet, target int64) (ChatRequest, int64, error) {
	if target <= 0 {
		return ChatRequest{}, 0, fmt.Errorf("claude bench target = %d, want > 0", target)
	}
	base, cycle, err := claudeBenchCycleBytes(set)
	if err != nil {
		return ChatRequest{}, 0, err
	}
	repeats := (target - base + cycle/2) / cycle
	if repeats < 1 {
		return ChatRequest{}, 0, fmt.Errorf("claude bench target = %d is below one cycle over %d base bytes", target, base)
	}
	request, err := claudeBenchRequest(set, int(repeats))
	if err != nil {
		return ChatRequest{}, 0, err
	}
	got, err := claudeBenchSemanticBytes(request)
	if err != nil {
		return ChatRequest{}, 0, err
	}
	return request, got, nil
}

// claudeBenchEstimate counts request b.N times with counter. The untimed first
// pass loads the counter's vocabulary so that load is not measured.
func claudeBenchEstimate(b *testing.B, counter textCounter, request ChatRequest, semanticBytes int64) {
	b.Helper()
	ctx := context.Background()
	if _, err := countRequestWithCounter(ctx, request, counter); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(semanticBytes)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := countRequestWithCounter(ctx, request, counter); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkClaudeRequestEstimate times whole-request estimates with the old
// tiktoken counter and the candidate ctok counter at each size. One op is one
// real request estimate; SetBytes reports its semantic bytes.
func BenchmarkClaudeRequestEstimate(b *testing.B) {
	set, err := loadClaudeFixture(claudeFixtureDir)
	if err != nil {
		b.Fatal(err)
	}
	candidate, err := claudeFixtureCounter(set.Manifest.Model)
	if err != nil {
		b.Fatal(err)
	}
	old, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		b.Fatalf("load old tokenizer: %v", err)
	}
	for _, size := range claudeBenchSizes {
		request, semanticBytes, err := claudeBenchSizedRequest(set, size.bytes)
		if err != nil {
			b.Fatal(err)
		}
		b.Run("size="+size.name, func(b *testing.B) {
			b.Run("old", func(b *testing.B) { claudeBenchEstimate(b, old, request, semanticBytes) })
			b.Run("candidate", func(b *testing.B) { claudeBenchEstimate(b, candidate, request, semanticBytes) })
		})
	}
}

// claudeBenchWith returns base with f applied to a copy.
func claudeBenchWith(base claudeBenchMeasurement, f func(*claudeBenchMeasurement)) claudeBenchMeasurement {
	f(&base)
	return base
}

func TestClaudeBenchCheckBudget(t *testing.T) {
	const target = claudeBenchLargeTarget
	base := claudeBenchMeasurement{Bytes: target, NsPerOp: int64(time.Second / 2), BytesPerOp: 2 * target, AllocsPerOp: 100}
	tests := []struct {
		name   string
		target int64
		m      claudeBenchMeasurement
		want   string // substring of the expected error; empty means pass
	}{
		{"within budget", target, base, ""},
		{"size at 95% passes", target, claudeBenchWith(base, func(m *claudeBenchMeasurement) { m.Bytes = 996148 }), ""},
		{"size below 95%", target, claudeBenchWith(base, func(m *claudeBenchMeasurement) { m.Bytes = 996147 }), "within"},
		{"size at 110% passes", target, claudeBenchWith(base, func(m *claudeBenchMeasurement) { m.Bytes = 1153433 }), ""},
		{"size above 110%", target, claudeBenchWith(base, func(m *claudeBenchMeasurement) { m.Bytes = 1153434 }), "within"},
		{"zero semantic bytes", target, claudeBenchWith(base, func(m *claudeBenchMeasurement) { m.Bytes = 0 }), "semantic bytes"},
		{"zero time", target, claudeBenchWith(base, func(m *claudeBenchMeasurement) { m.NsPerOp = 0 }), "unmeasured"},
		{"zero bytes per op", target, claudeBenchWith(base, func(m *claudeBenchMeasurement) { m.BytesPerOp = 0 }), "unmeasured"},
		{"zero allocs per op", target, claudeBenchWith(base, func(m *claudeBenchMeasurement) { m.AllocsPerOp = 0 }), "unmeasured"},
		{"time over one second", target, claudeBenchWith(base, func(m *claudeBenchMeasurement) { m.NsPerOp = int64(time.Second) + 1 }), "ns/op"},
		{"allocation at limit passes", target, claudeBenchWith(base, func(m *claudeBenchMeasurement) { m.BytesPerOp = 24*target + claudeBenchMiB }), ""},
		{"allocation over limit", target, claudeBenchWith(base, func(m *claudeBenchMeasurement) { m.BytesPerOp = 24*target + claudeBenchMiB + 1 }), "B/op"},
		{"zero target", 0, base, "target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := claudeBenchCheckBudget(tt.target, tt.m)
			checkClaudeBenchError(t, err, tt.want)
		})
	}
}

func TestClaudeBenchCheckScaling(t *testing.T) {
	const small, large = claudeBenchSmallTarget, claudeBenchLargeTarget
	smallM := claudeBenchMeasurement{Bytes: small, NsPerOp: 10 * small, BytesPerOp: 20 * small, AllocsPerOp: 1000}
	largeM := claudeBenchMeasurement{Bytes: large, NsPerOp: 10 * large, BytesPerOp: 20 * large, AllocsPerOp: 4000}
	largeWith := func(f func(*claudeBenchMeasurement)) claudeBenchMeasurement { return claudeBenchWith(largeM, f) }
	tests := []struct {
		name  string
		small claudeBenchMeasurement
		large claudeBenchMeasurement
		want  string
	}{
		{"linear scaling passes", smallM, largeM, ""},
		{"ns/byte at 2.5x passes", smallM, largeWith(func(m *claudeBenchMeasurement) { m.NsPerOp = 25 * large }), ""},
		{"ns/byte above 2.5x", smallM, largeWith(func(m *claudeBenchMeasurement) { m.NsPerOp = 26 * large }), "ns/byte"},
		{"large under 3.5x small", smallM, claudeBenchMeasurement{Bytes: 3 * small, NsPerOp: 30 * small, BytesPerOp: 60 * small, AllocsPerOp: 3000}, "3.5x"},
		{"equal sizes", smallM, claudeBenchWith(smallM, func(m *claudeBenchMeasurement) { m.NsPerOp *= 4; m.BytesPerOp *= 4 }), "3.5x"},
		{"alloc/byte above 1.5x", smallM, largeWith(func(m *claudeBenchMeasurement) { m.BytesPerOp = 31 * large }), "alloc/byte"},
		{"incremental slope above 24", smallM, largeWith(func(m *claudeBenchMeasurement) { m.BytesPerOp = 28 * large }), "incremental"},
		{"zero small time", claudeBenchWith(smallM, func(m *claudeBenchMeasurement) { m.NsPerOp = 0 }), largeM, "unmeasured"},
		{"zero large allocs", smallM, largeWith(func(m *claudeBenchMeasurement) { m.AllocsPerOp = 0 }), "unmeasured"},
		{"zero small bytes", claudeBenchWith(smallM, func(m *claudeBenchMeasurement) { m.Bytes = 0 }), largeM, "semantic bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := claudeBenchCheckScaling(tt.small, tt.large)
			checkClaudeBenchError(t, err, tt.want)
		})
	}
}

// checkClaudeBenchError fails t unless err is nil when want is empty, or err
// contains want.
func checkClaudeBenchError(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Errorf("unexpected error: %v", err)
	case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
		t.Errorf("error = %v, want one containing %q", err, want)
	}
}

func TestClaudeBenchSizedRequestHitsTargets(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	_, cycle, err := claudeBenchCycleBytes(set)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range claudeBenchSizes {
		t.Run(size.name, func(t *testing.T) {
			_, got, err := claudeBenchSizedRequest(set, size.bytes)
			if err != nil {
				t.Fatal(err)
			}
			if d := got - size.bytes; d > cycle/2 || -d > cycle/2 {
				t.Errorf("semantic bytes = %d, target %d: off by %d, want within half a cycle (%d)", got, size.bytes, d, cycle/2)
			}
		})
	}
}

func TestClaudeBenchSizedRequestRejectsInvalidTargets(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	tests := []struct {
		name   string
		target int64
	}{
		{"zero", 0},
		{"negative", -1},
		{"below one cycle", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := claudeBenchSizedRequest(set, tt.target); err == nil {
				t.Errorf("target %d accepted, want error", tt.target)
			}
		})
	}
}

func TestClaudeBenchRepeatsAddIdenticalSemanticBytes(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	base, cycle, err := claudeBenchCycleBytes(set)
	if err != nil {
		t.Fatal(err)
	}
	for _, repeats := range []int{2, 5} {
		request, err := claudeBenchRequest(set, repeats)
		if err != nil {
			t.Fatal(err)
		}
		got, err := claudeBenchSemanticBytes(request)
		if err != nil {
			t.Fatal(err)
		}
		if want := base + int64(repeats)*cycle; got != want {
			t.Errorf("%d repeats = %d semantic bytes, want %d", repeats, got, want)
		}
	}
}
