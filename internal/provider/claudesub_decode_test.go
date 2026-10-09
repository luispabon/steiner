package provider

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeSubDecodeEvents reads a JSONL stream-json fixture and returns the
// envelopes the process reader would forward, in order.
func claudeSubDecodeEvents(t *testing.T, name string) []claudeSubEvent {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "claudesub", name))
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	defer f.Close()
	var events []claudeSubEvent
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var env struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("parse fixture %s line %q: %v", name, line, err)
		}
		events = append(events, claudeSubEvent{Type: env.Type, Subtype: env.Subtype, Raw: json.RawMessage(line)})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan fixture %s: %v", name, err)
	}
	return events
}

// claudeSubDecodeAll runs one decoder over events, failing on any decode error.
func claudeSubDecodeAll(t *testing.T, hooks claudeSubDecodeHooks, events []claudeSubEvent) []claudeSubDecoded {
	t.Helper()
	d := newClaudeSubDecoder(hooks)
	var out []claudeSubDecoded
	for _, ev := range events {
		got, err := d.decode(ev)
		if err != nil {
			t.Fatalf("decode %s envelope: %v", ev.Type, err)
		}
		out = append(out, got...)
	}
	return out
}

func claudeSubDecodedOfKind(events []claudeSubDecoded, kind claudeSubDecodedKind) []claudeSubDecoded {
	var out []claudeSubDecoded
	for _, ev := range events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func claudeSubJoinedText(events []claudeSubDecoded) string {
	var b strings.Builder
	for _, ev := range claudeSubDecodedOfKind(events, claudeSubDecodeText) {
		b.WriteString(ev.Text)
	}
	return b.String()
}

// claudeSubKindIndex returns the position of the first event of kind, or -1.
func claudeSubKindIndex(events []claudeSubDecoded, kind claudeSubDecodedKind) int {
	for i, ev := range events {
		if ev.Kind == kind {
			return i
		}
	}
	return -1
}

// claudeSubStripPrefix is the name hook a test uses to model the MCP host's
// reverse mapping for names that fit without shortening.
func claudeSubStripPrefix(cliName string) string {
	return strings.TrimPrefix(cliName, claudeSubToolPrefix)
}

func TestClaudeSubDecodeTextTurn(t *testing.T) {
	events := claudeSubDecodeAll(t, claudeSubDecodeHooks{}, claudeSubDecodeEvents(t, "turn_text.jsonl"))

	texts := claudeSubDecodedOfKind(events, claudeSubDecodeText)
	if len(texts) != 2 {
		t.Fatalf("text deltas = %d, want 2", len(texts))
	}
	if got := claudeSubJoinedText(events); got != "Hello world" {
		t.Errorf("joined text = %q, want %q", got, "Hello world")
	}
	thinking := claudeSubDecodedOfKind(events, claudeSubDecodeThinking)
	if len(thinking) != 1 || thinking[0].Thinking != "Let me think" {
		t.Errorf("thinking = %+v, want one %q delta", thinking, "Let me think")
	}

	usage := claudeSubDecodedOfKind(events, claudeSubDecodeUsage)
	if len(usage) != 2 {
		t.Fatalf("usage events = %d, want 2 (message_start and message_delta)", len(usage))
	}
	last := usage[len(usage)-1].Usage
	if last == nil || last.PromptTokens != 1194 || last.CompletionTokens != 245 || last.TotalTokens != 1439 {
		t.Errorf("last usage = %+v, want prompt 1194 completion 245 total 1439", last)
	}

	messages := claudeSubDecodedOfKind(events, claudeSubDecodeMessage)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	m := messages[0]
	if m.Message == nil || m.Message.Content != "Hello world" {
		t.Fatalf("message content = %+v, want %q", m.Message, "Hello world")
	}
	if m.Message.ReasoningContent != "Let me think" {
		t.Errorf("reasoning = %q, want %q", m.Message.ReasoningContent, "Let me think")
	}
	if m.Message.ProviderMetadata == nil || m.Message.ProviderMetadata.Anthropic == nil || m.Message.ProviderMetadata.Anthropic.ThinkingSignature != "CAQSzw" {
		t.Errorf("thinking signature metadata = %+v, want CAQSzw", m.Message.ProviderMetadata)
	}
	if m.FinishReason != "stop" {
		t.Errorf("finish reason = %q, want %q", m.FinishReason, "stop")
	}
	if len(m.Message.ToolCalls) != 0 {
		t.Errorf("tool calls = %+v, want none", m.Message.ToolCalls)
	}

	results := claudeSubDecodedOfKind(events, claudeSubDecodeResult)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	r := results[0].Result
	if r == nil || r.Subtype != "success" || r.IsError {
		t.Fatalf("result = %+v, want a clean success", r)
	}
	if r.Text != "Hello world" || r.NumTurns != 1 || r.StopReason != "end_turn" {
		t.Errorf("result metadata = %+v", r)
	}
	if r.Usage == nil || r.Usage.PromptTokens != 4452 || r.Usage.CompletionTokens != 343 {
		t.Errorf("result usage = %+v, want prompt 4452 completion 343", r.Usage)
	}
}

func TestClaudeSubDecodeToolTurn(t *testing.T) {
	type observed struct{ id, name string }
	var seen []observed
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, observed{id, name}) },
	}
	events := claudeSubDecodeAll(t, hooks, claudeSubDecodeEvents(t, "turn_tool_use.jsonl"))

	tools := claudeSubDecodedOfKind(events, claudeSubDecodeToolUse)
	if len(tools) != 1 {
		t.Fatalf("tool observations = %d, want 1 (stream and echo must dedup)", len(tools))
	}
	if tools[0].ToolUseID != "toolu_1" || tools[0].ToolName != "read" {
		t.Errorf("observation = %+v, want id toolu_1 name read", tools[0])
	}
	if len(seen) != 1 || seen[0] != (observed{"toolu_1", "read"}) {
		t.Errorf("hook calls = %+v, want one toolu_1/read", seen)
	}

	messages := claudeSubDecodedOfKind(events, claudeSubDecodeMessage)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	m := messages[0].Message
	if m == nil || len(m.ToolCalls) != 1 {
		t.Fatalf("message tool calls = %+v, want one", m)
	}
	call := m.ToolCalls[0]
	if call.ID != "toolu_1" || call.Name != "read" {
		t.Errorf("tool call = %+v, want id toolu_1 name read (prefix stripped)", call)
	}
	if call.Arguments["path"] != "notes.txt" {
		t.Errorf("tool arguments = %+v, want path notes.txt", call.Arguments)
	}
	if messages[0].FinishReason != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls", messages[0].FinishReason)
	}
}

func TestClaudeSubDecodeEchoOpensToolUse(t *testing.T) {
	// An echo-only tool_use (the stream did not announce it) that arrives after
	// the stream's message_stop is observed once, translated, and accumulated so
	// the deferred final message carries its call.
	echo := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_9","name":"mcp__steiner__read","input":{"path":"a"}}]}}`
	var seen []string
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
	}
	d := newClaudeSubDecoder(hooks)
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	stop, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)})
	if err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if len(stop) != 0 {
		t.Fatalf("message_stop events = %+v, want none (the message is deferred)", stop)
	}
	got, err := d.decode(claudeSubEvent{Type: "assistant", Raw: json.RawMessage(echo)})
	if err != nil {
		t.Fatalf("decode echo: %v", err)
	}
	if len(got) != 2 || got[0].Kind != claudeSubDecodeToolUse || got[0].ToolUseID != "toolu_9" || got[0].ToolName != "read" {
		t.Fatalf("echo events = %+v, want one toolu_9/read observation then the message", got)
	}
	if len(seen) != 1 || seen[0] != "toolu_9:read" {
		t.Errorf("hook calls = %v, want [toolu_9:read]", seen)
	}
	if got[1].Kind != claudeSubDecodeMessage || got[1].Message == nil || len(got[1].Message.ToolCalls) != 1 {
		t.Fatalf("assembled message = %+v, want one tool call", got[1])
	}
	call := got[1].Message.ToolCalls[0]
	if call.ID != "toolu_9" || call.Name != "read" || call.Arguments["path"] != "a" {
		t.Errorf("tool call = %+v, want id toolu_9 name read path a", call)
	}
}

