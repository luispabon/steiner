package provider

import (
	"strings"
	"testing"
)

func reviewLedgerFramesFollowup(t *testing.T, frames ...string) []ChatChunk {
	t.Helper()
	chunks, err := collectResponsesStreamChunks(t, strings.NewReader(codexSSE(frames...)))
	if err != nil {
		t.Fatal(err)
	}
	return chunks
}

func TestReviewDoneOnlyWithoutResponseSnapshot(t *testing.T) {
	for _, frames := range [][]string{
		{`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","phase":"final_answer","content":[{"type":"output_text","text":"answer"}]}}`, `[DONE]`},
		{`{"type":"response.output_text.done","item_id":"m1","output_index":0,"content_index":0,"text":"answer"}`, `[DONE]`},
		{`{"type":"response.output_text.done","item_id":"m1","output_index":0,"content_index":0,"text":"answer"}`, `{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"f1","call_id":"c1","name":"tool","arguments":"{}"}}`, `[DONE]`},
	} {
		chunks := reviewLedgerFramesFollowup(t, frames...)
		if len(chunks) == 0 {
			t.Error("no final chunk despite ledger content")
			continue
		}
		final := chunks[len(chunks)-1]
		if !final.Done || final.Delta.Content != "answer" {
			t.Errorf("final content=%q Done=%v metadata=%#v", final.Delta.Content, final.Done, final.Delta.ProviderMetadata)
		}
	}
}

func TestReviewCallIDRetainedWhenDoneOmitsIt(t *testing.T) {
	chunks := reviewLedgerFramesFollowup(t,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"f1","call_id":"c1","name":"tool","arguments":""}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"f1","name":"tool","arguments":"{}"}}`,
		`{"type":"response.completed","response":{}}`)
	final := chunks[len(chunks)-1]
	if len(final.Delta.ToolCalls) != 1 || final.Delta.ToolCalls[0].ID != "c1" {
		t.Fatalf("calls %#v want c1", final.Delta.ToolCalls)
	}
}

func TestReviewCallOnlyDoneMarker(t *testing.T) {
	chunks := reviewLedgerFramesFollowup(t, `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"f1","call_id":"c1","name":"tool","arguments":"{}"}}`, `[DONE]`)
	if len(chunks) == 0 {
		t.Fatal("no terminal chunk for completed function call")
	}
	final := chunks[len(chunks)-1]
	if !final.Done || len(final.Delta.ToolCalls) != 1 || final.Delta.ToolCalls[0].ID != "c1" {
		t.Fatalf("final %#v", final)
	}
}

func TestReviewDoneTextWithReasoningOnly(t *testing.T) {
	chunks := reviewLedgerFramesFollowup(t, `{"type":"response.output_text.done","item_id":"m1","output_index":0,"text":"answer"}`, `{"type":"response.reasoning_summary_text.delta","delta":"reason"}`, `[DONE]`)
	if len(chunks) == 0 {
		t.Fatal("no terminal")
	}
	if got := chunks[len(chunks)-1].Delta.Content; got != "answer" {
		t.Fatalf("final text %q want answer", got)
	}
}

func TestReviewSnapshotRetainsCallIDWhenOmitted(t *testing.T) {
	chunks := reviewLedgerFramesFollowup(t,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"f1","call_id":"c1","name":"tool","arguments":"{}"}}`,
		`{"type":"response.completed","response":{"output":[{"type":"function_call","id":"f1","name":"tool","arguments":"{}"}]}}`)
	final := chunks[len(chunks)-1]
	if len(final.Delta.ToolCalls) != 1 || final.Delta.ToolCalls[0].ID != "c1" {
		t.Fatalf("calls %#v want c1", final.Delta.ToolCalls)
	}
}
