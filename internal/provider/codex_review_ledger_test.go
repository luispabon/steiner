package provider

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// reviewReplayItem compares the complete replay contract, not only flattened text.
type reviewReplayItem struct {
	Kind         string
	Text         string
	Phase        string
	CallID       string
	Name         string
	RawArguments string
	Arguments    map[string]any
}

func TestReviewCodexLedgerFixtures(t *testing.T) {
	var cases []struct {
		Name        string
		Frames      []string
		WantContent string
		WantReplay  []reviewReplayItem
		WantLedger  bool
	}
	if err := json.Unmarshal([]byte(reviewCodexLedgerCases), &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			var body strings.Builder
			for _, frame := range tc.Frames {
				body.WriteString("data: ")
				body.WriteString(frame)
				body.WriteString("\n\n")
			}
			chunks, err := collectResponsesStreamChunks(t, strings.NewReader(body.String()))
			if err != nil {
				t.Fatal(err)
			}
			if len(chunks) == 0 {
				t.Fatal("no terminal chunk")
			}
			final := chunks[len(chunks)-1]
			if !final.Done || !final.ContentSnapshot {
				t.Errorf("terminal flags: Done=%v ContentSnapshot=%v", final.Done, final.ContentSnapshot)
			}
			message := final.Delta
			if message.Content != tc.WantContent {
				t.Errorf("final content = %q, want %q", message.Content, tc.WantContent)
			}

			// Compare calls independently of replay: count, occurrence order, IDs, names,
			// parsed arguments and raw arguments must all survive recovery.
			wantCalls := make([]ToolCall, 0)
			wantBlocks := make([]CodexMessageBlock, 0, len(tc.WantReplay))
			for _, want := range tc.WantReplay {
				wantBlocks = append(wantBlocks, CodexMessageBlock{Kind: want.Kind, Text: want.Text, Phase: want.Phase, CallID: want.CallID})
				if want.Kind == "function_call" {
					wantCalls = append(wantCalls, ToolCall{ID: want.CallID, Name: want.Name, Arguments: want.Arguments, RawArguments: want.RawArguments})
				}
			}
			gotCalls := message.ToolCalls
			if gotCalls == nil {
				gotCalls = []ToolCall{}
			}
			if !reflect.DeepEqual(gotCalls, wantCalls) {
				t.Errorf("final calls = %#v, want %#v", gotCalls, wantCalls)
			}
			if tc.WantLedger {
				if message.ProviderMetadata == nil || message.ProviderMetadata.Codex == nil {
					t.Error("missing Codex ledger")
				} else if got := message.ProviderMetadata.Codex.Blocks; !reflect.DeepEqual(got, wantBlocks) {
					t.Errorf("blocks = %#v, want %#v", got, wantBlocks)
				}
			} else if message.ProviderMetadata != nil {
				t.Errorf("legacy nil metadata = %#v, want nil", message.ProviderMetadata)
			}

			items, err := messageToResponsesItems(message)
			if err != nil {
				t.Fatal(err)
			}
			gotReplay := make([]reviewReplayItem, 0, len(items))
			for _, item := range items {
				got := reviewReplayItem{Kind: item.Type, Phase: item.Phase, CallID: item.CallID, Name: item.Name, RawArguments: item.Args}
				switch item.Type {
				case "message":
					if item.Role != "assistant" {
						t.Errorf("replayed message role = %q, want assistant", item.Role)
					}
					for _, part := range item.Content {
						if part.Type != "output_text" {
							t.Errorf("replayed content type = %q, want output_text", part.Type)
						}
						got.Text += part.Text
					}
				case "function_call":
					if err := json.Unmarshal([]byte(item.Args), &got.Arguments); err != nil {
						t.Errorf("replayed arguments %q: %v", item.Args, err)
					}
				default:
					t.Errorf("unexpected replay item type %q", item.Type)
				}
				gotReplay = append(gotReplay, got)
			}
			if !reflect.DeepEqual(gotReplay, tc.WantReplay) {
				t.Errorf("replay = %#v, want %#v", gotReplay, tc.WantReplay)
			}
		})
	}
}