func TestClaudeSubDecodeEchoDeduplicatesStreamedTool(t *testing.T) {
	// An echo that repeats a streamed tool_use confirms it: no second
	// observation and no change to the accumulated input.
	start := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}}}`)}
	delta := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"notes.txt\"}"}}}`)}
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read","input":{"path":"overwritten"}}]}}`)}

	var seen []string
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
	}
	d := newClaudeSubDecoder(hooks)
	if _, err := d.decode(start); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if _, err := d.decode(delta); err != nil {
		t.Fatalf("decode delta: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	got, err := d.decode(echo)
	if err != nil {
		t.Fatalf("decode echo: %v", err)
	}
	if len(got) != 1 || got[0].Kind != claudeSubDecodeMessage {
		t.Fatalf("echo events = %+v, want the single deferred message", got)
	}
	if len(seen) != 1 || seen[0] != "toolu_1:read" {
		t.Errorf("hook calls = %v, want [toolu_1:read]", seen)
	}
	acc := d.toolUses[0]
	if acc == nil || acc.Input.String() != `{"path":"notes.txt"}` {
		t.Fatalf("accumulated input = %+v, want the streamed JSON unchanged", acc)
	}
}

func TestClaudeSubDecodeEchoConflictingNameFailsClosed(t *testing.T) {
	start := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}}}`)}
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__write","input":{"path":"a"}}]}}`)}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{ToolName: claudeSubStripPrefix})
	if _, err := d.decode(start); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if _, err := d.decode(echo); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("conflicting echo name error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
}

func TestClaudeSubDecodeEchoDuplicateIDsFailClosed(t *testing.T) {
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"},{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}]}}`)}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{ToolName: claudeSubStripPrefix})
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if _, err := d.decode(echo); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("duplicate echo id error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
}

func TestClaudeSubDecodeEchoIncompatibleIndexFailsClosed(t *testing.T) {
	// An echo-only tool_use that exposes an index the stream already owns cannot
	// silently alias that block.
	start := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}}}`)}
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_2","name":"mcp__steiner__read","index":0}]}}`)}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{ToolName: claudeSubStripPrefix})
	if _, err := d.decode(start); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if _, err := d.decode(echo); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("incompatible echo index error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
}

func TestClaudeSubDecodeEchoExplicitIndexCollidesWithStreamedText(t *testing.T) {
	// An echo-only index must conflict with a streamed block of any type, not
	// only a streamed tool_use.
	start := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`)}
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_2","name":"mcp__steiner__read","index":0}]}}`)}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{ToolName: claudeSubStripPrefix})
	if _, err := d.decode(start); err != nil {
		t.Fatalf("decode streamed text start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if _, err := d.decode(echo); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("echo index colliding with streamed text error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
}

func TestClaudeSubDecodeEchoOnlyExplicitIndexFinalizes(t *testing.T) {
	// The normal echo-only fallback still observes, translates and finalises its
	// call when the echo exposes a free index, even though it arrives after the
	// stream's message_stop.
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_9","name":"mcp__steiner__read","index":3,"input":{"path":"a"}}]}}`)}
	var seen []string
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
	}
	d := newClaudeSubDecoder(hooks)
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	got, err := d.decode(echo)
	if err != nil {
		t.Fatalf("decode echo: %v", err)
	}
	if len(got) != 2 || got[0].Kind != claudeSubDecodeToolUse || got[0].ToolUseID != "toolu_9" || got[0].ToolName != "read" {
		t.Fatalf("echo events = %+v, want one toolu_9/read observation then the message", got)
	}
	if len(seen) != 1 || seen[0] != "toolu_9:read" {
		t.Errorf("hook calls = %v, want [toolu_9:read]", seen)
	}
	if got[1].Kind != claudeSubDecodeMessage || got[1].Message == nil || len(got[1].Message.ToolCalls) != 1 {
		t.Fatalf("assembled message = %+v, want one tool call", got[1])
	}
	call := got[1].Message.ToolCalls[0]
	if call.ID != "toolu_9" || call.Name != "read" || call.Arguments["path"] != "a" {
		t.Errorf("tool call = %+v, want id toolu_9 name read path a", call)
	}
}

func TestClaudeSubDecodeEchoOnlyExplicitIndexGrantsNoStreamAuthority(t *testing.T) {
	// An echo-only tool use may expose a free index, but that must never
	// authorise a later streamed input_json_delta: only a streamed tool_use block
	// start can.
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_9","name":"mcp__steiner__read","index":3,"input":{"path":"a"}}]}}`)}
	delta := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"b\"}"}}}`)}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{ToolName: claudeSubStripPrefix})
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if _, err := d.decode(echo); err != nil {
		t.Fatalf("decode echo: %v", err)
	}
	if _, claimed := d.blocks[3]; claimed {
		t.Fatalf("echo-only index 3 claimed streamed block ownership: %v", d.blocks)
	}
	if _, err := d.decode(delta); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("delta after echo-only index error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
}

func TestClaudeSubDecodeDuplicateToolUseStart(t *testing.T) {
	start := func(index int, id string) claudeSubEvent {
		return claudeSubEvent{
			Type: "stream_event",
			Raw:  json.RawMessage(fmt.Sprintf(`{"event":{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":%q,"name":"mcp__steiner__read"}}}`, index, id)),
		}
	}
	partial := func(index int, partial string) claudeSubEvent {
		return claudeSubEvent{
			Type: "stream_event",
			Raw:  json.RawMessage(fmt.Sprintf(`{"event":{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%q}}}`, index, partial)),
		}
	}

	// A second tool_use start at the same index, after partial input, must fail
	// closed rather than overwrite the accumulated input.
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	if _, err := d.decode(start(0, "toolu_1")); err != nil {
		t.Fatalf("decode first start: %v", err)
	}
	if _, err := d.decode(partial(0, `{"path":`)); err != nil {
		t.Fatalf("decode partial input: %v", err)
	}
	if _, err := d.decode(start(0, "toolu_1")); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("duplicate start at same index error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}

	// The same non-empty id at a different index must fail closed.
	d = newClaudeSubDecoder(claudeSubDecodeHooks{})
	if _, err := d.decode(start(0, "toolu_1")); err != nil {
		t.Fatalf("decode first start: %v", err)
	}
	if _, err := d.decode(start(1, "toolu_1")); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("duplicate id at another index error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
}

func TestClaudeSubDecodeStreamBlockIndexIsUniqueAcrossTypes(t *testing.T) {
	start := func(index int, block string) claudeSubEvent {
		return claudeSubEvent{
			Type: "stream_event",
			Raw:  json.RawMessage(fmt.Sprintf(`{"event":{"type":"content_block_start","index":%d,"content_block":%s}}`, index, block)),
		}
	}
	tests := []struct {
		name   string
		first  claudeSubEvent
		second claudeSubEvent
	}{
		{"text then tool_use at the same index", start(0, `{"type":"text","text":""}`), start(0, `{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}`)},
		{"thinking then tool_use at the same index", start(0, `{"type":"thinking","thinking":""}`), start(0, `{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}`)},
		{"duplicate text starts at the same index", start(0, `{"type":"text","text":"a"}`), start(0, `{"type":"text","text":"b"}`)},
		{"duplicate thinking starts at the same index", start(0, `{"type":"thinking","thinking":"a"}`), start(0, `{"type":"thinking","thinking":"b"}`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newClaudeSubDecoder(claudeSubDecodeHooks{})
			if _, err := d.decode(tc.first); err != nil {
				t.Fatalf("decode first block start: %v", err)
			}
			if _, err := d.decode(tc.second); !errors.Is(err, errClaudeSubDecodeStream) {
				t.Fatalf("reused index error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
			}
		})
	}
}

func TestClaudeSubDecodeStreamStartCannotOverwriteEchoAccumulator(t *testing.T) {
	// An echo-only tool_use with an explicit index seeds an accumulator there.
	// A later streamed tool_use block start at the same index with a different
	// id and name must fail closed: it must not overwrite the echo id, name or
	// seeded input, must not observe a second call, and must not partially claim
	// the streamed block index.
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_echo","name":"mcp__steiner__read","index":3,"input":{"path":"a"}}]}}`)}
	start := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_stream","name":"mcp__steiner__write"}}}`)}

	var seen []string
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
	}
	d := newClaudeSubDecoder(hooks)
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if _, err := d.decode(echo); err != nil {
		t.Fatalf("decode echo: %v", err)
	}
	if len(seen) != 1 || seen[0] != "toolu_echo:read" {
		t.Fatalf("hook calls after echo = %v, want [toolu_echo:read]", seen)
	}
	if _, err := d.decode(start); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("streamed start over echo accumulator error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
	if len(seen) != 1 {
		t.Errorf("hook calls after rejected start = %v, want no second callback", seen)
	}
	acc := d.toolUses[3]
	if acc == nil || acc.ID != "toolu_echo" || acc.Name != "mcp__steiner__read" {
		t.Fatalf("accumulator after rejected start = %+v, want the echo identity intact", acc)
	}
	if acc.Input.String() != `{"path":"a"}` {
		t.Errorf("accumulator input after rejected start = %q, want the seeded echo input intact", acc.Input.String())
	}
	if _, claimed := d.blocks[3]; claimed {
		t.Errorf("rejected start claimed streamed block index 3: %v", d.blocks)
	}
}

func TestClaudeSubDecodeStreamStartCannotReclaimEchoIdentity(t *testing.T) {
	// Even when an echo and a later streamed start agree on id, name and index,
	// the streamed start fails closed: it must not re-observe the call or touch
	// the echo accumulator, and it must not claim streamed block ownership.
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read","index":3,"input":{"path":"a"}}]}}`)}
	start := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}}}`)}

	var seen []string
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
	}
	d := newClaudeSubDecoder(hooks)
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if _, err := d.decode(echo); err != nil {
		t.Fatalf("decode echo: %v", err)
	}
	if _, err := d.decode(start); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("streamed start reclaiming echo identity error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
	if len(seen) != 1 || seen[0] != "toolu_1:read" {
		t.Errorf("hook calls = %v, want one toolu_1:read", seen)
	}
	acc := d.toolUses[3]
	if acc == nil || acc.ID != "toolu_1" || acc.Name != "mcp__steiner__read" || acc.Input.String() != `{"path":"a"}` {
		t.Fatalf("accumulator after rejected start = %+v, want the echo call intact", acc)
	}
	if _, claimed := d.blocks[3]; claimed {
		t.Errorf("rejected start claimed streamed block index 3: %v", d.blocks)
	}
}

func TestClaudeSubDecodeBlockIndexResetsPerMessage(t *testing.T) {
	// Ownership is per message: a fresh message_start releases every index.
	start := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`)}
	begin := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	if _, err := d.decode(start); err != nil {
		t.Fatalf("decode first message block start: %v", err)
	}
	if _, err := d.decode(begin); err != nil {
		t.Fatalf("decode second message_start: %v", err)
	}
	if _, err := d.decode(start); err != nil {
		t.Fatalf("index 0 must be reusable in a new message: %v", err)
	}
}

