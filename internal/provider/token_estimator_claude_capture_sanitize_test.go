package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The sanitizer derives each captures/attempt-NN.jsonl from one retained original
// capture. It keeps only the fields claudeLedgerObserveStream reads, with synthetic
// IDs. It rejects every envelope or event kind it does not list, and any thinking
// content, so no rejected kind can reach a fixture.

// errClaudeCaptureSanitize marks a capture the sanitizer will not derive from.
var errClaudeCaptureSanitize = errors.New("claude capture sanitize rejected")

// claudeCaptureSession is the synthetic session ID of every sanitized capture.
const claudeCaptureSession = "00000000-0000-4000-8000-000000000001"

// claudeCaptureUsageKeys are the four token counters a sanitized usage object may
// hold. An absent counter stays absent, because presence is what the observer reads.
var claudeCaptureUsageKeys = []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens"}

// claudeCaptureKeyAllow lists the object keys a sanitized capture may hold, keyed by
// the key that holds the object. The root object has the key "". Tests enforce it
// recursively.
var claudeCaptureKeyAllow = map[string][]string{
	"":              {"type", "subtype", "model", "is_error", "stop_reason", "session_id", "uuid", "event"},
	"event":         {"type", "index", "message", "content_block", "delta", "usage"},
	"message":       {"model", "usage"},
	"content_block": {"type", "text"},
	"delta":         {"type", "text", "stop_reason"},
	"usage":         claudeCaptureUsageKeys,
}

// sanitizeClaudeCapture derives the sanitized stream from one raw capture. The CLI
// init line keeps its model. Assistant echoes, rate-limit events, control responses,
// and status lines are dropped. Stream events keep the fields the observer reads, and
// the terminal success result keeps its subtype, error flag, and stop reason. Every
// other kind fails the capture. Kept lines get synthetic session and message IDs in
// order.
func sanitizeClaudeCapture(raw []byte) ([]byte, error) {
	var out bytes.Buffer
	kept := 0
	for i, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		env, err := claudeCaptureDecodeObject(line)
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %w", errClaudeCaptureSanitize, i+1, err)
		}
		clean, err := claudeCaptureSanitizeEnvelope(env)
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %w", errClaudeCaptureSanitize, i+1, err)
		}
		if clean == nil {
			continue
		}
		kept++
		clean["session_id"] = claudeCaptureSession
		clean["uuid"] = fmt.Sprintf("00000000-0000-4000-8000-%012d", kept)
		b, err := json.Marshal(clean)
		if err != nil {
			return nil, fmt.Errorf("encode sanitized line %d: %w", kept, err)
		}
		out.Write(b)
		out.WriteByte('\n')
	}
	if kept == 0 {
		return nil, fmt.Errorf("%w: no line is kept", errClaudeCaptureSanitize)
	}
	return out.Bytes(), nil
}

// claudeCaptureDecodeObject decodes one JSON object. Numbers stay json.Number so
// their text round-trips exactly.
func claudeCaptureDecodeObject(line []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errors.New("line is not a JSON object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("line holds more than one JSON value")
	}
	return obj, nil
}

// claudeCaptureSanitizeEnvelope returns the sanitized form of one envelope, or nil
// when the envelope kind is dropped.
func claudeCaptureSanitizeEnvelope(env map[string]any) (map[string]any, error) {
	typ, err := claudeCaptureString(env, "type")
	if err != nil {
		return nil, err
	}
	switch typ {
	case "system":
		sub, err := claudeCaptureString(env, "subtype")
		if err != nil {
			return nil, err
		}
		switch sub {
		case "init":
			model, err := claudeCaptureString(env, "model")
			if err != nil {
				return nil, err
			}
			return map[string]any{"type": typ, "subtype": sub, "model": model}, nil
		case "status":
			return nil, nil
		}
		return nil, fmt.Errorf("unknown system subtype %q", sub)
	case "assistant", "control_response", "rate_limit_event":
		// The observer never reads these. They carry account and quota data, so they
		// are dropped.
		return nil, nil
	case "stream_event":
		ev, ok := env["event"].(map[string]any)
		if !ok {
			return nil, errors.New("stream_event has no event object")
		}
		clean, err := claudeCaptureSanitizeEvent(ev)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": typ, "event": clean}, nil
	case "result":
		return claudeCaptureSanitizeResult(env)
	}
	return nil, fmt.Errorf("unknown envelope type %q", typ)
}

