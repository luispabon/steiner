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
		Usage *claudeLedgerUsage `json:"usage"`
	} `json:"event"`
}

// claudeLedgerObserveStream reads one stream-json capture for a launch that
// requested model. Every message_start must name a model, all must agree, and the
// one model must equal requested exactly. The terminal message usage comes from
// the production decoder. The presence-aware fields come from the raw stream, and
// the two prompt totals must match. The CLI result usage is never read.
func claudeLedgerObserveStream(data []byte, requested string) (claudeLedgerObservation, error) {
	dec := newClaudeSubDecoder(claudeSubDecodeHooks{})
	var (
		models     []string
		current    claudeLedgerUsage // raw usage of the message being streamed
		terminal   claudeLedgerUsage // raw usage when the last message was emitted
		decoded    *UsageStats       // decoder usage of the last emitted message
		sawMessage bool
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
		if env.Event != nil {
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
			if d.Kind == claudeSubDecodeMessage {
				sawMessage, decoded, terminal = true, d.Usage, current
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
	case !sawMessage || decoded == nil:
		return claudeLedgerObservation{}, fmt.Errorf("%w: terminal message has no input usage", errClaudeLedgerStream)
	}
	prompt := terminal.prompt()
	if prompt != decoded.PromptTokens {
		return claudeLedgerObservation{}, fmt.Errorf("%w: stream prompt %d, decoder prompt %d", errClaudeLedgerStream, prompt, decoded.PromptTokens)
	}
	if prompt <= 0 {
		return claudeLedgerObservation{}, fmt.Errorf("%w: prompt %d is not positive", errClaudeLedgerStream, prompt)
	}
	terminal.PromptTotal = prompt
	return claudeLedgerObservation{ObservedModel: models[0], claudeLedgerUsage: terminal}, nil
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
}

// prompt returns input plus cache creation plus cache read. Each present field
// counts once and an absent field counts as zero.
func (u claudeLedgerUsage) prompt() int {
	total := 0
	for _, v := range []*int{u.Input, u.CacheCreation, u.CacheRead} {
		if v != nil {
			total += *v
		}
	}
	return total
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
			want: claudeLedgerUsage{Input: claudeLedgerIntPtr(2), CacheCreation: claudeLedgerIntPtr(100), CacheRead: claudeLedgerIntPtr(7), PromptTotal: 109},
		},
		{
			// cache_creation is absent and cache_read is present as zero. The two stay distinct.
			name: "absent and zero cache fields differ",
			file: "cache_presence.jsonl",
			want: claudeLedgerUsage{Input: claudeLedgerIntPtr(5), CacheRead: claudeLedgerIntPtr(0), PromptTotal: 5},
		},
		{
			// input is absent, so it stays nil and the prompt is the cache read alone.
			name: "cache read only, input absent",
			file: "usage_no_input.jsonl",
			want: claudeLedgerUsage{CacheRead: claudeLedgerIntPtr(7), PromptTotal: 7},
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

func TestClaudeTokenObservationFailsClosedOnUsage(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{"terminal message without usage", "usage_missing.jsonl", "terminal message has no input usage"},
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
