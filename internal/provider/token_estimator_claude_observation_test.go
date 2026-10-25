package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// claudeLedgerObservation is what one launch's stream proves: the model the
// stream named and the presence-aware usage of its terminal message. A fake
// launch returns it to claudeLedger.RunAttempt.
type claudeLedgerObservation struct {
	ObservedModel string
	claudeLedgerUsage
	// ThinkingBlocksObserved is true when the stream started a thinking or redacted
	// thinking block. It is audit only.
	ThinkingBlocksObserved bool
}

// errClaudeLedgerStream marks a stream that cannot prove a launch's identity or usage.
var errClaudeLedgerStream = errors.New("claude stream observation rejected")

// claudeLedgerStreamLine decodes the envelope of one stream-json line. The raw
// model and usage fields are what the production decoder does not keep.
type claudeLedgerStreamLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
	Event     *struct {
		Type    string `json:"type"`
		Message *struct {
			Model string             `json:"model"`
			Usage *claudeLedgerUsage `json:"usage"`
		} `json:"message"`
		Usage        *claudeLedgerUsage `json:"usage"`
		ContentBlock *struct {
			Type string `json:"type"`
		} `json:"content_block"`
	} `json:"event"`
}

// claudeLedgerObserveStream reads one stream-json capture for a launch that
// requested model. Every message_start must name a model, all must agree, and the
// one model must equal requested exactly. Each emitted message is checked against
// the raw stream and the production decoder. The capture must end in a successful
// terminal result. The CLI result usage is never read.
func claudeLedgerObserveStream(data []byte, requested string) (claudeLedgerObservation, error) {
	dec := newClaudeSubDecoder(claudeSubDecodeHooks{})
	var (
		models     []string
		current    claudeLedgerUsage // raw usage of the message being streamed
		terminal   claudeLedgerUsage // checked usage of the last emitted message
		sawMessage bool
		sawResult  bool
		thinking   bool // a thinking block was streamed
	)
	for i, raw := range bytes.Split(data, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var env claudeLedgerStreamLine
		if err := json.Unmarshal(line, &env); err != nil {
			return claudeLedgerObservation{}, fmt.Errorf("%w: line %d: %w", errClaudeLedgerStream, i+1, err)
		}
		// Only stream_event envelopes carry the raw Anthropic events read here. Other
		// envelopes are left to the production decoder, which ignores them.
		if env.Type == "stream_event" && env.Event != nil {
			switch env.Event.Type {
			case "message_start":
				if env.Event.Message == nil || env.Event.Message.Model == "" {
					return claudeLedgerObservation{}, fmt.Errorf("%w: line %d: message_start has no model", errClaudeLedgerStream, i+1)
				}
				if !slices.Contains(models, env.Event.Message.Model) {
					models = append(models, env.Event.Message.Model)
				}
				current = claudeLedgerUsage{}
				current.merge(env.Event.Message.Usage)
			case "message_delta":
				current.merge(env.Event.Usage)
			case "content_block_start":
				if cb := env.Event.ContentBlock; cb != nil && (cb.Type == "thinking" || cb.Type == "redacted_thinking") {
					thinking = true
				}
			}
		}
		got, err := dec.decode(claudeSubEvent{
			Type:      env.Type,
			Subtype:   env.Subtype,
			UUID:      env.UUID,
			SessionID: env.SessionID,
			Raw:       json.RawMessage(line),
		})
		if err != nil {
			return claudeLedgerObservation{}, fmt.Errorf("%w: line %d: decode: %w", errClaudeLedgerStream, i+1, err)
		}
		for _, d := range got {
			switch d.Kind {
			case claudeSubDecodeMessage:
				usage, err := claudeLedgerCheckMessage(current, d.Usage)
				if err != nil {
					return claudeLedgerObservation{}, fmt.Errorf("%w: line %d: %w", errClaudeLedgerStream, i+1, err)
				}
				sawMessage, terminal = true, usage
			case claudeSubDecodeResult:
				if d.Result == nil || d.Result.IsError || d.Result.Subtype != "success" {
					return claudeLedgerObservation{}, fmt.Errorf("%w: line %d: terminal result is not a success", errClaudeLedgerStream, i+1)
				}
				sawResult = true
			}
		}
	}
	switch {
	case len(models) == 0:
		return claudeLedgerObservation{}, fmt.Errorf("%w: no message_start model", errClaudeLedgerStream)
	case len(models) > 1:
		return claudeLedgerObservation{}, fmt.Errorf("%w: ambiguous models %q", errClaudeLedgerStream, models)
	case models[0] != requested:
		return claudeLedgerObservation{}, fmt.Errorf("%w: observed model %q, requested %q", errClaudeLedgerStream, models[0], requested)
	case !sawMessage:
		return claudeLedgerObservation{}, fmt.Errorf("%w: no terminal message", errClaudeLedgerStream)
	case !sawResult:
		return claudeLedgerObservation{}, fmt.Errorf("%w: no terminal result", errClaudeLedgerStream)
	}
	return claudeLedgerObservation{ObservedModel: models[0], claudeLedgerUsage: terminal, ThinkingBlocksObserved: thinking}, nil
}