func TestClaudeSubDecodeEchoEmptyToolUseIgnored(t *testing.T) {
	var seen []string
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
	}
	for _, echo := range []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"","name":"mcp__steiner__read"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_x","name":""}]}}`,
	} {
		// A fresh decoder per echo: one assistant message carries at most one
		// echo, and the echo is only valid after message_stop.
		d := newClaudeSubDecoder(hooks)
		if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}); err != nil {
			t.Fatalf("decode message_start: %v", err)
		}
		if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
			t.Fatalf("decode message_stop: %v", err)
		}
		got, err := d.decode(claudeSubEvent{Type: "assistant", Raw: json.RawMessage(echo)})
		if err != nil {
			t.Fatalf("decode echo: %v", err)
		}
		// The nameless entry is skipped, so the echo contributes no tool use;
		// the deferred message is still emitted exactly once.
		if len(got) != 1 || got[0].Kind != claudeSubDecodeMessage {
			t.Fatalf("echo %s = %+v, want the single deferred message", echo, got)
		}
		if len(got[0].Message.ToolCalls) != 0 {
			t.Errorf("echo %s produced tool calls = %+v, want none", echo, got[0].Message.ToolCalls)
		}
	}
	if len(seen) != 0 {
		t.Errorf("hook calls = %v, want none", seen)
	}
}

func TestClaudeSubDecodeThinkingDisabled(t *testing.T) {
	events := claudeSubDecodeAll(t, claudeSubDecodeHooks{}, claudeSubDecodeEvents(t, "turn_text_no_thinking.jsonl"))
	if got := claudeSubDecodedOfKind(events, claudeSubDecodeThinking); len(got) != 0 {
		t.Errorf("thinking events = %+v, want none", got)
	}
	messages := claudeSubDecodedOfKind(events, claudeSubDecodeMessage)
	if len(messages) != 1 || messages[0].Message.ReasoningContent != "" {
		t.Fatalf("message = %+v, want no reasoning content", messages)
	}
	if messages[0].Message.Content != "No thinking here" {
		t.Errorf("content = %q, want %q", messages[0].Message.Content, "No thinking here")
	}
}

func TestClaudeSubDecodeResultError(t *testing.T) {
	events := claudeSubDecodeAll(t, claudeSubDecodeHooks{}, claudeSubDecodeEvents(t, "result_error.jsonl"))
	results := claudeSubDecodedOfKind(events, claudeSubDecodeResult)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	r := results[0].Result
	if r == nil || !r.IsError || r.Subtype != "error_during_execution" {
		t.Fatalf("result = %+v, want an error_during_execution error", r)
	}
	if r.Text != "[Request interrupted by user for tool use]" {
		t.Errorf("result text = %q", r.Text)
	}
	// The result text must not be re-emitted as a delta; only the streamed text counts.
	if got := claudeSubJoinedText(events); got != "partial" {
		t.Errorf("joined text = %q, want %q", got, "partial")
	}
}

func TestClaudeSubDecodeUsageSplit(t *testing.T) {
	events := claudeSubDecodeAll(t, claudeSubDecodeHooks{}, claudeSubDecodeEvents(t, "turn_usage_split.jsonl"))

	usage := claudeSubDecodedOfKind(events, claudeSubDecodeUsage)
	if len(usage) != 2 {
		t.Fatalf("usage events = %d, want 2", len(usage))
	}
	first := usage[0].Usage
	if first == nil || first.PromptTokens != 109 || first.CompletionTokens != 0 || first.CacheCreationInputTokens != 100 || first.CacheReadInputTokens != 7 {
		t.Fatalf("message_start usage = %+v, want prompt 109 cache_creation 100 cache_read 7", first)
	}
	// message_delta reports input_tokens and output_tokens but omits the cache
	// fields; the omitted components and the prompt total must be retained.
	last := usage[1].Usage
	if last.PromptTokens != 109 || last.CacheCreationInputTokens != 100 || last.CacheReadInputTokens != 7 {
		t.Errorf("merged usage dropped an omitted input/cache field: %+v", last)
	}
	if last.CompletionTokens != 42 || last.TotalTokens != 151 {
		t.Errorf("merged usage = %+v, want completion 42 total 151", last)
	}
	if first.CompletionTokens != 0 || first.CacheReadInputTokens != 7 {
		t.Errorf("first usage event was mutated by a later merge: %+v", first)
	}

	results := claudeSubDecodedOfKind(events, claudeSubDecodeResult)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	// Turn-level result usage stays separate from the message's usage.
	if got := results[0].Result.Usage; got == nil || got.PromptTokens != 506 || got.CompletionTokens != 50 {
		t.Errorf("result usage = %+v, want prompt 506 completion 50", got)
	}
}