const reviewCodexLedgerCases = `[
  {
    "Name": "original_1a_completed_only_anonymous_calls",
    "Frames": [
      "{\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\"},{\"type\":\"function_call\",\"name\":\"b\",\"arguments\":\"{}\"}]}}"
    ],
    "WantContent": "",
    "WantReplay": [
      {
        "Kind": "function_call",
        "CallID": "",
        "Name": "a",
        "RawArguments": "{}",
        "Arguments": {}
      },
      {
        "Kind": "function_call",
        "CallID": "",
        "Name": "b",
        "RawArguments": "{}",
        "Arguments": {}
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "original_1b_partial_anonymous_calls",
    "Frames": [
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\"}}",
      "{\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\"},{\"type\":\"function_call\",\"name\":\"b\",\"arguments\":\"{}\"}]}}"
    ],
    "WantContent": "",
    "WantReplay": [
      {
        "Kind": "function_call",
        "CallID": "",
        "Name": "a",
        "RawArguments": "{}",
        "Arguments": {}
      },
      {
        "Kind": "function_call",
        "CallID": "",
        "Name": "b",
        "RawArguments": "{}",
        "Arguments": {}
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "original_2_authoritative_single_item_replay",
    "Frames": [
      "{\"type\":\"response.output_text.delta\",\"delta\":\"part\",\"item_id\":\"m1\",\"output_index\":0}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"m1\",\"content\":[{\"type\":\"output_text\",\"text\":\"part\"}],\"phase\":\"commentary\"},\"output_index\":0}",
      "{\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"m1\",\"content\":[{\"type\":\"output_text\",\"text\":\"part whole\"}]}]}}"
    ],
    "WantContent": "part whole",
    "WantReplay": [
      {
        "Kind": "message",
        "Text": "part whole",
        "Phase": "commentary"
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "original_3_distinct_ids_without_indices",
    "Frames": [
      "{\"type\":\"response.output_text.delta\",\"delta\":\"one\",\"item_id\":\"m1\"}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"m1\",\"content\":[{\"type\":\"output_text\",\"text\":\"one\"}],\"phase\":\"commentary\"}}",
      "{\"type\":\"response.output_text.delta\",\"delta\":\"two\",\"item_id\":\"m2\"}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"m2\",\"content\":[{\"type\":\"output_text\",\"text\":\"two\"}],\"phase\":\"final_answer\"}}",
      "{\"type\":\"response.completed\",\"response\":{}}"
    ],
    "WantContent": "onetwo",
    "WantReplay": [
      {
        "Kind": "message",
        "Text": "one",
        "Phase": "commentary"
      },
      {
        "Kind": "message",
        "Text": "two",
        "Phase": "final_answer"
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "original_4_equal_text_phase_identity",
    "Frames": [
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"m1\",\"content\":[{\"type\":\"output_text\",\"text\":\"same\"}],\"phase\":\"commentary\"},\"output_index\":0}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"m2\",\"content\":[{\"type\":\"output_text\",\"text\":\"same\"}],\"phase\":\"final_answer\"},\"output_index\":1}",
      "{\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"m1\",\"content\":[{\"type\":\"output_text\",\"text\":\"same\"}]},{\"type\":\"message\",\"id\":\"m2\",\"content\":[{\"type\":\"output_text\",\"text\":\"same\"}]}]}}"
    ],
    "WantContent": "samesame",
    "WantReplay": [
      {
        "Kind": "message",
        "Text": "same",
        "Phase": "commentary"
      },
      {
        "Kind": "message",
        "Text": "same",
        "Phase": "final_answer"
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "original_5_anonymous_call_acquires_identity",
    "Frames": [
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\"}}",
      "{\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"note\"}],\"phase\":\"commentary\"},{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\",\"id\":\"f1\",\"call_id\":\"c1\"}]}}"
    ],
    "WantContent": "note",
    "WantReplay": [
      {
        "Kind": "message",
        "Text": "note",
        "Phase": "commentary"
      },
      {
        "Kind": "function_call",
        "CallID": "c1",
        "Name": "a",
        "RawArguments": "{}",
        "Arguments": {}
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "original_6_nil_metadata_authoritative_final",
    "Frames": [
      "{\"type\":\"response.output_text.delta\",\"delta\":\"AB\",\"item_id\":\"m1\"}",
      "{\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"m1\",\"content\":[{\"type\":\"output_text\",\"text\":\"XAB\"}]}]}}"
    ],
    "WantContent": "XAB",
    "WantReplay": [
      {
        "Kind": "message",
        "Text": "XAB",
        "Phase": ""
      }
    ],
    "WantLedger": false
  },
  {
    "Name": "sparse_1_anonymous_deltas_coalesce",
    "Frames": [
      "{\"type\":\"response.output_text.delta\",\"delta\":\"one\"}",
      "{\"type\":\"response.output_text.delta\",\"delta\":\"two\"}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"m1\",\"content\":[{\"type\":\"output_text\",\"text\":\"onetwo\"}],\"phase\":\"final_answer\"}}",
      "{\"type\":\"response.completed\",\"response\":{}}"
    ],
    "WantContent": "onetwo",
    "WantReplay": [
      {
        "Kind": "message",
        "Text": "onetwo",
        "Phase": "final_answer"
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "sparse_2_anonymous_delta_adopts_late_index",
    "Frames": [
      "{\"type\":\"response.output_text.delta\",\"delta\":\"one\"}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"m1\",\"content\":[{\"type\":\"output_text\",\"text\":\"one\"}],\"phase\":\"final_answer\"},\"output_index\":0}",
      "{\"type\":\"response.completed\",\"response\":{}}"
    ],
    "WantContent": "one",
    "WantReplay": [
      {
        "Kind": "message",
        "Text": "one",
        "Phase": "final_answer"
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "sparse_3_call_completion_alias_update",
    "Frames": [
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\"},\"output_index\":0}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\",\"id\":\"f1\",\"call_id\":\"c1\"},\"output_index\":0}",
      "{\"type\":\"response.completed\",\"response\":{}}"
    ],
    "WantContent": "",
    "WantReplay": [
      {
        "Kind": "function_call",
        "CallID": "c1",
        "Name": "a",
        "RawArguments": "{}",
        "Arguments": {}
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "sparse_4_phase_on_added_retained",
    "Frames": [
      "{\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"id\":\"m1\",\"phase\":\"commentary\"},\"output_index\":0}",
      "{\"type\":\"response.output_text.delta\",\"delta\":\"note\",\"item_id\":\"m1\",\"output_index\":0}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"m1\",\"content\":[{\"type\":\"output_text\",\"text\":\"note\"}]},\"output_index\":0}",
      "{\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"m1\",\"content\":[{\"type\":\"output_text\",\"text\":\"note\"}]}]}}"
    ],
    "WantContent": "note",
    "WantReplay": [
      {
        "Kind": "message",
        "Text": "note",
        "Phase": "commentary"
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "latest_1_distinct_index_only_messages",
    "Frames": [
      "{\"type\":\"response.output_text.delta\",\"delta\":\"one\",\"output_index\":0}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"one\"}],\"phase\":\"commentary\"},\"output_index\":0}",
      "{\"type\":\"response.output_text.delta\",\"delta\":\"two\",\"output_index\":1}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"two\"}],\"phase\":\"final_answer\"},\"output_index\":1}",
      "{\"type\":\"response.completed\",\"response\":{}}"
    ],
    "WantContent": "onetwo",
    "WantReplay": [
      {
        "Kind": "message",
        "Text": "one",
        "Phase": "commentary"
      },
      {
        "Kind": "message",
        "Text": "two",
        "Phase": "final_answer"
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "latest_2_out_of_order_call_completions",
    "Frames": [
      "{\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\",\"id\":\"f1\",\"call_id\":\"c1\"},\"output_index\":0}",
      "{\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"b\",\"arguments\":\"{}\",\"id\":\"f2\",\"call_id\":\"c2\"},\"output_index\":1}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"b\",\"arguments\":\"{}\",\"id\":\"f2\",\"call_id\":\"c2\"},\"output_index\":1}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\",\"id\":\"f1\",\"call_id\":\"c1\"},\"output_index\":0}",
      "{\"type\":\"response.completed\",\"response\":{}}"
    ],
    "WantContent": "",
    "WantReplay": [
      {
        "Kind": "function_call",
        "CallID": "c1",
        "Name": "a",
        "RawArguments": "{}",
        "Arguments": {}
      },
      {
        "Kind": "function_call",
        "CallID": "c2",
        "Name": "b",
        "RawArguments": "{}",
        "Arguments": {}
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "latest_3a_call_id_acquires_index_alias",
    "Frames": [
      "{\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\",\"id\":\"f1\",\"call_id\":\"c1\"}}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\",\"id\":\"f1\",\"call_id\":\"c1\"},\"output_index\":0}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\",\"call_id\":\"c1\"},\"output_index\":0}",
      "{\"type\":\"response.completed\",\"response\":{}}"
    ],
    "WantContent": "",
    "WantReplay": [
      {
        "Kind": "function_call",
        "CallID": "c1",
        "Name": "a",
        "RawArguments": "{}",
        "Arguments": {}
      }
    ],
    "WantLedger": true
  },
  {
    "Name": "latest_3b_call_alias_preserves_argument_update",
    "Frames": [
      "{\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{}\",\"id\":\"f1\",\"call_id\":\"c1\"}}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{\\\"value\\\":1}\",\"id\":\"f1\",\"call_id\":\"c1\"},\"output_index\":0}",
      "{\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"a\",\"arguments\":\"{\\\"value\\\":2}\",\"call_id\":\"c1\"},\"output_index\":0}",
      "{\"type\":\"response.completed\",\"response\":{}}"
    ],
    "WantContent": "",
    "WantReplay": [
      {
        "Kind": "function_call",
        "CallID": "c1",
        "Name": "a",
        "RawArguments": "{\"value\":2}",
        "Arguments": {
          "value": 2
        }
      }
    ],
    "WantLedger": true
  }
]`