// claudeLedgerCheckMessage checks one emitted message. The raw usage must pass the
// shared usage checks. The production decoder must agree on the cache components
// with presence semantics (an absent raw field is an absent decoder value, zero)
// and on the output count, with the same presence semantics, and on the prompt
// total. It returns the raw usage with PromptTotal set.
func claudeLedgerCheckMessage(raw claudeLedgerUsage, decoded *UsageStats) (claudeLedgerUsage, error) {
	raw.PromptTotal = raw.prompt()
	if err := claudeLedgerValidateUsage(raw); err != nil {
		return claudeLedgerUsage{}, fmt.Errorf("message usage: %w", err)
	}
	if decoded == nil {
		return claudeLedgerUsage{}, errors.New("message usage: decoder reported none")
	}
	if got, want := decoded.CacheCreationInputTokens, claudeLedgerOrZero(raw.CacheCreation); got != want {
		return claudeLedgerUsage{}, fmt.Errorf("cache creation: stream %d, decoder %d", want, got)
	}
	if got, want := decoded.CacheReadInputTokens, claudeLedgerOrZero(raw.CacheRead); got != want {
		return claudeLedgerUsage{}, fmt.Errorf("cache read: stream %d, decoder %d", want, got)
	}
	if got, want := decoded.CompletionTokens, claudeLedgerOrZero(raw.OutputTokens); got != want {
		return claudeLedgerUsage{}, fmt.Errorf("output: stream %d, decoder %d", want, got)
	}
	if decoded.PromptTokens != raw.PromptTotal {
		return claudeLedgerUsage{}, fmt.Errorf("stream prompt %d, decoder prompt %d", raw.PromptTotal, decoded.PromptTokens)
	}
	return raw, nil
}

// claudeLedgerOrZero returns the value of an optional usage component, or zero when absent.
func claudeLedgerOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// merge applies one usage snapshot the way the production decoder does: a field
// the snapshot carries replaces the stored value, and an omitted field keeps it.
func (u *claudeLedgerUsage) merge(src *claudeLedgerUsage) {
	if src == nil {
		return
	}
	if src.Input != nil {
		u.Input = src.Input
	}
	if src.CacheCreation != nil {
		u.CacheCreation = src.CacheCreation
	}
	if src.CacheRead != nil {
		u.CacheRead = src.CacheRead
	}
	if src.OutputTokens != nil {
		u.OutputTokens = src.OutputTokens
	}
}

// prompt returns input plus cache creation plus cache read. Each present field
// counts once and an absent field counts as zero.
func (u claudeLedgerUsage) prompt() int {
	return claudeLedgerOrZero(u.Input) + claudeLedgerOrZero(u.CacheCreation) + claudeLedgerOrZero(u.CacheRead)
}

// claudeLedgerStreamPath returns the path of a synthetic stream fixture.
func claudeLedgerStreamPath(name string) string {
	return filepath.Join(claudeFixtureDir, "streams", name)
}

// claudeLedgerReadStream reads one stream capture, failing the test on error.
func claudeLedgerReadStream(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stream %s: %v", path, err)
	}
	return data
}

// claudeLedgerReplaceOnce returns data with the first occurrence of from replaced
// by to. The fixture must contain from.
func claudeLedgerReplaceOnce(t *testing.T, data []byte, from, to string) []byte {
	t.Helper()
	if !bytes.Contains(data, []byte(from)) {
		t.Fatalf("fixture lacks %q", from)
	}
	return []byte(strings.Replace(string(data), from, to, 1))
}