// claudeCaptureSanitizeEvent returns the sanitized form of one stream event. Text
// blocks are kept and every other block or delta kind is rejected.
func claudeCaptureSanitizeEvent(ev map[string]any) (map[string]any, error) {
	typ, err := claudeCaptureString(ev, "type")
	if err != nil {
		return nil, err
	}
	switch typ {
	case "message_start":
		msg, ok := ev["message"].(map[string]any)
		if !ok {
			return nil, errors.New("message_start has no message object")
		}
		model, err := claudeCaptureString(msg, "model")
		if err != nil {
			return nil, err
		}
		usage, err := claudeCaptureUsage(msg)
		if err != nil {
			return nil, err
		}
		message := map[string]any{"model": model}
		if usage != nil {
			message["usage"] = usage
		}
		return map[string]any{"type": typ, "message": message}, nil
	case "content_block_start":
		index, err := claudeCaptureIndex(ev)
		if err != nil {
			return nil, err
		}
		block, ok := ev["content_block"].(map[string]any)
		if !ok {
			return nil, errors.New("content_block_start has no content_block object")
		}
		btype, err := claudeCaptureString(block, "type")
		if err != nil {
			return nil, err
		}
		if btype != "text" {
			return nil, fmt.Errorf("content block %q is not kept", btype)
		}
		text, err := claudeCaptureString(block, "text")
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": typ, "index": index, "content_block": map[string]any{"type": btype, "text": text}}, nil
	case "content_block_delta":
		index, err := claudeCaptureIndex(ev)
		if err != nil {
			return nil, err
		}
		delta, ok := ev["delta"].(map[string]any)
		if !ok {
			return nil, errors.New("content_block_delta has no delta object")
		}
		dtype, err := claudeCaptureString(delta, "type")
		if err != nil {
			return nil, err
		}
		if dtype != "text_delta" {
			return nil, fmt.Errorf("delta %q is not kept", dtype)
		}
		text, err := claudeCaptureString(delta, "text")
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": typ, "index": index, "delta": map[string]any{"type": dtype, "text": text}}, nil
	case "content_block_stop":
		index, err := claudeCaptureIndex(ev)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": typ, "index": index}, nil
	case "message_delta":
		delta, ok := ev["delta"].(map[string]any)
		if !ok {
			return nil, errors.New("message_delta has no delta object")
		}
		kept := map[string]any{}
		if stop, ok := delta["stop_reason"].(string); ok {
			kept["stop_reason"] = stop
		}
		out := map[string]any{"type": typ, "delta": kept}
		usage, err := claudeCaptureUsage(ev)
		if err != nil {
			return nil, err
		}
		if usage != nil {
			out["usage"] = usage
		}
		return out, nil
	case "message_stop":
		return map[string]any{"type": typ}, nil
	}
	return nil, fmt.Errorf("unknown stream event type %q", typ)
}

// claudeCaptureSanitizeResult keeps only the success essentials of the terminal
// result. Its usage, cost, IDs, and text are dropped. An error result fails the capture.
func claudeCaptureSanitizeResult(env map[string]any) (map[string]any, error) {
	sub, err := claudeCaptureString(env, "subtype")
	if err != nil {
		return nil, err
	}
	isErr, ok := env["is_error"].(bool)
	if !ok {
		return nil, errors.New("result has no is_error flag")
	}
	stop, err := claudeCaptureString(env, "stop_reason")
	if err != nil {
		return nil, err
	}
	if sub != "success" || isErr {
		return nil, fmt.Errorf("terminal result %q is not a success", sub)
	}
	return map[string]any{"type": "result", "subtype": sub, "is_error": false, "stop_reason": stop}, nil
}