func TestClaudeSubDecodeIgnoresUnknownStreamEventType(t *testing.T) {
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	for _, inner := range []string{
		`{"event":{"type":"ping"}}`,
		`{"event":{"type":"a_future_event_type"}}`,
	} {
		got, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(inner)})
		if err != nil {
			t.Fatalf("decode %s: %v", inner, err)
		}
		if len(got) != 0 {
			t.Errorf("decode %s = %+v, want no events", inner, got)
		}
	}
}

func TestClaudeSubDecodeIgnoresIrrelevantEnvelopes(t *testing.T) {
	types := []string{"system", "user", "rate_limit_event", "control_response", "future_type"}
	for _, typ := range types {
		t.Run(typ, func(t *testing.T) {
			d := newClaudeSubDecoder(claudeSubDecodeHooks{})
			got, err := d.decode(claudeSubEvent{Type: typ, Raw: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatalf("decode %s: %v", typ, err)
			}
			if len(got) != 0 {
				t.Errorf("decode %s = %+v, want no events", typ, got)
			}
		})
	}
}

func TestClaudeSubDecodeFailClosed(t *testing.T) {
	tests := []struct {
		name string
		ev   claudeSubEvent
		want error
	}{
		{
			"malformed stream_event json",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"type":"stream_event","event":`)},
			errClaudeSubDecodeStream,
		},
		{
			"stream_event without event payload",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"type":"stream_event"}`)},
			errClaudeSubDecodeStream,
		},
		{
			"stream_event with malformed payload",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":`)},
			errClaudeSubDecodeStream,
		},
		{
			"null stream event payload",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":null}`)},
			errClaudeSubDecodeStream,
		},
		{
			"empty stream event payload",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"message_start without message",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start"}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"content_block_start without content_block",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"content_block_start with empty content_block",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{}}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"content_block_start without index",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","content_block":{"type":"text"}}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"content_block_start with negative index",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":-1,"content_block":{"type":"text"}}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"tool_use block without id",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"mcp__steiner__read"}}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"tool_use block without name",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1"}}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"tool_use block with empty id",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"","name":"mcp__steiner__read"}}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"content_block_delta without delta",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_delta","index":0}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"content_block_delta with empty delta",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_delta","index":0,"delta":{}}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"content_block_delta without index",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"x"}}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"content_block_stop without index",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_stop"}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"content_block_stop with negative index",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_stop","index":-1}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"message_delta without delta",
			claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_delta"}}`)},
			errClaudeSubDecodeStream,
		},
		{
			"malformed result json",
			claudeSubEvent{Type: "result", Raw: json.RawMessage(`not json`)},
			errClaudeSubResultEnvelope,
		},
		{
			"unsupported result subtype",
			claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"future_terminal","is_error":false}`)},
			errClaudeSubResultEnvelope,
		},
		{
			"empty result subtype",
			claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"is_error":false}`)},
			errClaudeSubResultEnvelope,
		},
		{
			"success marked is_error",
			claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":true}`)},
			errClaudeSubResultEnvelope,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newClaudeSubDecoder(claudeSubDecodeHooks{})
			_, err := d.decode(tc.ev)
			if err == nil {
				t.Fatalf("decode() error = nil, want %v", tc.want)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("decode() error = %v, want wrapping %v", err, tc.want)
			}
		})
	}
}

func TestClaudeSubDecodeToolInputBound(t *testing.T) {
	old := claudeSubDecodeMaxToolInputBytes
	claudeSubDecodeMaxToolInputBytes = 8
	t.Cleanup(func() { claudeSubDecodeMaxToolInputBytes = old })

	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	start := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}}}`)}
	if _, err := d.decode(start); err != nil {
		t.Fatalf("decode block start: %v", err)
	}
	delta := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\": \"a-very-long-path\"}"}}}`)}
	_, err := d.decode(delta)
	if !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("decode oversized input error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
}

func TestClaudeSubDecodeInputDeltaRequiresToolStart(t *testing.T) {
	delta := func(index int, partial string) claudeSubEvent {
		return claudeSubEvent{
			Type: "stream_event",
			Raw:  json.RawMessage(fmt.Sprintf(`{"event":{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%q}}}`, index, partial)),
		}
	}
	start := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}}}`)}

	// A delta with no preceding tool_use block start must fail closed.
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	if _, err := d.decode(delta(0, `{"path":"a"}`)); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("delta without tool start error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}

	// A delta at an index other than the observed tool_use block must fail closed.
	d = newClaudeSubDecoder(claudeSubDecodeHooks{})
	if _, err := d.decode(start); err != nil {
		t.Fatalf("decode tool start: %v", err)
	}
	if _, err := d.decode(delta(1, `{"path":"a"}`)); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("wrong-index delta error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}

	// The valid split-input path still accumulates into the final tool call.
	d = newClaudeSubDecoder(claudeSubDecodeHooks{})
	if _, err := d.decode(start); err != nil {
		t.Fatalf("decode tool start: %v", err)
	}
	if _, err := d.decode(delta(0, `{"path":`)); err != nil {
		t.Fatalf("decode split delta: %v", err)
	}
	if _, err := d.decode(delta(0, `"a"}`)); err != nil {
		t.Fatalf("decode split delta: %v", err)
	}
	got, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)})
	if err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("message_stop events = %+v, want none (the message is deferred)", got)
	}
	flush, err := d.decode(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":false}`)})
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(flush) != 2 || flush[0].Kind != claudeSubDecodeMessage || flush[0].Message == nil || len(flush[0].Message.ToolCalls) != 1 || flush[0].Message.ToolCalls[0].Arguments["path"] != "a" {
		t.Fatalf("flushed final message = %+v, want one tool call with path a", flush)
	}
}

func TestClaudeSubDecodeLongNameTranslation(t *testing.T) {
	const cliName = "mcp__steiner__abcdefghijklmnopqrstuvwxyz_12345678"
	const steinerName = "abcdefghijklmnopqrstuvwxyz-original"
	hooks := claudeSubDecodeHooks{ToolName: func(name string) string {
		if name == cliName {
			return steinerName
		}
		return name
	}}
	start := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_7","name":"` + cliName + `"}}}`)}
	stop := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}
	d := newClaudeSubDecoder(hooks)
	got, err := d.decode(start)
	if err != nil {
		t.Fatalf("decode block start: %v", err)
	}
	if len(got) != 1 || got[0].ToolName != steinerName {
		t.Fatalf("observation = %+v, want translated name %q", got, steinerName)
	}
	got, err = d.decode(stop)
	if err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("message_stop events = %+v, want none (the message is deferred)", got)
	}
	got, err = d.decode(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":false}`)})
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(got) != 2 || got[0].Message == nil || len(got[0].Message.ToolCalls) != 1 || got[0].Message.ToolCalls[0].Name != steinerName {
		t.Fatalf("flushed final message = %+v, want translated tool name %q", got, steinerName)
	}
}

