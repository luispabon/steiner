package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/tiktoken-go/tokenizer"
)

// The Claude estimation fixtures under testdata/claude_token_estimation are
// authored text payloads for synthetic requests. They hold no recorded API
// traffic and no measured token counts.
const (
	claudeSyntheticDir    = "testdata/claude_token_estimation"
	claudeSyntheticModel  = "claude-synthetic-estimation"
	claudeSyntheticSystem = "Synthetic system prompt."
)

// claudeSyntheticPayloadNames lists the authored payload files in report order.
var claudeSyntheticPayloadNames = []string{"code_json", "markdown_prose", "unicode", "mixed"}

// claudeSyntheticCounter returns a fresh ctok counter for the synthetic model.
// It fails when selection falls back to a tiktoken codec instead of ctok.
func claudeSyntheticCounter() (textCounter, error) {
	counter, meta, err := selectTextCounter(claudeSyntheticModel, counterSelectOptions{
		ids: map[string]struct{}{claudeSyntheticModel: {}},
		codecLoader: func(string) (tokenizer.Codec, error) {
			return nil, errors.New("synthetic claude model must not load a tiktoken codec")
		},
		candidate: &claudeTextCounter{},
	})
	if err != nil {
		return nil, fmt.Errorf("select synthetic claude counter: %w", err)
	}
	if meta.strategy != TokenizerStrategyCtok {
		return nil, fmt.Errorf("synthetic claude strategy = %q, want %q", meta.strategy, TokenizerStrategyCtok)
	}
	return counter, nil
}

// loadClaudeSyntheticPayloads reads the authored payload files in dir.
func loadClaudeSyntheticPayloads(dir string) (map[string]string, error) {
	payloads := make(map[string]string, len(claudeSyntheticPayloadNames))
	for _, name := range claudeSyntheticPayloadNames {
		path := filepath.Join(dir, "payloads", name+".txt")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read claude synthetic payload %s: %w", name, err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("read claude synthetic payload %s: file is empty", name)
		}
		payloads[name] = string(data)
	}
	return payloads, nil
}

// mustClaudeSyntheticPayloads loads the payloads or stops the test.
func mustClaudeSyntheticPayloads(t testing.TB) map[string]string {
	t.Helper()
	payloads, err := loadClaudeSyntheticPayloads(claudeSyntheticDir)
	if err != nil {
		t.Fatal(err)
	}
	return payloads
}

// claudeSyntheticDelta returns CountContent(claudeSyntheticSystem+payload) minus
// baseline. The payload is counted verbatim, with no trimming or normalization.
func claudeSyntheticDelta(counter textCounter, baseline int, payload string) (int, error) {
	total, err := counter.Count(claudeSyntheticSystem + payload)
	if err != nil {
		return 0, fmt.Errorf("count claude synthetic payload: %w", err)
	}
	return total - baseline, nil
}

// claudeSyntheticRecordingCounter counts text as its byte length and records each input.
type claudeSyntheticRecordingCounter struct {
	inputs []string
}

func (c *claudeSyntheticRecordingCounter) Count(text string) (int, error) {
	c.inputs = append(c.inputs, text)
	return len(text), nil
}

func TestClaudeSyntheticPayloadFixtures(t *testing.T) {
	t.Parallel()
	payloads := mustClaudeSyntheticPayloads(t)
	needles := []struct {
		payload string
		want    []string
	}{
		{"code_json", []string{"func (l *Ledger) Add", `"sku": "BLT-204"`}},
		{"markdown_prose", []string{"\n## ", "\n| ", "```sh"}},
		{"mixed", []string{`{"batch":`, "def summarize(", "\u2705"}},
	}
	for _, n := range needles {
		for _, sub := range n.want {
			if !strings.Contains(payloads[n.payload], sub) {
				t.Errorf("%s payload lacks %q", n.payload, sub)
			}
		}
	}
	runes := []rune(payloads["unicode"])
	for _, check := range []struct {
		name  string
		match func(rune) bool
	}{
		{"combining mark", func(r rune) bool { return unicode.Is(unicode.Mn, r) }},
		{"Han ideograph", func(r rune) bool { return unicode.Is(unicode.Han, r) }},
		{"zero width joiner", func(r rune) bool { return r == 0x200d }},
		{"emoji variation selector", func(r rune) bool { return r == 0xfe0f }},
		{"emoji skin tone modifier", func(r rune) bool { return r >= 0x1f3fb && r <= 0x1f3ff }},
	} {
		if !slices.ContainsFunc(runes, check.match) {
			t.Errorf("unicode payload has no %s", check.name)
		}
	}
}

func TestClaudeSyntheticDeltaCountsRawPayload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload string
	}{
		{"leading tab", "\tindented line\n"},
		{"leading and trailing spaces", "  padded  \n\n"},
		{"crlf line endings", "first\r\nsecond\r\n"},
		{"tab and crlf mix", "col\tvalue\r\n\t\r\n"},
		{"no final newline", "tail without newline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := &claudeSyntheticRecordingCounter{}
			delta, err := claudeSyntheticDelta(counter, len(claudeSyntheticSystem), tt.payload)
			if err != nil {
				t.Fatal(err)
			}
			want := claudeSyntheticSystem + tt.payload
			if len(counter.inputs) != 1 || counter.inputs[0] != want {
				t.Fatalf("counted %q, want verbatim %q", counter.inputs, want)
			}
			if delta != len(tt.payload) {
				t.Errorf("delta = %d, want %d raw payload bytes", delta, len(tt.payload))
			}
		})
	}
}

func TestClaudeSyntheticEstimatesAreDeterministic(t *testing.T) {
	t.Parallel()
	payloads := mustClaudeSyntheticPayloads(t)
	for _, name := range claudeSyntheticPayloadNames {
		t.Run(name, func(t *testing.T) {
			var firstBaseline, firstDelta int
			for i := range 2 {
				counter, err := claudeSyntheticCounter()
				if err != nil {
					t.Fatal(err)
				}
				baseline, err := counter.Count(claudeSyntheticSystem)
				if err != nil {
					t.Fatal(err)
				}
				delta, err := claudeSyntheticDelta(counter, baseline, payloads[name])
				if err != nil {
					t.Fatal(err)
				}
				if delta <= 0 {
					t.Fatalf("delta = %d, want > 0", delta)
				}
				if i == 0 {
					firstBaseline, firstDelta = baseline, delta
					continue
				}
				if baseline != firstBaseline || delta != firstDelta {
					t.Fatalf("second count = (%d, %d), want (%d, %d)", baseline, delta, firstBaseline, firstDelta)
				}
			}
		})
	}
}

func TestClaudeSyntheticRequestEstimateUsesContentCounter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	payloads := mustClaudeSyntheticPayloads(t)
	request := ChatRequest{
		Model: "claude-opus-5-5",
		Messages: []Message{
			{Role: MessageRoleUser, Content: payloads["mixed"]},
		},
	}
	got, err := EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := countRequestWithCounter(ctx, request, &claudeTextCounter{})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("EstimateChatRequestTokens = %d, content count = %d", got, want)
	}
}
