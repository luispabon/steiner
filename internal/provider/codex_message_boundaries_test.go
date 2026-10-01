package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCodexMessageBoundaries(t *testing.T) {
	tests := []struct {
		name string
		text []string
		want string
	}{
		{name: "plain", text: []string{"note", "answer"}, want: "note\n\nanswer"},
		{name: "existing trailing newline", text: []string{"one\n", "two"}, want: "one\n\ntwo"},
		{name: "existing leading newline", text: []string{"one", "\ntwo"}, want: "one\n\ntwo"},
		{name: "already separated", text: []string{"one\n", "\ntwo"}, want: "one\n\ntwo"},
		{name: "empty items", text: []string{"one", "", "two"}, want: "one\n\ntwo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var joined strings.Builder
			for i, item := range tt.text {
				if i > 0 {
					joined.WriteString(codexMessageBoundary(joined.String(), item))
				}
				joined.WriteString(item)
			}
			if got := joined.String(); got != tt.want {
				t.Fatalf("content = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCodexMessageBoundaryPathsShareAccumulatedPresentation(t *testing.T) {
	tests := []struct {
		name string
		text []string
		want string
	}{
		{name: "newline message between items", text: []string{"note", "\n", "answer"}, want: "note\n\nanswer"},
		{name: "empty message between items", text: []string{"one", "", "two"}, want: "one\n\ntwo"},
		{name: "multiple newline items preserve raw newlines", text: []string{"a", "\n", "\n", "b"}, want: "a\n\n\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := responsesResponse{}
			state := responsesStreamState{}
			for i, text := range tt.text {
				index := i
				part := responsesContentPart{Type: "output_text", Text: text}
				payload.Output = append(payload.Output, responsesItem{Type: "message", ID: fmt.Sprintf("m%d", i), Content: []responsesContentPart{part}})
				entry := responsesLedgerEntry{kind: "message", outputIndex: &index, parts: map[int]string{0: text}, partOrder: []int{0}}
				state.ledger = append(state.ledger, entry)
			}
			projected := func() string { _, _, text := state.projected(); return text }()
			unary, err := normalizeResponsesResponse(payload)
			if err != nil {
				t.Fatal(err)
			}
			if projected != tt.want || unary.Message.Content != tt.want {
				t.Fatalf("projection = %q, unary = %q, want %q", projected, unary.Message.Content, tt.want)
			}
			var chunks []ChatChunk
			emit := func(chunk ChatChunk) error { chunks = append(chunks, chunk); return nil }
			streamState := responsesStreamState{}
			for i, text := range tt.text {
				index := i
				delta, _ := json.Marshal(responsesStreamEvent{Type: "response.output_text.delta", ItemID: fmt.Sprintf("m%d", i), OutputIndex: &index, Delta: text})
				if _, err := processResponsesStreamEvent(&streamState, string(delta), emit); err != nil {
					t.Fatal(err)
				}
				itemDone, _ := json.Marshal(responsesStreamEvent{Type: "response.output_item.done", OutputIndex: &index, Item: payload.Output[i]})
				if _, err := processResponsesStreamEvent(&streamState, string(itemDone), emit); err != nil {
					t.Fatal(err)
				}
			}
			var streamed strings.Builder
			for _, chunk := range chunks {
				streamed.WriteString(chunk.Delta.Content)
			}
			_, _, finalProjection := streamState.projected()
			if streamed.String() != tt.want || finalProjection != tt.want {
				t.Fatalf("stream = %q, projection = %q, want %q", streamed.String(), finalProjection, tt.want)
			}
		})
	}
}

func TestNormalizeResponsesMessageBoundariesPreserveBlocks(t *testing.T) {
	response, err := normalizeResponsesResponse(responsesResponse{Output: []responsesItem{
		{Type: "message", ID: "m1", Phase: "commentary", Content: []responsesContentPart{{Type: "output_text", Text: "note"}, {Type: "output_text", Text: "answer"}}},
		{Type: "message", ID: "m2", Phase: "final_answer", Content: []responsesContentPart{{Type: "output_text", Text: "done"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := response.Message.Content, "noteanswer\n\ndone"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	blocks := response.Message.ProviderMetadata.Codex.Blocks
	if len(blocks) != 2 || blocks[0].Text != "noteanswer" || blocks[1].Text != "done" {
		t.Fatalf("blocks = %#v", blocks)
	}
}

func TestCodexStreamDistinctMessageBoundariesMatchSnapshot(t *testing.T) {
	frames := []string{
		`{"type":"response.output_text.delta","item_id":"m1","output_index":0,"delta":"note"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"note"}]}}`,
		`{"type":"response.output_text.delta","item_id":"m2","output_index":1,"delta":"answer"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"m2","phase":"final_answer","content":[{"type":"output_text","text":"answer"}]}}`,
		`{"type":"response.completed","response":{"output":[{"type":"message","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"note"}]},{"type":"message","id":"m2","phase":"final_answer","content":[{"type":"output_text","text":"answer"}]}]}}`,
	}
	chunks, err := collectResponsesStreamChunks(t, strings.NewReader(codexSSE(frames...)))
	if err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	for _, chunk := range chunks {
		if !chunk.Done {
			streamed.WriteString(chunk.Delta.Content)
		}
	}
	final := chunks[len(chunks)-1].Delta.Content
	if streamed.String() != "note\n\nanswer" || final != streamed.String() {
		t.Fatalf("stream = %q, snapshot = %q", streamed.String(), final)
	}
}

func TestCodexRepeatedDoneFlushesPendingTextOnce(t *testing.T) {
	state := responsesStreamState{content: strings.Builder{}}
	state.content.WriteString("note")
	state.pendingTextNewlines = "\n"
	var chunks []ChatChunk
	emit := func(chunk ChatChunk) error { chunks = append(chunks, chunk); return nil }
	for i := 0; i < 2; i++ {
		if _, err := processResponsesStreamEvent(&state, "[DONE]", emit); err != nil {
			t.Fatal(err)
		}
	}
	if len(chunks) != 1 || chunks[0].Delta.Content != "\n\n" {
		t.Fatalf("chunks = %#v, want one two-newline flush", chunks)
	}
}

func TestCodexStreamNewlineOnlyItemFlushesAtTerminal(t *testing.T) {
	frames := []string{
		`{"type":"response.output_text.delta","item_id":"m1","output_index":0,"delta":"note"}`,
		`{"type":"response.output_text.delta","item_id":"m2","output_index":1,"delta":"\n"}`,
		`{"type":"response.output_text.delta","item_id":"m2","output_index":1,"delta":"\n"}`,
		`{"type":"response.completed","response":{"output":[{"type":"message","id":"m1","content":[{"type":"output_text","text":"note"}]},{"type":"message","id":"m2","content":[{"type":"output_text","text":"\n\n"}]}]}}`,
	}
	chunks, err := collectResponsesStreamChunks(t, strings.NewReader(codexSSE(frames...)))
	if err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	for _, chunk := range chunks {
		if !chunk.Done {
			streamed.WriteString(chunk.Delta.Content)
		}
	}
	if got, want := streamed.String(), chunks[len(chunks)-1].Delta.Content; got != want {
		t.Fatalf("stream = %q, snapshot = %q", got, want)
	}
}