func TestClaudeSubDecodeEchoOnlyFallbackAfterStop(t *testing.T) {
	// The real CLI order is message_stop, then the assistant echo, then result.
	// An echo-only tool use the stream never announced must reach the single
	// deferred final message, translated, with exactly one observation callback
	// and no duplicate text, usage or message.
	var seen []string
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
	}
	events := claudeSubDecodeAll(t, hooks, claudeSubDecodeEvents(t, "turn_echo_only_tool.jsonl"))

	if got := claudeSubJoinedText(events); got != "Let me look" {
		t.Errorf("joined text = %q, want %q", got, "Let me look")
	}
	tools := claudeSubDecodedOfKind(events, claudeSubDecodeToolUse)
	if len(tools) != 1 || tools[0].ToolUseID != "toolu_echo" || tools[0].ToolName != "read" {
		t.Fatalf("tool observations = %+v, want exactly one toolu_echo/read", tools)
	}
	if len(seen) != 1 || seen[0] != "toolu_echo:read" {
		t.Errorf("hook calls = %v, want exactly [toolu_echo:read]", seen)
	}
	if usage := claudeSubDecodedOfKind(events, claudeSubDecodeUsage); len(usage) != 2 {
		t.Errorf("usage events = %d, want 2 (the echo adds no usage)", len(usage))
	}
	messages := claudeSubDecodedOfKind(events, claudeSubDecodeMessage)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want exactly 1", len(messages))
	}
	m := messages[0].Message
	if m == nil || m.Content != "Let me look" || len(m.ToolCalls) != 1 {
		t.Fatalf("assembled message = %+v, want text plus one tool call", m)
	}
	call := m.ToolCalls[0]
	if call.ID != "toolu_echo" || call.Name != "read" || call.Arguments["path"] != "fallback.txt" {
		t.Errorf("tool call = %+v, want id toolu_echo name read path fallback.txt", call)
	}
	if messages[0].FinishReason != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls", messages[0].FinishReason)
	}
	toolAt := claudeSubKindIndex(events, claudeSubDecodeToolUse)
	messageAt := claudeSubKindIndex(events, claudeSubDecodeMessage)
	resultAt := claudeSubKindIndex(events, claudeSubDecodeResult)
	if !(toolAt < messageAt && messageAt < resultAt) {
		t.Errorf("event order tool=%d message=%d result=%d, want observation and message before the result", toolAt, messageAt, resultAt)
	}
}

func TestClaudeSubDecodeStreamEchoBlocks(t *testing.T) {
	var seen []string
	events := claudeSubDecodeAll(t, claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
	}, claudeSubDecodeEvents(t, "turn_stream_echo_blocks.jsonl"))
	if len(seen) != 1 || seen[0] != "toolu_stream_echo:read" {
		t.Fatalf("hook calls = %v, want one streamed tool observation", seen)
	}
	if thinking := claudeSubDecodedOfKind(events, claudeSubDecodeThinking); len(thinking) != 1 || thinking[0].Thinking != "Think first" {
		t.Fatalf("thinking events = %+v, want one thinking delta", thinking)
	}
	messages := claudeSubDecodedOfKind(events, claudeSubDecodeMessage)
	if len(messages) != 1 || messages[0].Message == nil || len(messages[0].Message.ToolCalls) != 1 {
		t.Fatalf("messages = %+v, want one assembled tool message", messages)
	}
	if messages[0].Message.ToolCalls[0].ID != "toolu_stream_echo" || messages[0].Message.ToolCalls[0].Arguments["path"] != "stream.txt" {
		t.Fatalf("tool call = %+v, want streamed arguments", messages[0].Message.ToolCalls[0])
	}
	messageAt := claudeSubKindIndex(events, claudeSubDecodeMessage)
	resultAt := claudeSubKindIndex(events, claudeSubDecodeResult)
	if messageAt < 0 || resultAt < 0 || messageAt > resultAt {
		t.Fatalf("message/result order = %d/%d, want message before result", messageAt, resultAt)
	}
}

func TestClaudeSubDecodeInStreamEchoThenPostStopConfirmation(t *testing.T) {
	stream := func(inner string) claudeSubEvent {
		return claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":` + inner + `}`)}
	}
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_confirm","name":"mcp__steiner__read","input":{"path":"confirmed.txt"}}]}}`)}
	var seen []string
	d := newClaudeSubDecoder(claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
	})
	var all []claudeSubDecoded
	for _, ev := range []claudeSubEvent{
		stream(`{"type":"message_start","message":{"usage":{}}}`),
		stream(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_confirm","name":"mcp__steiner__read"}}`),
		echo,
		stream(`{"type":"content_block_stop","index":0}`),
		stream(`{"type":"message_stop"}`),
		echo,
		{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":false}`)},
	} {
		got, err := d.decode(ev)
		if err != nil {
			t.Fatalf("decode %s: %v", ev.Type, err)
		}
		all = append(all, got...)
	}
	messages := claudeSubDecodedOfKind(all, claudeSubDecodeMessage)
	if len(messages) != 1 || messages[0].Message == nil || len(messages[0].Message.ToolCalls) != 1 {
		t.Fatalf("messages = %+v, want exactly one message with one tool call", messages)
	}
	if seenCount := len(claudeSubDecodedOfKind(all, claudeSubDecodeToolUse)); seenCount != 1 || len(seen) != 1 || seen[0] != "toolu_confirm:read" {
		t.Fatalf("tool observations = %d, hooks = %v, want exactly one toolu_confirm/read", seenCount, seen)
	}
}

func TestClaudeSubDecodeNoEchoFlushesAtResult(t *testing.T) {
	// A turn whose stream stopped without an assistant echo must still emit its
	// final assistant message, flushed immediately before the terminal result.
	events := claudeSubDecodeAll(t, claudeSubDecodeHooks{}, claudeSubDecodeEvents(t, "turn_no_echo.jsonl"))

	messages := claudeSubDecodedOfKind(events, claudeSubDecodeMessage)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want exactly 1 flushed at the result", len(messages))
	}
	if messages[0].Message == nil || messages[0].Message.Content != "No echo turn" {
		t.Fatalf("flushed message = %+v, want %q", messages[0].Message, "No echo turn")
	}
	if messages[0].FinishReason != "stop" {
		t.Errorf("finish reason = %q, want stop", messages[0].FinishReason)
	}
	messageAt := claudeSubKindIndex(events, claudeSubDecodeMessage)
	resultAt := claudeSubKindIndex(events, claudeSubDecodeResult)
	if !(messageAt >= 0 && resultAt >= 0 && messageAt < resultAt) {
		t.Errorf("event order message=%d result=%d, want the message flushed before the result", messageAt, resultAt)
	}
}

func TestClaudeSubDecodeErrorResultFlushesPendingMessage(t *testing.T) {
	// A recognised terminal error result must flush the held final message
	// before itself, so 6c is never left without the turn's assistant output.
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	stream := func(inner string) claudeSubEvent {
		return claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":` + inner + `}`)}
	}
	for _, inner := range []string{
		`{"type":"message_start","message":{"usage":{}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		`{"type":"message_stop"}`,
	} {
		if _, err := d.decode(stream(inner)); err != nil {
			t.Fatalf("decode %s: %v", inner, err)
		}
	}
	got, err := d.decode(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"error_during_execution","is_error":true,"result":"boom"}`)})
	if err != nil {
		t.Fatalf("decode error result: %v", err)
	}
	if len(got) != 2 || got[0].Kind != claudeSubDecodeMessage || got[0].Message == nil || got[0].Message.Content != "partial" {
		t.Fatalf("events = %+v, want the pending message flushed before the error result", got)
	}
	if got[1].Kind != claudeSubDecodeResult || got[1].Result == nil || !got[1].Result.IsError {
		t.Fatalf("second event = %+v, want the error result", got[1])
	}
}

func TestClaudeSubDecodeInvalidEchoAfterStopEmitsNoMessage(t *testing.T) {
	// A conflicting echo after message_stop must fail closed without emitting a
	// misleading final message in the same decode call.
	start := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}}}`)}
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__write","input":{"path":"a"}}]}}`)}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{ToolName: claudeSubStripPrefix})
	if _, err := d.decode(start); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	got, err := d.decode(echo)
	if !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("conflicting echo error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
	if len(got) != 0 {
		t.Fatalf("conflicting echo events = %+v, want none", got)
	}
}

