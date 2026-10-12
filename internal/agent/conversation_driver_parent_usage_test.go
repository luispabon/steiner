package agent

import (
	"context"
	"reflect"
	"slices"
	"testing"
)

// finishRunWith releases run c with its conversation plus an assistant reply
// and the given output fields.
func finishRunWith(c *runCall, out DriverRunOutput) {
	out.Conversation = append(slices.Clone(c.in.Conversation), Message{Role: MessageRoleAssistant, Content: "ok"})
	c.release <- driverTestResult{out: out}
}

// TestConversationDriverCarriesParentUsageAcrossRuns proves consecutive
// assistant-only runs carry the last parent usage, and that the driver keeps its
// own copy of what a run returned.
func TestConversationDriverCarriesParentUsageAcrossRuns(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()

	h.d.Submit("one", nil, SubmitMeta{})
	first := h.nextRun()
	if first.in.ParentUsage != nil {
		t.Fatalf("first run ParentUsage = %+v, want nil", first.in.ParentUsage)
	}
	finishRunWith(first, DriverRunOutput{ParentUsage: parentUsageFixture(8000)})
	h.waitQuiescent()

	h.d.Submit("two", nil, SubmitMeta{})
	second := h.nextRun()
	if !reflect.DeepEqual(second.in.ParentUsage, parentUsageFixture(8000)) {
		t.Fatalf("second run ParentUsage = %+v, want %+v", second.in.ParentUsage, parentUsageFixture(8000))
	}
	returned := parentUsageFixture(9000)
	finishRunWith(second, DriverRunOutput{ParentUsage: returned})
	h.waitQuiescent()
	returned.Usage.PromptTokens = 1

	h.d.Submit("three", nil, SubmitMeta{})
	third := h.nextRun()
	if !reflect.DeepEqual(third.in.ParentUsage, parentUsageFixture(9000)) {
		t.Fatalf("third run ParentUsage = %+v, want %+v", third.in.ParentUsage, parentUsageFixture(9000))
	}
	finishRunWith(third, DriverRunOutput{})
	h.waitQuiescent()
	h.closeDriver()
}

// TestConversationDriverParentUsageDroppedOnGenerationAdvance proves a run that
// advances the lineage, as automatic compaction does, leaves no usage for the
// next run to seed from.
func TestConversationDriverParentUsageDroppedOnGenerationAdvance(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()

	h.d.Submit("one", nil, SubmitMeta{})
	first := h.nextRun()
	advanced := newConversationLineage(first.in.Conversation).WithNewGeneration(nil, first.in.Conversation)
	finishRunWith(first, DriverRunOutput{Lineage: advanced, ParentUsage: parentUsageFixture(8000)})
	h.waitQuiescent()

	h.d.Submit("two", nil, SubmitMeta{})
	second := h.nextRun()
	if second.in.ParentUsage != nil {
		t.Fatalf("run after generation advance ParentUsage = %+v, want nil", second.in.ParentUsage)
	}
	finishRunWith(second, DriverRunOutput{})
	h.waitQuiescent()
	h.closeDriver()
}

// TestConversationDriverCarriesStatefulFloorAcrossGenerationAdvance proves an
// armed stateful floor survives the generation advance of the compaction that
// preceded it, and is carried to the new generation.
func TestConversationDriverCarriesStatefulFloorAcrossGenerationAdvance(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()

	h.d.Submit("one", nil, SubmitMeta{})
	first := h.nextRun()
	advanced := newConversationLineage(first.in.Conversation).WithNewGeneration(nil, first.in.Conversation)
	floored := parentUsageFixture(8000)
	floored.StatefulFloor = 7500
	finishRunWith(first, DriverRunOutput{Lineage: advanced, ParentUsage: floored})
	h.waitQuiescent()

	h.d.Submit("two", nil, SubmitMeta{})
	second := h.nextRun()
	want := parentUsageFixture(8000)
	want.StatefulFloor = 7500
	if !reflect.DeepEqual(second.in.ParentUsage, want) {
		t.Fatalf("run after generation advance ParentUsage = %+v, want %+v", second.in.ParentUsage, want)
	}
	finishRunWith(second, DriverRunOutput{})
	h.waitQuiescent()
	h.closeDriver()
}

// TestConversationDriverParentUsageDroppedOnManualCompaction proves a manual
// compaction that changes the generation leaves no usage for the next run.
func TestConversationDriverParentUsageDroppedOnManualCompaction(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()

	h.d.Submit("one", nil, SubmitMeta{})
	first := h.nextRun()
	finishRunWith(first, DriverRunOutput{ParentUsage: parentUsageFixture(8000)})
	h.waitQuiescent()

	h.d.RequestCompaction(func(_ context.Context, conv []Message) ([]Message, ConversationLineage, error) {
		return conv, newConversationLineage(conv).WithNewGeneration(nil, conv), nil
	})
	h.waitQuiescent()

	h.d.Submit("two", nil, SubmitMeta{})
	second := h.nextRun()
	if second.in.ParentUsage != nil {
		t.Fatalf("run after manual compaction ParentUsage = %+v, want nil", second.in.ParentUsage)
	}
	finishRunWith(second, DriverRunOutput{})
	h.waitQuiescent()
	h.closeDriver()
}

// TestCarriedParentUsageInvalidatedByGeneration proves the carried usage is
// tied to its generation: once the latest generation moves on, it is not seeded
// even when nothing cleared it.
func TestCarriedParentUsageInvalidatedByGeneration(t *testing.T) {
	conv := []Message{{Role: MessageRoleUser, Content: "hi"}}
	d := NewConversationDriver(DriverOptions{}, conv, newConversationLineage(conv))
	d.mu.Lock()
	defer d.mu.Unlock()

	d.carryParentUsageLocked(false, parentUsageFixture(8000))
	if d.carriedParentUsageLocked() == nil {
		t.Fatal("carried usage nil in its own generation, want it seeded")
	}

	d.lineage = d.lineage.WithNewGeneration(nil, conv)
	if got := d.carriedParentUsageLocked(); got != nil {
		t.Errorf("carried usage after generation change = %+v, want nil", got)
	}
}
