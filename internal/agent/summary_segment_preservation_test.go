package agent

import "testing"

func segmentTestMessage() Message {
	return Message{
		Role:    MessageRoleSummary,
		Content: withRequestBlock("Segment 1. summary", "the latest request"),
		Images:  []ImageBlock{{ID: "img-1", FilePath: "/tmp/img.png", MediaType: "image/png", Data: "aGk="}},
		Segment: &SummarySegment{Sequence: 1, SourceMessageCount: 3},
	}
}

func TestSummarySegmentSurvivesMessageHelpers(t *testing.T) {
	helpers := []struct {
		name string
		run  func([]Message) []Message
	}{
		{name: "stripImages", run: stripImages},
		{name: "stripImagesFromMessages", run: func(messages []Message) []Message {
			return stripImagesFromMessages(messages, false)
		}},
		{name: "stripImagesFromMessagesExceptDeferredRead", run: func(messages []Message) []Message {
			return stripImagesFromMessagesExceptDeferredRead(messages, false)
		}},
		{name: "stripDeferredReadImages", run: func(messages []Message) []Message {
			return stripDeferredReadImages(messages, false)
		}},
		{name: "stripRequestBlocks", run: stripRequestBlocks},
		{name: "stripRequestBlockSource", run: func(messages []Message) []Message {
			out, _ := stripRequestBlockSource(messages)
			return out
		}},
	}
	for _, helper := range helpers {
		t.Run(helper.name, func(t *testing.T) {
			source := segmentTestMessage()
			out := helper.run([]Message{source})
			if len(out) != 1 {
				t.Fatalf("message count = %d, want 1", len(out))
			}
			if out[0].Segment == nil {
				t.Fatalf("segment metadata dropped: %+v", out[0])
			}
			if *out[0].Segment != *source.Segment {
				t.Fatalf("segment metadata = %+v, want %+v", *out[0].Segment, *source.Segment)
			}
		})
	}
}

func TestDropRequestBlockStatePreservesSegment(t *testing.T) {
	segment := segmentTestMessage()
	plain := Message{Role: MessageRoleSummary, Content: withRequestBlock("plain summary", "the latest request")}
	state := RunState{Lineage: ConversationLineage{
		Generations:      []ConversationGeneration{{ID: 2, SummaryPrefix: []Message{segment, plain}}},
		NextGenerationID: 3,
	}}
	state.Conversation = state.Lineage.FullMessages()
	next, summaryText, dropped := dropRequestBlockState(state)
	if !dropped || summaryText != "plain summary" {
		t.Fatalf("dropped = %t text = %q, want only the plain summary rewritten", dropped, summaryText)
	}
	prefix := next.Lineage.Generations[0].SummaryPrefix
	if prefix[0].Segment == nil || *prefix[0].Segment != *segment.Segment {
		t.Fatalf("segment metadata = %+v, want %+v", prefix[0].Segment, *segment.Segment)
	}
	if prefix[0].Content != segment.Content {
		t.Fatalf("segment content = %q, want its bytes kept", prefix[0].Content)
	}
	if prefix[1].Content != "plain summary" {
		t.Fatalf("plain summary = %q, want the body", prefix[1].Content)
	}
	if state.Lineage.Generations[0].SummaryPrefix[0].Content != segment.Content {
		t.Fatal("source state mutated")
	}
}

func TestLineageWithoutImageDataPreservesSegment(t *testing.T) {
	source := segmentTestMessage()
	lineage := ConversationLineage{
		Generations:      []ConversationGeneration{{ID: 2, SummaryPrefix: []Message{source}, Messages: []Message{source}}},
		NextGenerationID: 3,
	}
	out := LineageWithoutImageData(lineage)
	for name, messages := range map[string][]Message{
		"summary prefix": out.Generations[0].SummaryPrefix,
		"messages":       out.Generations[0].Messages,
	} {
		if len(messages) != 1 || messages[0].Segment == nil {
			t.Fatalf("%s lost segment metadata: %+v", name, messages)
		}
		if *messages[0].Segment != *source.Segment {
			t.Fatalf("%s segment metadata = %+v, want %+v", name, *messages[0].Segment, *source.Segment)
		}
		if len(messages[0].Images) != 0 {
			t.Fatalf("%s kept image data", name)
		}
	}
	if lineage.Generations[0].SummaryPrefix[0].Segment == nil {
		t.Fatal("source lineage mutated")
	}
}

func TestCloneMessagesDeepCopiesSegment(t *testing.T) {
	source := []Message{segmentTestMessage()}
	cloned := CloneMessages(source)
	if cloned[0].Segment == source[0].Segment {
		t.Fatal("segment metadata pointer is shared with the source")
	}
	cloned[0].Segment.Sequence = 7
	if source[0].Segment.Sequence != 1 {
		t.Fatalf("source segment mutated to sequence %d", source[0].Segment.Sequence)
	}
}

// TestCloneMessagesWithoutSegmentsAllocatesOnlyTheSlice pins that a
// segment-free clone allocates nothing per message: the nil Segment path must
// add no work, so only the output slice is allocated.
func TestCloneMessagesWithoutSegmentsAllocatesOnlyTheSlice(t *testing.T) {
	source := []Message{
		{Role: MessageRoleUser, Content: "question"},
		{Role: MessageRoleAssistant, Content: "answer"},
	}
	var cloned []Message
	allocs := testing.AllocsPerRun(100, func() {
		cloned = cloneMessages(source)
	})
	if allocs != 1 {
		t.Fatalf("cloneMessages allocated %v times for two segment-free messages, want 1 for the output slice", allocs)
	}
	if cloned[0].Segment != nil {
		t.Fatalf("cloned message grew segment metadata: %+v", cloned[0].Segment)
	}
}

func TestConversationGenerationCloneDeepCopiesSegment(t *testing.T) {
	source := segmentTestMessage()
	generation := ConversationGeneration{
		ID:            2,
		SummaryPrefix: []Message{source},
		Messages:      []Message{source},
	}
	cloned := generation.Clone()
	if cloned.SummaryPrefix[0].Segment == generation.SummaryPrefix[0].Segment {
		t.Fatal("summary prefix segment metadata pointer is shared")
	}
	if cloned.Messages[0].Segment == generation.Messages[0].Segment {
		t.Fatal("message segment metadata pointer is shared")
	}
	cloned.SummaryPrefix[0].Segment.Sequence = 5
	if generation.SummaryPrefix[0].Segment.Sequence != 1 {
		t.Fatalf("source generation mutated to sequence %d", generation.SummaryPrefix[0].Segment.Sequence)
	}
}