func TestClaudeSubDecodeRepeatedPerBlockEchoIsIdempotent(t *testing.T) {
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read","input":{"path":"a"}}]}}`)}
	var seen []string
	d := newClaudeSubDecoder(claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
	})
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	start := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}}}`)}
	if _, err := d.decode(start); err != nil {
		t.Fatalf("decode tool start: %v", err)
	}
	if _, err := d.decode(echo); err != nil {
		t.Fatalf("decode first echo: %v", err)
	}
	if _, err := d.decode(echo); err != nil {
		t.Fatalf("decode repeated echo: %v", err)
	}
	if len(seen) != 1 || seen[0] != "toolu_1:read" {
		t.Fatalf("hook calls = %v, want one toolu_1/read observation", seen)
	}
	got, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)})
	if err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if len(got) != 1 || got[0].Kind != claudeSubDecodeMessage || got[0].Message == nil || len(got[0].Message.ToolCalls) != 1 {
		t.Fatalf("message_stop events = %+v, want one assembled message with one tool call", got)
	}
}

func TestClaudeSubDecodeMessageStartWhilePendingFailsClosed(t *testing.T) {
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	begin := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}
	if _, err := d.decode(begin); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if _, err := d.decode(begin); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("message_start while pending error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
}

func TestClaudeSubDecodeDuplicateMessageStopFailsClosed(t *testing.T) {
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	stop := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}
	if _, err := d.decode(stop); err != nil {
		t.Fatalf("decode first message_stop: %v", err)
	}
	if _, err := d.decode(stop); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("second message_stop error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
}

func TestClaudeSubDecodeResultAfterEchoEmitsOneMessage(t *testing.T) {
	// A result that follows the echo must not emit a second final message.
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	echo, err := d.decode(claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`)})
	if err != nil {
		t.Fatalf("decode echo: %v", err)
	}
	if len(echo) != 1 || echo[0].Kind != claudeSubDecodeMessage {
		t.Fatalf("echo events = %+v, want the single deferred message", echo)
	}
	got, err := d.decode(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":false}`)})
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(got) != 1 || got[0].Kind != claudeSubDecodeResult {
		t.Fatalf("result events = %+v, want only the result", got)
	}
}

func TestClaudeSubDecodePreservesToolObservationOrder(t *testing.T) {
	// Two streamed tool uses followed by an echo-only fallback must finalize in
	// the order they were observed: stream1, stream2, then the echo fallback.
	// The fallback's negative synthetic accumulator key must not sort ahead of
	// the streamed calls that preceded it.
	stream := func(inner string) claudeSubEvent {
		return claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":` + inner + `}`)}
	}
	var seen []string
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id) },
	}
	d := newClaudeSubDecoder(hooks)
	for _, inner := range []string{
		`{"type":"message_start","message":{"usage":{}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_2","name":"mcp__steiner__write"}}`,
		`{"type":"message_stop"}`,
	} {
		if _, err := d.decode(stream(inner)); err != nil {
			t.Fatalf("decode %s: %v", inner, err)
		}
	}
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_echo","name":"mcp__steiner__read","input":{"path":"fallback"}}]}}`)}
	got, err := d.decode(echo)
	if err != nil {
		t.Fatalf("decode echo: %v", err)
	}
	messages := claudeSubDecodedOfKind(got, claudeSubDecodeMessage)
	if len(messages) != 1 || messages[0].Message == nil {
		t.Fatalf("echo events = %+v, want one assembled message", got)
	}
	calls := messages[0].Message.ToolCalls
	if len(calls) != 3 {
		t.Fatalf("tool calls = %+v, want 3", calls)
	}
	gotIDs := make([]string, len(calls))
	for i := range calls {
		gotIDs[i] = calls[i].ID
	}
	wantIDs := []string{"toolu_1", "toolu_2", "toolu_echo"}
	for i, want := range wantIDs {
		if gotIDs[i] != want {
			t.Errorf("tool call order = %v, want %v", gotIDs, wantIDs)
			break
		}
	}
	if len(seen) != 3 || seen[0] != "toolu_1" || seen[1] != "toolu_2" || seen[2] != "toolu_echo" {
		t.Errorf("observation order = %v, want [toolu_1 toolu_2 toolu_echo]", seen)
	}
}

func TestClaudeSubDecodeRejectsStreamEventsAfterStop(t *testing.T) {
	// Once message_stop has been seen, every streamed event that could mutate
	// the stopped message's content or invoke a tool callback must fail closed
	// with no event and no hook call. Only a new message_start and the assistant
	// echo remain valid before the terminal result.
	stream := func(inner string) claudeSubEvent {
		return claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":` + inner + `}`)}
	}
	tests := []struct {
		name  string
		inner string
	}{
		{"text delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"late"}}`},
		{"tool start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_9","name":"mcp__steiner__read"}}`},
		{"block stop", `{"type":"content_block_stop","index":0}`},
		{"message delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`},
		{"duplicate stop", `{"type":"message_stop"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var seen []string
			hooks := claudeSubDecodeHooks{
				ToolName:       claudeSubStripPrefix,
				ObserveToolUse: func(id, name string) { seen = append(seen, id) },
			}
			d := newClaudeSubDecoder(hooks)
			for _, inner := range []string{
				`{"type":"message_start","message":{"usage":{}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"message_stop"}`,
			} {
				if _, err := d.decode(stream(inner)); err != nil {
					t.Fatalf("decode %s: %v", inner, err)
				}
			}
			got, err := d.decode(stream(tc.inner))
			if !errors.Is(err, errClaudeSubDecodeStream) {
				t.Fatalf("decode %s error = %v, want wrapping %v", tc.inner, err, errClaudeSubDecodeStream)
			}
			if len(got) != 0 {
				t.Errorf("decode %s events = %+v, want none", tc.inner, got)
			}
			if len(seen) != 0 {
				t.Errorf("hook calls = %v, want none after message_stop", seen)
			}
		})
	}
}

func TestClaudeSubDecodeRejectsEchoAfterResult(t *testing.T) {
	// A terminal result closes the turn: an assistant echo that follows must
	// fail closed without invoking the tool-use hook.
	var seen []string
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id) },
	}
	d := newClaudeSubDecoder(hooks)
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":false}`)}); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_late","name":"mcp__steiner__read","input":{"path":"a"}}]}}`)}
	got, err := d.decode(echo)
	if !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("echo after result error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
	if len(got) != 0 {
		t.Errorf("echo after result events = %+v, want none", got)
	}
	if len(seen) != 0 {
		t.Errorf("hook calls = %v, want none after the terminal result", seen)
	}
}

