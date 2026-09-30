package provider

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCodexBlocksReplayOrderAndPhase(t *testing.T) {
	response, err := normalizeResponsesResponse(responsesResponse{Output: []responsesItem{
		{Type: "message", Phase: "commentary", Content: []responsesContentPart{{Type: "output_text", Text: "working"}}},
		{Type: "function_call", CallID: "c1", Name: "tool", Args: "{}"},
		{Type: "message", Phase: "final", Content: []responsesContentPart{{Type: "output_text", Text: "done"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	blocks := response.Message.ProviderMetadata.Codex.Blocks
	if len(blocks) != 3 || blocks[0].Phase != "commentary" || blocks[2].Text != "done" {
		t.Fatalf("blocks = %#v", blocks)
	}
	items, err := messageToResponsesItems(response.Message)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].Phase != "commentary" || items[1].Type != "function_call" || items[2].Phase != "final" {
		t.Fatalf("replay = %#v", items)
	}
}

func TestCodexBlocksLegacyEncodingAndClone(t *testing.T) {
	legacy, err := json.Marshal(CodexMessageMetadata{ReasoningID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if string(legacy) != `{"reasoning_id":"r"}` {
		t.Fatalf("legacy encoding = %s", legacy)
	}
	message := Message{ProviderMetadata: &MessageProviderMetadata{Codex: &CodexMessageMetadata{Blocks: []CodexMessageBlock{{Kind: "message", Phase: "final"}}}}}
	clone := CloneMessages([]Message{message})
	clone[0].ProviderMetadata.Codex.Blocks[0].Phase = "changed"
	if reflect.DeepEqual(clone[0].ProviderMetadata.Codex.Blocks, message.ProviderMetadata.Codex.Blocks) {
		t.Fatal("clone shares Codex blocks")
	}
}