// claudeLedgerDropLastLine returns data without its final non-empty line.
func claudeLedgerDropLastLine(data []byte) []byte {
	trimmed := bytes.TrimRight(data, "\n")
	return trimmed[:bytes.LastIndexByte(trimmed, '\n')+1]
}

func TestClaudeTokenObservationSplitsPresence(t *testing.T) {
	const haiku = "claude-haiku-5-5"
	tests := []struct {
		name string
		file string
		want claudeLedgerUsage
	}{
		{
			// message_delta omits the cache fields, which keep their message_start values.
			name: "cache fields kept across message_delta",
			file: "usage_split_model.jsonl",
			want: claudeLedgerUsage{Input: claudeLedgerIntPtr(2), CacheCreation: claudeLedgerIntPtr(100), CacheRead: claudeLedgerIntPtr(7), OutputTokens: claudeLedgerIntPtr(42), PromptTotal: 109},
		},
		{
			// cache_creation is absent and cache_read is present as zero. The two stay distinct.
			name: "absent and zero cache fields differ",
			file: "cache_presence.jsonl",
			want: claudeLedgerUsage{Input: claudeLedgerIntPtr(5), CacheRead: claudeLedgerIntPtr(0), OutputTokens: claudeLedgerIntPtr(9), PromptTotal: 5},
		},
		{
			// input is absent, so it stays nil and the prompt is the cache read alone.
			name: "cache read only, input absent",
			file: "usage_no_input.jsonl",
			want: claudeLedgerUsage{CacheRead: claudeLedgerIntPtr(7), OutputTokens: claudeLedgerIntPtr(3), PromptTotal: 7},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs, err := claudeLedgerObserveStream(claudeLedgerReadStream(t, claudeLedgerStreamPath(tt.file)), haiku)
			if err != nil {
				t.Fatalf("observe: %v", err)
			}
			if obs.ObservedModel != haiku {
				t.Errorf("observed model = %q, want %q", obs.ObservedModel, haiku)
			}
			if !reflect.DeepEqual(obs.claudeLedgerUsage, tt.want) {
				t.Errorf("usage = %+v, want %+v", obs.claudeLedgerUsage, tt.want)
			}
		})
	}
}

// The result usage in each capture is cumulative and carries cache_read 500. The
// prompt must come from the terminal message only.
func TestClaudeTokenObservationIgnoresResultUsage(t *testing.T) {
	tests := []struct {
		file  string
		model string
		want  int
	}{
		{"usage_split_model.jsonl", "claude-haiku-5-5", 109},
		{"cache_presence.jsonl", "claude-haiku-5-5", 5},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			obs, err := claudeLedgerObserveStream(claudeLedgerReadStream(t, claudeLedgerStreamPath(tt.file)), tt.model)
			if err != nil {
				t.Fatalf("observe: %v", err)
			}
			if obs.PromptTotal != tt.want {
				t.Errorf("prompt = %d, want %d", obs.PromptTotal, tt.want)
			}
		})
	}
}

// Only stream_event envelopes feed the model and usage reads. A foreign envelope
// that carries a message_start must not change the observed model or usage.
func TestClaudeTokenObservationIgnoresForeignEvents(t *testing.T) {
	fixture := claudeLedgerReadStream(t, claudeLedgerStreamPath("usage_split_model.jsonl"))
	foreign := []byte(`{"type":"system","subtype":"init","event":{"type":"message_start","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1}}}}` + "\n")
	obs, err := claudeLedgerObserveStream(append(foreign, fixture...), "claude-haiku-5-5")
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if obs.ObservedModel != "claude-haiku-5-5" || obs.PromptTotal != 109 {
		t.Errorf("observed = %q prompt %d, want claude-haiku-5-5 and 109", obs.ObservedModel, obs.PromptTotal)
	}
}

func TestClaudeTokenObservationFailsClosedOnModel(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		model string
		want  string
	}{
		{"missing model", filepath.Join("testdata", "claudesub", "turn_usage_split.jsonl"), "claude-haiku-5-5", "message_start has no model"},
		{"wrong model", claudeLedgerStreamPath("model_wrong.jsonl"), "claude-haiku-5-5", "observed model"},
		{"two message_start in one message", claudeLedgerStreamPath("model_ambiguous.jsonl"), "claude-haiku-5-5", "duplicate message_start"},
		{"requested model is a prefix", claudeLedgerStreamPath("usage_split_model.jsonl"), "claude-haiku-5", "observed model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := claudeLedgerObserveStream(claudeLedgerReadStream(t, tt.path), tt.model)
			if !errors.Is(err, errClaudeLedgerStream) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want errClaudeLedgerStream containing %q", err, tt.want)
			}
		})
	}
}

