package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func codexSSE(frames ...string) string {
	var b strings.Builder
	for _, frame := range frames {
		b.WriteString("data: ")
		b.WriteString(frame)
		b.WriteString("\n\n")
	}
	return b.String()
}

func TestCodexStreamItemPhaseAndOrderedRecovery(t *testing.T) {
	tests := []struct {
		name    string
		frames  []string
		content string
		phases  []string
		calls   int
		deltas  []string
	}{
		{"late identity, phase, multiple parts", []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"message"}}`,
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"one"}`,
			`{"type":"response.output_text.delta","output_index":0,"content_index":1,"delta":"two"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"onetwo"}]}}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"onetwo"}]}]}}`, `[DONE]`}, "onetwo", []string{"commentary"}, 0, []string{"one", "two"}},
		{"commentary-call-final", []string{
			`{"type":"response.output_text.delta","item_id":"m1","output_index":0,"delta":"note"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"note"}]}}`,
			`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"c1","name":"tool","arguments":"{}"}}`,
			`{"type":"response.output_item.done","output_index":2,"item":{"type":"message","id":"m2","phase":"final","content":[{"type":"output_text","text":"answer"}]}}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"note"}]},{"type":"function_call","call_id":"c1","name":"tool","arguments":"{}"},{"type":"message","id":"m2","phase":"final","content":[{"type":"output_text","text":"answer"}]}]}}`, `[DONE]`}, "noteanswer", []string{"commentary", "", "final"}, 1, []string{"note", "answer"}},
		{"completed earlier item and suffix", []string{
			`{"type":"response.output_text.delta","item_id":"m2","output_index":1,"delta":"answer"}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"earlier"}]},{"type":"message","id":"m2","phase":"final","content":[{"type":"output_text","text":"answer plus"}]}]}}`, `[DONE]`}, "earlieranswer plus", []string{"commentary", "final"}, 0, []string{"answer"}},
		{"terminal authoritative non-prefix content", []string{
			`{"type":"response.output_text.delta","item_id":"m1","delta":"AB"}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","id":"m1","content":[{"type":"output_text","text":"XAB"}]}]}}`, `[DONE]`}, "XAB", nil, 0, []string{"AB"}},
		{"anonymous message deltas then item done", []string{
			`{"type":"response.output_text.delta","delta":"one"}`,
			`{"type":"response.output_text.delta","delta":"two"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","phase":"final","content":[{"type":"output_text","text":"onetwo"}]}}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","id":"m1","phase":"final","content":[{"type":"output_text","text":"onetwo"}]}]}}`, `[DONE]`}, "onetwo", []string{"final"}, 0, []string{"one", "two"}},
		{"anonymous delta then late index and identified next", []string{
			`{"type":"response.output_text.delta","delta":"one"}`,
			`{"type":"response.output_text.delta","output_index":0,"delta":"+"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"one+"}]}}`,
			`{"type":"response.output_text.delta","item_id":"m2","output_index":1,"delta":"two"}`,
			`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"m2","phase":"final","content":[{"type":"output_text","text":"two"}]}}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"one+"}]},{"type":"message","id":"m2","phase":"final","content":[{"type":"output_text","text":"two"}]}]}}`, `[DONE]`}, "one+two", []string{"commentary", "final"}, 0, []string{"one", "+", "two"}},
		{"added phase persists when done omits phase", []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m1","phase":"commentary"}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","content":[{"type":"output_text","text":"note"}]}}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","id":"m1","content":[{"type":"output_text","text":"note"}]}]}}`, `[DONE]`}, "note", []string{"commentary"}, 0, nil},
		{"function item done identity aliases update one call", []string{
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","name":"tool","arguments":"{}"}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"f1","call_id":"c1","name":"tool","arguments":"{}"}}`,
			`{"type":"response.completed","response":{"output":[{"type":"function_call","id":"f1","call_id":"c1","name":"tool","arguments":"{}"}]}}`, `[DONE]`}, "", []string{""}, 1, nil},
		{"completed replaces stale unphased text", []string{
			`{"type":"response.output_text.delta","output_index":0,"delta":"part"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","phase":"commentary","content":[{"type":"output_text","text":"part"}]}}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","phase":"final","content":[{"type":"output_text","text":"part whole"}]}]}}`, `[DONE]`}, "part whole", []string{"final"}, 0, []string{"part", " whole"}},
		{"separate IDs with equal text", []string{
			`{"type":"response.output_text.delta","item_id":"m1","output_index":0,"delta":"same"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"same"}]}}`,
			`{"type":"response.output_text.delta","item_id":"m2","output_index":1,"delta":"same"}`,
			`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"m2","phase":"final","content":[{"type":"output_text","text":"same"}]}}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","id":"m1","content":[{"type":"output_text","text":"same"}]},{"type":"message","id":"m2","content":[{"type":"output_text","text":"same"}]}]}}`, `[DONE]`}, "samesame", []string{"commentary", "final"}, 0, []string{"same", "same"}},
		{"completed-only idless calls", []string{`{"type":"response.completed","response":{"output":[{"type":"function_call","name":"tool","arguments":"{}"},{"type":"function_call","name":"tool","arguments":"{}"}]}}`, `[DONE]`}, "", nil, 2, nil},
		{"one streamed call reconciles to two completed idless calls", []string{
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","name":"tool","arguments":"{}"}}`,
			`{"type":"response.completed","response":{"output":[{"type":"function_call","name":"tool","arguments":"{}"},{"type":"function_call","name":"tool","arguments":"{}"}]}}`, `[DONE]`}, "", nil, 2, nil},
		{"streamed idless calls acquire IDs", []string{
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","name":"tool","arguments":"{}"}}`,
			`{"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"recovered","name":"tool","arguments":"{}"}]}}`, `[DONE]`}, "", nil, 1, nil},
		{"repeated calls and idless occurrences", []string{
			`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"same1","name":"tool","arguments":"{}"}}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"same2","name":"tool","arguments":"{}"}}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","name":"tool","arguments":"{}"}}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","name":"tool","arguments":"{}"}}`,
			`{"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"same1","name":"tool","arguments":"{}"},{"type":"function_call","call_id":"same2","name":"tool","arguments":"{}"},{"type":"function_call","name":"tool","arguments":"{}"},{"type":"function_call","name":"tool","arguments":"{}"}]}}`, `[DONE]`}, "", nil, 4, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks, err := collectResponsesStreamChunks(t, strings.NewReader(codexSSE(tt.frames...)))
			if err != nil {
				t.Fatal(err)
			}
			final := chunks[len(chunks)-1]
			if final.Delta.Content != tt.content {
				t.Fatalf("content %q want %q", final.Delta.Content, tt.content)
			}
			if !final.ContentSnapshot {
				t.Fatal("terminal chunk lacks ContentSnapshot")
			}
			if len(final.Delta.ToolCalls) != tt.calls {
				t.Fatalf("calls %d want %d", len(final.Delta.ToolCalls), tt.calls)
			}
			var phases []string
			if final.Delta.ProviderMetadata != nil && final.Delta.ProviderMetadata.Codex != nil {
				for _, b := range final.Delta.ProviderMetadata.Codex.Blocks {
					phases = append(phases, b.Phase)
				}
			}
			if len(tt.phases) > 0 && len(phases) != len(tt.phases) {
				t.Fatalf("phases %#v want %#v", phases, tt.phases)
			}
			for i := 0; i < len(tt.phases); i++ {
				if phases[i] != tt.phases[i] {
					t.Fatalf("phases %#v want %#v", phases, tt.phases)
				}
			}
			var deltas []string
			for _, chunk := range chunks {
				if !chunk.Done && chunk.Delta.Content != "" {
					deltas = append(deltas, chunk.Delta.Content)
				}
			}
			if len(tt.deltas) > 0 && len(deltas) != len(tt.deltas) {
				t.Fatalf("deltas %#v want %#v", deltas, tt.deltas)
			}
			for i := 0; i < len(tt.deltas); i++ {
				if deltas[i] != tt.deltas[i] {
					t.Fatalf("deltas %#v want %#v", deltas, tt.deltas)
				}
			}
		})
	}
}

func TestCodexWSTransportMixedItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.CloseNow() }()
		if _, _, err := conn.Read(r.Context()); err != nil {
			t.Error(err)
			return
		}
		for _, frame := range []string{
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"note"}]}}`,
			`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"c1","name":"tool","arguments":"{}"}}`,
			`{"type":"response.output_item.done","output_index":2,"item":{"type":"message","id":"m2","phase":"final","content":[{"type":"output_text","text":"answer"}]}}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"note"}]},{"type":"function_call","call_id":"c1","name":"tool","arguments":"{}"},{"type":"message","id":"m2","phase":"final","content":[{"type":"output_text","text":"answer"}]}]}}`,
			`[DONE]`,
		} {
			if err := conn.Write(r.Context(), websocket.MessageText, []byte(frame)); err != nil {
				t.Error(err)
				return
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	p := &codexWSProvider{conn: conn, model: "test", writeTimeout: time.Second, interFrameTimeout: time.Second}
	response, err := p.sendRequest(ctx, ChatRequest{Model: "test"}, &wsEmitter{})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content != "noteanswer" || len(response.Message.ToolCalls) != 1 {
		t.Fatalf("response = %#v", response.Message)
	}
	if response.Message.ProviderMetadata == nil || len(response.Message.ProviderMetadata.Codex.Blocks) != 3 {
		t.Fatalf("metadata = %#v", response.Message.ProviderMetadata)
	}
}

func TestCodexWSSharedDecoderMixedItems(t *testing.T) {
	state := responsesStreamState{}
	var chunks []ChatChunk
	emit := func(c ChatChunk) error { chunks = append(chunks, c); return nil }
	frames := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m1"}}`,
		`{"type":"response.output_text.delta","item_id":"m1","output_index":0,"delta":"comment"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"comment"}]}}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"c1","name":"tool","arguments":"{}"}}`,
		`{"type":"response.output_item.done","output_index":2,"item":{"type":"message","id":"m2","phase":"final","content":[{"type":"output_text","text":"final"}]}}`,
		`{"type":"response.completed","response":{"output":[{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"comment"}]},{"type":"function_call","call_id":"c1","name":"tool","arguments":"{}"},{"type":"message","id":"m2","phase":"final","content":[{"type":"output_text","text":"final"}]}]}}`,
	}
	for _, frame := range frames {
		if _, err := processResponsesStreamEvent(&state, frame, emit); err != nil {
			t.Fatal(err)
		}
	}
	final := responsesStreamStateToChatChunk(state)
	if len(final.Delta.ToolCalls) != 1 || final.Delta.Content != "commentfinal" || !final.ContentSnapshot {
		t.Fatalf("final = %#v", final)
	}
	if len(final.Delta.ProviderMetadata.Codex.Blocks) != 3 {
		t.Fatalf("blocks = %#v", final.Delta.ProviderMetadata.Codex.Blocks)
	}
}