// claudeCaptureUsage copies the present token counters of obj["usage"]. It returns
// nil when obj has no usage object.
func claudeCaptureUsage(obj map[string]any) (map[string]any, error) {
	v, present := obj["usage"]
	if !present || v == nil {
		return nil, nil
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("usage is not an object")
	}
	out := map[string]any{}
	for _, key := range claudeCaptureUsageKeys {
		v, ok := raw[key]
		if !ok {
			continue
		}
		n, ok := v.(json.Number)
		if !ok {
			return nil, fmt.Errorf("usage %s is not a number", key)
		}
		if _, err := n.Int64(); err != nil {
			return nil, fmt.Errorf("usage %s: %w", key, err)
		}
		out[key] = n
	}
	return out, nil
}

// claudeCaptureIndex returns the content block index of a stream event.
func claudeCaptureIndex(ev map[string]any) (json.Number, error) {
	n, ok := ev["index"].(json.Number)
	if !ok {
		return "", errors.New("index is not a number")
	}
	if _, err := n.Int64(); err != nil {
		return "", fmt.Errorf("index: %w", err)
	}
	return n, nil
}

// claudeCaptureString returns obj[key] when it is a string.
func claudeCaptureString(obj map[string]any, key string) (string, error) {
	s, ok := obj[key].(string)
	if !ok {
		return "", fmt.Errorf("%s is not a string", key)
	}
	return s, nil
}

// claudeCaptureSource is one original capture after sanitizing and matching.
type claudeCaptureSource struct {
	Attempt      claudeLedgerRecord
	SourceSHA256 string
	SourceBytes  int
	Data         []byte
}

// claudeCaptureSanitizeDir sanitizes every original capture in dir except the control
// file, which its pinned digest excludes. Each original is hashed before it is
// sanitized. Each sanitized capture must replay to one succeeded attempt, matched by
// model and prompt total, with that attempt's identity, usage, and thinking flag. The
// result is sorted by attempt ID.
func claudeCaptureSanitizeDir(dir string, attempts []claudeLedgerRecord) ([]claudeCaptureSource, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read original captures: %w", err)
	}
	var (
		srcs []claudeCaptureSource
		keys []claudeCaptureKey
		obs  []claudeLedgerObservation
	)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read original capture: %w", err)
		}
		digest := claudeDigest(raw)
		if digest == claudeProvenanceControlSHA256 {
			continue
		}
		clean, err := sanitizeClaudeCapture(raw)
		if err != nil {
			return nil, fmt.Errorf("sanitize original %s: %w", digest, err)
		}
		o, err := claudeCaptureReplayAny(clean)
		if err != nil {
			return nil, fmt.Errorf("replay original %s: %w", digest, err)
		}
		keys = append(keys, claudeCaptureKey{Model: o.ObservedModel, PromptTotal: o.PromptTotal})
		obs = append(obs, o)
		srcs = append(srcs, claudeCaptureSource{SourceSHA256: digest, SourceBytes: len(raw), Data: clean})
	}
	matched, err := claudeCaptureMatch(attempts, keys)
	if err != nil {
		return nil, err
	}
	for c := range srcs {
		rec := attempts[matched[c]]
		if err := claudeCaptureCheckRecord(obs[c], rec); err != nil {
			return nil, err
		}
		srcs[c].Attempt = rec
	}
	slices.SortFunc(srcs, func(a, b claudeCaptureSource) int {
		return a.Attempt.AttemptID - b.Attempt.AttemptID
	})
	return srcs, nil
}

// claudeCaptureIDPattern matches the synthetic session and message IDs.
var claudeCaptureIDPattern = regexp.MustCompile(`^00000000-0000-4000-8000-[0-9]{12}$`)