func TestClaudeSubDecodeRejectsStreamAfterResult(t *testing.T) {
	// A streamed tool_use that follows the terminal result must fail closed with
	// no event and no hook call; a second result is a duplicate terminal event.
	var seen []string
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id) },
	}
	d := newClaudeSubDecoder(hooks)
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":false}`)}); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	late := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_late","name":"mcp__steiner__read"}}}`)}
	got, err := d.decode(late)
	if !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("stream tool after result error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
	if len(got) != 0 {
		t.Errorf("stream tool after result events = %+v, want none", got)
	}
	if len(seen) != 0 {
		t.Errorf("hook calls = %v, want none after the terminal result", seen)
	}
	if _, err := d.decode(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":false}`)}); !errors.Is(err, errClaudeSubResultEnvelope) {
		t.Fatalf("duplicate result error = %v, want wrapping %v", err, errClaudeSubResultEnvelope)
	}
	// Unrelated top-level and unknown stream envelopes stay ignored in every
	// phase, including after the terminal result.
	for _, ev := range []claudeSubEvent{
		{Type: "user", Raw: json.RawMessage(`{}`)},
		{Type: "system", Raw: json.RawMessage(`{}`)},
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"ping"}}`)},
	} {
		got, err := d.decode(ev)
		if err != nil || len(got) != 0 {
			t.Errorf("decode %s after result = %+v, %v; want no events and no error", ev.Type, got, err)
		}
	}
}

func TestClaudeSubDecodeRejectsMessageStartAfterStop(t *testing.T) {
	// A message_start after message_stop must fail closed without resetting the
	// stopped message's content or dropping its pending assistant output.
	stream := func(inner string) claudeSubEvent {
		return claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":` + inner + `}`)}
	}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	for _, inner := range []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"kept"}}`,
		`{"type":"message_stop"}`,
	} {
		if _, err := d.decode(stream(inner)); err != nil {
			t.Fatalf("decode %s: %v", inner, err)
		}
	}
	got, err := d.decode(stream(`{"type":"message_start","message":{"usage":{"input_tokens":9999}}}`))
	if !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("message_start after stop error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
	if len(got) != 0 {
		t.Fatalf("message_start after stop events = %+v, want none", got)
	}
	if d.content.String() != "kept" {
		t.Errorf("content after rejected message_start = %q, want %q", d.content.String(), "kept")
	}
	if d.phase != claudeSubPhaseStopped {
		t.Errorf("phase after rejected message_start = %v, want stopped", d.phase)
	}
	if !d.messagePending {
		t.Errorf("pending flag cleared by rejected message_start")
	}
	if d.usage == nil || d.usage.InputTokens == nil || *d.usage.InputTokens != 5 {
		t.Errorf("usage after rejected message_start = %+v, want the stopped message usage retained", d.usage)
	}
}

func TestClaudeSubDecodeRejectsResultBeforeStop(t *testing.T) {
	// A result in the streaming phase must fail closed without closing the turn
	// or stranding the still-streaming message. Once message_stop arrives, the
	// same message still flushes at the result.
	stream := func(inner string) claudeSubEvent {
		return claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":` + inner + `}`)}
	}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	for _, inner := range []string{
		`{"type":"message_start","message":{"usage":{}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
	} {
		if _, err := d.decode(stream(inner)); err != nil {
			t.Fatalf("decode %s: %v", inner, err)
		}
	}
	got, err := d.decode(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":false}`)})
	if !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("result before stop error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
	if len(got) != 0 {
		t.Fatalf("result before stop events = %+v, want none", got)
	}
	if d.phase != claudeSubPhaseStreaming {
		t.Fatalf("phase after rejected result = %v, want streaming", d.phase)
	}
	if _, err := d.decode(stream(`{"type":"message_stop"}`)); err != nil {
		t.Fatalf("decode message_stop: %v", err)
	}
	got, err = d.decode(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":false}`)})
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(got) != 2 || got[0].Kind != claudeSubDecodeMessage || got[0].Message == nil || got[0].Message.Content != "partial" {
		t.Fatalf("events after stop = %+v, want the preserved message then the result", got)
	}
	if got[1].Kind != claudeSubDecodeResult {
		t.Errorf("second event = %+v, want the terminal result", got[1])
	}
}

func TestClaudeSubDecodeRejectsUnclaimedOrMismatchedBlockEvents(t *testing.T) {
	stream := func(inner string) claudeSubEvent {
		return claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":` + inner + `}`)}
	}
	tests := []struct {
		name  string
		prior []string
		inner string
	}{
		{
			name:  "text delta with no block start",
			inner: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`,
		},
		{
			name:  "thinking delta on a text block",
			prior: []string{`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			inner: `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"x"}}`,
		},
		{
			name:  "text delta on a tool_use block",
			prior: []string{`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}}`},
			inner: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`,
		},
		{
			name:  "input json delta on a text block",
			prior: []string{`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			inner: `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		},
		{
			name:  "delta for a different index than the open block",
			prior: []string{`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			inner: `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"x"}}`,
		},
		{
			name:  "stop with no block start",
			inner: `{"type":"content_block_stop","index":0}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newClaudeSubDecoder(claudeSubDecodeHooks{})
			if _, err := d.decode(stream(`{"type":"message_start","message":{"usage":{}}}`)); err != nil {
				t.Fatalf("decode message_start: %v", err)
			}
			for _, prior := range tc.prior {
				if _, err := d.decode(stream(prior)); err != nil {
					t.Fatalf("decode prior %s: %v", prior, err)
				}
			}
			got, err := d.decode(stream(tc.inner))
			if !errors.Is(err, errClaudeSubDecodeStream) {
				t.Fatalf("decode %s error = %v, want wrapping %v", tc.inner, err, errClaudeSubDecodeStream)
			}
			if len(got) != 0 {
				t.Errorf("decode %s events = %+v, want none", tc.inner, got)
			}
		})
	}
}

func TestClaudeSubDecodeRejectsBlockLifecycleViolations(t *testing.T) {
	// A stopped block rejects a repeated stop and any later delta.
	stream := func(inner string) claudeSubEvent {
		return claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":` + inner + `}`)}
	}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	for _, inner := range []string{
		`{"type":"message_start","message":{"usage":{}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_stop","index":0}`,
	} {
		if _, err := d.decode(stream(inner)); err != nil {
			t.Fatalf("decode %s: %v", inner, err)
		}
	}
	dup, err := d.decode(stream(`{"type":"content_block_stop","index":0}`))
	if !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("duplicate stop error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
	if len(dup) != 0 {
		t.Errorf("duplicate stop events = %+v, want none", dup)
	}
	late, err := d.decode(stream(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"late"}}`))
	if !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("delta after stop error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
	if len(late) != 0 {
		t.Errorf("delta after stop events = %+v, want none", late)
	}
}

func TestClaudeSubDecodeRejectsDuplicateMessageStart(t *testing.T) {
	// A second message_start must fail closed without resetting the in-flight
	// message's content or usage, and without emitting events.
	stream := func(inner string) claudeSubEvent {
		return claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":` + inner + `}`)}
	}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	for _, inner := range []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"keep"}}`,
	} {
		if _, err := d.decode(stream(inner)); err != nil {
			t.Fatalf("decode %s: %v", inner, err)
		}
	}
	got, err := d.decode(stream(`{"type":"message_start","message":{"usage":{"input_tokens":9999}}}`))
	if !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("duplicate message_start error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
	if len(got) != 0 {
		t.Fatalf("duplicate message_start events = %+v, want none", got)
	}
	if d.content.String() != "keep" {
		t.Errorf("content after duplicate message_start = %q, want %q", d.content.String(), "keep")
	}
	if d.usage == nil || d.usage.InputTokens == nil || *d.usage.InputTokens != 5 {
		t.Errorf("usage after duplicate message_start = %+v, want the first usage retained", d.usage)
	}
	got, err = d.decode(stream(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"!"}}`))
	if err != nil || len(got) != 1 {
		t.Fatalf("delta after duplicate message_start = %+v, %v; want one text delta", got, err)
	}
	if d.content.String() != "keep!" {
		t.Errorf("content after later delta = %q, want %q", d.content.String(), "keep!")
	}
}

func TestClaudeSubDecodeAcceptsZeroLengthOptionalData(t *testing.T) {
	// Empty initial content and empty deltas on a correctly owned open block are
	// valid and must not be rejected.
	stream := func(inner string) claudeSubEvent {
		return claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":` + inner + `}`)}
	}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	if _, err := d.decode(stream(`{"type":"message_start","message":{"usage":{}}}`)); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	for _, inner := range []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":""}}`,
		`{"type":"content_block_stop","index":1}`,
	} {
		got, err := d.decode(stream(inner))
		if err != nil {
			t.Fatalf("decode %s: %v", inner, err)
		}
		if len(got) != 0 {
			t.Errorf("decode %s = %+v, want no events for zero-length data", inner, got)
		}
	}
}

func TestClaudeSubDecodeEchoBeforeStreamToolStartRejectsWithoutState(t *testing.T) {
	// An echo for a tool use the stream has not announced must fail closed while
	// streaming without observing a tool use, recording the echo, or touching any
	// accumulator, order or pending-message state.
	var seen []string
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
	}
	d := newClaudeSubDecoder(hooks)
	if _, err := d.decode(claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}); err != nil {
		t.Fatalf("decode message_start: %v", err)
	}
	echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read","input":{"path":"a"}}]}}`)}
	got, err := d.decode(echo)
	if !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("echo before stop error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
	if len(got) != 0 {
		t.Errorf("echo before stop events = %+v, want none", got)
	}
	if len(seen) != 0 {
		t.Errorf("hook calls = %v, want none", seen)
	}
	if d.echoProcessed || d.streamEchoProcessed {
		t.Errorf("rejected echo advanced echo state: processed=%v streamProcessed=%v", d.echoProcessed, d.streamEchoProcessed)
	}
	if d.sawToolUse || len(d.toolUses) != 0 || len(d.toolOrder) != 0 {
		t.Errorf("rejected echo mutated tool state: sawToolUse=%v toolUses=%v order=%v", d.sawToolUse, d.toolUses, d.toolOrder)
	}
	if d.messagePending {
		t.Errorf("rejected echo set messagePending")
	}
	if _, registered := d.observed["toolu_1"]; registered {
		t.Errorf("rejected echo registered an observation: %v", d.observed)
	}
}