// A second response after message_stop is ambiguous. The production decoder
// rejects it before the ledger sees the model, so the error must still fail closed.
func TestClaudeTokenObservationRejectsAmbiguousTurns(t *testing.T) {
	stream := []byte(`{"type":"stream_event","event":{"type":"message_start","message":{"model":"claude-haiku-5-5","usage":{"input_tokens":1}}}}
{"type":"stream_event","event":{"type":"message_stop"}}
{"type":"stream_event","event":{"type":"message_start","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1}}}}
`)
	_, err := claudeLedgerObserveStream(stream, "claude-haiku-5-5")
	if !errors.Is(err, errClaudeLedgerStream) || !strings.Contains(err.Error(), "another response") {
		t.Errorf("err = %v, want errClaudeLedgerStream containing \"another response\"", err)
	}
}

// Usage that the stream cannot prove fails closed. Each case edits the fixture's
// message_start and message_delta usage, so the decoder's view stays in step.
func TestClaudeTokenObservationFailsClosedOnComponents(t *testing.T) {
	fixture := claudeLedgerReadStream(t, claudeLedgerStreamPath("usage_split_model.jsonl"))
	const (
		startFrom = `"usage":{"input_tokens":2,"cache_creation_input_tokens":100,"cache_read_input_tokens":7}`
		deltaFrom = `"usage":{"input_tokens":2,"output_tokens":42}`
	)
	tests := []struct {
		name             string
		startTo, deltaTo string
	}{
		// Each sum stays positive, so only the per-component check can reject it.
		{"negative cache creation", `"usage":{"input_tokens":200,"cache_creation_input_tokens":-100,"cache_read_input_tokens":7}`, `"usage":{"input_tokens":200,"output_tokens":42}`},
		{"negative cache read", `"usage":{"input_tokens":200,"cache_creation_input_tokens":100,"cache_read_input_tokens":-7}`, `"usage":{"input_tokens":200,"output_tokens":42}`},
		{"negative input", `"usage":{"input_tokens":-1,"cache_creation_input_tokens":100,"cache_read_input_tokens":7}`, `"usage":{"input_tokens":-1,"output_tokens":42}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := claudeLedgerReplaceOnce(t, fixture, startFrom, tt.startTo)
			data = claudeLedgerReplaceOnce(t, data, deltaFrom, tt.deltaTo)
			_, err := claudeLedgerObserveStream(data, "claude-haiku-5-5")
			if !errors.Is(err, errClaudeLedgerStream) || !strings.Contains(err.Error(), "negative") {
				t.Errorf("err = %v, want errClaudeLedgerStream containing \"negative\"", err)
			}
		})
	}
}

// The capture must end in a successful terminal result. A complete valid message
// followed by an error result, or by no result, fails closed.
func TestClaudeTokenObservationRequiresSuccessResult(t *testing.T) {
	fixture := claudeLedgerReadStream(t, claudeLedgerStreamPath("usage_split_model.jsonl"))
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"error result after a complete message", claudeLedgerReplaceOnce(t, fixture, `"subtype":"success","is_error":false`, `"subtype":"error_during_execution","is_error":true`), "terminal result is not a success"},
		{"success subtype marked is_error", claudeLedgerReplaceOnce(t, fixture, `"subtype":"success","is_error":false`, `"subtype":"success","is_error":true`), "success subtype marked is_error"},
		{"no terminal result", claudeLedgerDropLastLine(fixture), "no terminal result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := claudeLedgerObserveStream(tt.data, "claude-haiku-5-5")
			if !errors.Is(err, errClaudeLedgerStream) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want errClaudeLedgerStream containing %q", err, tt.want)
			}
		})
	}
}

func TestClaudeTokenObservationFailsClosedOnUsage(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{"terminal message without usage", "usage_missing.jsonl", "usage has no components"},
		{"non-positive prompt", "prompt_nonpositive.jsonl", "is not positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := claudeLedgerObserveStream(claudeLedgerReadStream(t, claudeLedgerStreamPath(tt.file)), "claude-haiku-5-5")
			if !errors.Is(err, errClaudeLedgerStream) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want errClaudeLedgerStream containing %q", err, tt.want)
			}
		})
	}
}