// claudeCaptureCheckKeys returns an error for the first object key the allowlist does
// not hold in its context. It walks objects and arrays recursively.
func claudeCaptureCheckKeys(ctx string, v any) error {
	switch x := v.(type) {
	case map[string]any:
		for key, child := range x {
			if !slices.Contains(claudeCaptureKeyAllow[ctx], key) {
				return fmt.Errorf("key %q is not allowed under %q", key, ctx)
			}
			if err := claudeCaptureCheckKeys(key, child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range x {
			if err := claudeCaptureCheckKeys(ctx, child); err != nil {
				return err
			}
		}
	}
	return nil
}

// claudeCaptureProblems lists every contract breach in one sanitized line: a key the
// allowlist does not hold, a forbidden key or path value, or a non-synthetic ID.
func claudeCaptureProblems(label string, line []byte) []string {
	doc, err := claudeCaptureDecodeObject(line)
	if err != nil {
		return []string{fmt.Sprintf("%s is not one JSON object: %v", label, err)}
	}
	var out []string
	if err := claudeCaptureCheckKeys("", doc); err != nil {
		out = append(out, fmt.Sprintf("%s: %v", label, err))
	}
	for _, key := range []string{"session_id", "uuid"} {
		if id, ok := doc[key].(string); !ok || !claudeCaptureIDPattern.MatchString(id) {
			out = append(out, fmt.Sprintf("%s: %s is not a synthetic ID", label, key))
		}
	}
	return claudeGateViolations(label, doc, out)
}

// claudeCaptureRawLines is a minimal CLI turn with one text block. It also carries
// the dropped kinds, so a test can see them removed.
var claudeCaptureRawLines = []string{
	`{"type":"system","subtype":"init","cwd":"/private/work","model":"claude-opus-5-5","session_id":"s","uuid":"u"}`,
	`{"type":"system","subtype":"status","status":"requesting","session_id":"s"}`,
	`{"type":"stream_event","event":{"type":"message_start","message":{"model":"claude-opus-5-5","id":"msg_1","usage":{"input_tokens":2,"cache_creation_input_tokens":5,"cache_read_input_tokens":0,"output_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":5}}}},"session_id":"s","uuid":"u"}`,
	`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}},"session_id":"s","uuid":"u"}`,
	`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}},"session_id":"s","uuid":"u"}`,
	`{"type":"assistant","message":{"model":"claude-opus-5-5"},"session_id":"s","uuid":"u"}`,
	`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"},"session_id":"s","uuid":"u"}`,
	`{"type":"stream_event","event":{"type":"content_block_stop","index":0},"session_id":"s","uuid":"u"}`,
	`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}},"session_id":"s","uuid":"u"}`,
	`{"type":"stream_event","event":{"type":"message_stop"},"session_id":"s","uuid":"u"}`,
	`{"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","num_turns":1,"total_cost_usd":0.01,"session_id":"s","uuid":"u","result":"OK"}`,
}

// TestClaudeTokenCaptureSanitizeKeepsStream checks that the minimal turn keeps eight
// lines, drops the rest, passes the allowlist, and replays to its usage.
func TestClaudeTokenCaptureSanitizeKeepsStream(t *testing.T) {
	clean, err := sanitizeClaudeCapture([]byte(strings.Join(claudeCaptureRawLines, "\n") + "\n"))
	if err != nil {
		t.Fatalf("sanitize valid capture: %v", err)
	}
	lines := bytes.Split(bytes.TrimRight(clean, "\n"), []byte("\n"))
	if len(lines) != 8 {
		t.Fatalf("kept %d lines, want 8", len(lines))
	}
	for i, line := range lines {
		for _, msg := range claudeCaptureProblems(fmt.Sprintf("line %d", i+1), line) {
			t.Error(msg)
		}
	}
	for _, dropped := range []string{"rate_limit", "assistant", "status", "cost", "cwd", "/private"} {
		if bytes.Contains(clean, []byte(dropped)) {
			t.Errorf("sanitized capture holds dropped content %q", dropped)
		}
	}
	obs, err := claudeCaptureReplayAny(clean)
	if err != nil {
		t.Fatalf("replay sanitized capture: %v", err)
	}
	if obs.ObservedModel != "claude-opus-5-5" || obs.PromptTotal != 7 || obs.OutputTokens == nil || *obs.OutputTokens != 1 || obs.ThinkingBlocksObserved {
		t.Errorf("replay = %+v, want opus, prompt 7, output 1, no thinking", obs)
	}
}

// TestClaudeTokenCaptureSanitizeAllowlist checks every committed capture against the
// key allowlist, the forbidden-key and path rules, and the synthetic ID form. It also
// checks that each breach kind is reported.
func TestClaudeTokenCaptureSanitizeAllowlist(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(claudeFixtureDir, "captures", "*.jsonl"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("list sanitized captures: %v (%d found)", err, len(paths))
	}
	for _, path := range paths {
		data := claudeLedgerReadStream(t, path)
		for i, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
			for _, msg := range claudeCaptureProblems(fmt.Sprintf("%s line %d", filepath.Base(path), i+1), line) {
				t.Error(msg)
			}
		}
	}
	ids := `,"session_id":"00000000-0000-4000-8000-000000000001","uuid":"00000000-0000-4000-8000-000000000002"`
	bad := []struct{ name, line string }{
		{"forbidden key", `{"type":"system","subtype":"init","model":"claude-opus-5-5","apiKey":"x"` + ids + `}`},
		{"unlisted usage counter", `{"type":"stream_event","event":{"type":"message_stop","usage":{"service_tier":"standard"}}` + ids + `}`},
		{"unlisted message key", `{"type":"stream_event","event":{"type":"message_start","message":{"model":"claude-opus-5-5","id":"msg_1"}}` + ids + `}`},
		{"path value", `{"type":"system","subtype":"init","model":"/home/user/model"` + ids + `}`},
		{"real session ID", `{"type":"system","subtype":"init","model":"claude-opus-5-5","session_id":"real-session","uuid":"00000000-0000-4000-8000-000000000002"}`},
	}
	for _, tc := range bad {
		if got := claudeCaptureProblems(tc.name, []byte(tc.line)); len(got) == 0 {
			t.Errorf("%s: no problem reported", tc.name)
		}
	}
}

// TestClaudeTokenCaptureSanitizeRejects checks that each unknown kind, thinking
// content, error result, or malformed value fails the whole capture.
func TestClaudeTokenCaptureSanitizeRejects(t *testing.T) {
	replace := func(i int, line string) []byte {
		lines := slices.Clone(claudeCaptureRawLines)
		lines[i] = line
		return []byte(strings.Join(lines, "\n") + "\n")
	}
	tests := []struct {
		name string
		raw  []byte
	}{
		{"unknown envelope", replace(1, `{"type":"telemetry"}`)},
		{"unknown system subtype", replace(0, `{"type":"system","subtype":"hook_started","model":"claude-opus-5-5"}`)},
		{"unknown stream event", replace(7, `{"type":"stream_event","event":{"type":"ping"}}`)},
		{"thinking block", replace(3, `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`)},
		{"thinking delta", replace(4, `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"x"}}}`)},
		{"error result", replace(10, `{"type":"result","subtype":"error_max_turns","is_error":true,"stop_reason":"max_tokens"}`)},
		{"result without error flag", replace(10, `{"type":"result","subtype":"success","stop_reason":"end_turn"}`)},
		{"fractional usage", replace(2, `{"type":"stream_event","event":{"type":"message_start","message":{"model":"claude-opus-5-5","usage":{"input_tokens":2.5}}}}`)},
		{"stream event without event", replace(2, `{"type":"stream_event"}`)},
		{"two values on one line", replace(0, `{"type":"system","subtype":"init","model":"claude-opus-5-5"} {}`)},
		{"no kept lines", []byte(`{"type":"rate_limit_event"}` + "\n")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := sanitizeClaudeCapture(tc.raw); !errors.Is(err, errClaudeCaptureSanitize) {
				t.Errorf("sanitize error = %v, want %v", err, errClaudeCaptureSanitize)
			}
		})
	}
}