func TestClaudeSubDecodeEchoIsTransactional(t *testing.T) {
	// A multi-block echo must be validated as a whole: a later block's error
	// must leave no accumulator, tool order, echo state or callback from an
	// earlier valid block.
	begin := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)}
	stop := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)}

	newStoppedDecoder := func(t *testing.T, hooks claudeSubDecodeHooks) *claudeSubDecoder {
		t.Helper()
		d := newClaudeSubDecoder(hooks)
		if _, err := d.decode(begin); err != nil {
			t.Fatalf("decode message_start: %v", err)
		}
		if _, err := d.decode(stop); err != nil {
			t.Fatalf("decode message_stop: %v", err)
		}
		return d
	}

	t.Run("later block repeats id", func(t *testing.T) {
		echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"},{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}]}}`)}
		var seen []string
		d := newStoppedDecoder(t, claudeSubDecodeHooks{
			ToolName:       claudeSubStripPrefix,
			ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
		})
		got, err := d.decode(echo)
		if !errors.Is(err, errClaudeSubDecodeStream) {
			t.Fatalf("conflicting multi-block echo error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
		}
		if len(got) != 0 {
			t.Errorf("events = %+v, want none", got)
		}
		if len(seen) != 0 {
			t.Errorf("hook calls = %v, want none", seen)
		}
		if d.echoProcessed || d.streamEchoProcessed {
			t.Errorf("rejected echo advanced echo state: processed=%v streamProcessed=%v", d.echoProcessed, d.streamEchoProcessed)
		}
		if d.sawToolUse || len(d.toolUses) != 0 || len(d.toolOrder) != 0 {
			t.Errorf("rejected echo committed tool state: sawToolUse=%v toolUses=%v order=%v", d.sawToolUse, d.toolUses, d.toolOrder)
		}
		if !d.messagePending {
			t.Errorf("rejected echo discarded the pending message")
		}
	})

	t.Run("later block oversized", func(t *testing.T) {
		old := claudeSubDecodeMaxToolInputBytes
		claudeSubDecodeMaxToolInputBytes = 8
		t.Cleanup(func() { claudeSubDecodeMaxToolInputBytes = old })

		echo := claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"},{"type":"tool_use","id":"toolu_2","name":"mcp__steiner__read","input":{"path":"a-very-long-path"}}]}}`)}
		var seen []string
		d := newStoppedDecoder(t, claudeSubDecodeHooks{
			ToolName:       claudeSubStripPrefix,
			ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
		})
		got, err := d.decode(echo)
		if !errors.Is(err, errClaudeSubDecodeStream) {
			t.Fatalf("oversized multi-block echo error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
		}
		if len(got) != 0 {
			t.Errorf("events = %+v, want none", got)
		}
		if len(seen) != 0 {
			t.Errorf("hook calls = %v, want none", seen)
		}
		if d.echoProcessed || d.streamEchoProcessed {
			t.Errorf("rejected echo advanced echo state: processed=%v streamProcessed=%v", d.echoProcessed, d.streamEchoProcessed)
		}
		if d.sawToolUse || len(d.toolUses) != 0 || len(d.toolOrder) != 0 {
			t.Errorf("rejected echo committed tool state: sawToolUse=%v toolUses=%v order=%v", d.sawToolUse, d.toolUses, d.toolOrder)
		}
	})
}

func TestClaudeSubDecodeFailedAssemblyPreservesPending(t *testing.T) {
	// A malformed (incomplete) accumulated tool input makes final assembly fail
	// at the flush. The flush must not clear the pending flag or advance the
	// phase, and the terminal result must not be emitted, so the turn's message
	// is preserved and recoverable rather than silently lost.
	stream := func(inner string) claudeSubEvent {
		return claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":` + inner + `}`)}
	}
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	for _, inner := range []string{
		`{"type":"message_start","message":{"usage":{}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"kept"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_stop"}`,
	} {
		if _, err := d.decode(stream(inner)); err != nil {
			t.Fatalf("decode %s: %v", inner, err)
		}
	}
	if !d.messagePending {
		t.Fatalf("message_pending false before flush")
	}
	got, err := d.decode(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":false}`)})
	if err == nil {
		t.Fatalf("decode result over malformed tool input error = nil, want an assembly failure")
	}
	if len(got) != 0 {
		t.Errorf("events after failed flush = %+v, want none", got)
	}
	if !d.messagePending {
		t.Errorf("message_pending cleared by a failed assembly")
	}
	if d.phase != claudeSubPhaseStopped {
		t.Errorf("phase after failed flush = %v, want stopped", d.phase)
	}
	if d.content.String() != "kept" {
		t.Errorf("content after failed flush = %q, want %q", d.content.String(), "kept")
	}
}

func TestClaudeSubDecodeOversizedStartLeavesNoState(t *testing.T) {
	// An oversized initial tool input must be rejected before the stream claims
	// the block, creates an accumulator, records order, sets sawToolUse or
	// invokes the observation hook, so a subsequent valid start at the same
	// index succeeds cleanly.
	old := claudeSubDecodeMaxToolInputBytes
	claudeSubDecodeMaxToolInputBytes = 8
	t.Cleanup(func() { claudeSubDecodeMaxToolInputBytes = old })

	var seen []string
	hooks := claudeSubDecodeHooks{
		ToolName:       claudeSubStripPrefix,
		ObserveToolUse: func(id, name string) { seen = append(seen, id+":"+name) },
	}
	d := newClaudeSubDecoder(hooks)
	oversized := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read","input":{"path":"a-very-long-path"}}}}`)}
	got, err := d.decode(oversized)
	if !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("oversized start error = %v, want wrapping %v", err, errClaudeSubDecodeStream)
	}
	if len(got) != 0 {
		t.Errorf("oversized start events = %+v, want none", got)
	}
	if len(seen) != 0 {
		t.Errorf("hook calls = %v, want none", seen)
	}
	if _, claimed := d.blocks[0]; claimed {
		t.Errorf("oversized start claimed block index 0: %v", d.blocks)
	}
	if _, exists := d.toolUses[0]; exists {
		t.Errorf("oversized start created an accumulator: %v", d.toolUses)
	}
	if d.sawToolUse || len(d.toolOrder) != 0 {
		t.Errorf("oversized start recorded order/sawToolUse: order=%v sawToolUse=%v", d.toolOrder, d.sawToolUse)
	}
	if _, registered := d.observed["toolu_1"]; registered {
		t.Errorf("oversized start registered an observation: %v", d.observed)
	}

	valid := claudeSubEvent{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__steiner__read","input":{}}}}`)}
	got, err = d.decode(valid)
	if err != nil {
		t.Fatalf("decode valid start after rejected oversized start: %v", err)
	}
	if len(got) != 1 || got[0].Kind != claudeSubDecodeToolUse || got[0].ToolUseID != "toolu_1" {
		t.Fatalf("valid start events = %+v, want one toolu_1 observation", got)
	}
	if len(seen) != 1 || seen[0] != "toolu_1:read" {
		t.Errorf("hook calls = %v, want [toolu_1:read]", seen)
	}
	if owner, claimed := d.blocks[0]; !claimed || owner != "tool_use" {
		t.Errorf("valid start did not claim block index 0: %v", d.blocks)
	}
	acc := d.toolUses[0]
	if acc == nil || acc.ID != "toolu_1" || acc.Name != "mcp__steiner__read" {
		t.Errorf("valid start accumulator = %+v, want the streamed identity", acc)
	}
}
