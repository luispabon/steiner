package agent

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

// lastMainRequestFixture returns a main request started at at on the test
// model identity.
func lastMainRequestFixture(at time.Time) LastMainRequest {
	return LastMainRequest{At: at, ProviderAlias: "openai", BackendModelID: "gpt-test", PromptCacheKey: "cache-key"}
}

// TestConversationDriverCarriesLastMainRequestAcrossRuns proves each run is
// seeded with the last main request the previous run returned, and that the
// accessor reports the same value.
func TestConversationDriverCarriesLastMainRequestAcrossRuns(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()
	base := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

	h.d.Submit("one", nil, SubmitMeta{})
	first := h.nextRun()
	if first.in.LastMainRequest != (LastMainRequest{}) {
		t.Fatalf("first run LastMainRequest = %+v, want zero", first.in.LastMainRequest)
	}
	firstReq := lastMainRequestFixture(base)
	finishRunWith(first, DriverRunOutput{LastMainRequest: firstReq})
	h.waitQuiescent()
	if got := h.d.LastMainRequest(); got != firstReq {
		t.Fatalf("accessor after first run = %+v, want %+v", got, firstReq)
	}

	h.d.Submit("two", nil, SubmitMeta{})
	second := h.nextRun()
	if second.in.LastMainRequest != firstReq {
		t.Fatalf("second run LastMainRequest = %+v, want %+v", second.in.LastMainRequest, firstReq)
	}
	secondReq := lastMainRequestFixture(base.Add(time.Minute))
	finishRunWith(second, DriverRunOutput{LastMainRequest: secondReq})
	h.waitQuiescent()

	h.d.Submit("three", nil, SubmitMeta{})
	third := h.nextRun()
	if !reflect.DeepEqual(third.in.LastMainRequest, secondReq) {
		t.Fatalf("third run LastMainRequest = %+v, want %+v", third.in.LastMainRequest, secondReq)
	}
	if got := h.d.LastMainRequest(); got != secondReq {
		t.Errorf("accessor while third run in flight = %+v, want %+v", got, secondReq)
	}
	finishRunWith(third, DriverRunOutput{LastMainRequest: secondReq})
	h.waitQuiescent()
	h.closeDriver()
}

// TestConversationDriverKeepsLastMainRequestAcrossCompactionAndLineageAdvance
// proves a run that advances the lineage, and a manual compaction that changes
// the generation, both leave the last main request for the next run. Parent
// usage is still dropped by the same transitions.
func TestConversationDriverKeepsLastMainRequestAcrossCompactionAndLineageAdvance(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()
	req := lastMainRequestFixture(time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC))

	h.d.Submit("one", nil, SubmitMeta{})
	first := h.nextRun()
	advanced := newConversationLineage(first.in.Conversation).WithNewGeneration(nil, first.in.Conversation)
	finishRunWith(first, DriverRunOutput{Lineage: advanced, ParentUsage: parentUsageFixture(8000), LastMainRequest: req})
	h.waitQuiescent()

	h.d.Submit("two", nil, SubmitMeta{})
	second := h.nextRun()
	if second.in.LastMainRequest != req {
		t.Fatalf("run after lineage advance LastMainRequest = %+v, want %+v", second.in.LastMainRequest, req)
	}
	if second.in.ParentUsage != nil {
		t.Errorf("run after lineage advance ParentUsage = %+v, want nil", second.in.ParentUsage)
	}
	finishRunWith(second, DriverRunOutput{LastMainRequest: req})
	h.waitQuiescent()

	h.d.RequestCompaction(func(_ context.Context, conv []Message) ([]Message, ConversationLineage, error) {
		return conv, newConversationLineage(conv).WithNewGeneration(nil, conv), nil
	})
	h.waitQuiescent()
	if got := h.d.LastMainRequest(); got != req {
		t.Errorf("accessor after manual compaction = %+v, want %+v", got, req)
	}

	h.d.Submit("three", nil, SubmitMeta{})
	third := h.nextRun()
	if third.in.LastMainRequest != req {
		t.Fatalf("run after manual compaction LastMainRequest = %+v, want %+v", third.in.LastMainRequest, req)
	}
	finishRunWith(third, DriverRunOutput{LastMainRequest: req})
	h.waitQuiescent()
	h.closeDriver()
}

// TestConversationDriverLastMainRequestAccessorIsSynchronized reads the accessor
// from another goroutine while runs replace the value. Under -race an unlocked
// read fails the run, and a read that returns a value no run returned fails the
// test.
func TestConversationDriverLastMainRequestAccessorIsSynchronized(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()
	base := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

	const rounds = 20
	allowed := []LastMainRequest{{}}
	for i := range rounds {
		allowed = append(allowed, lastMainRequestFixture(base.Add(time.Duration(i)*time.Second)))
	}

	stop := make(chan struct{})
	bad := make(chan LastMainRequest, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if got := h.d.LastMainRequest(); !slices.Contains(allowed, got) {
				bad <- got
				return
			}
		}
	}()

	for i := range rounds {
		h.d.Submit("turn", nil, SubmitMeta{})
		call := h.nextRun()
		finishRunWith(call, DriverRunOutput{LastMainRequest: allowed[i+1]})
		h.waitQuiescent()
	}
	close(stop)
	wg.Wait()
	select {
	case got := <-bad:
		t.Errorf("accessor returned %+v, which no run returned", got)
	default:
	}
	if got := h.d.LastMainRequest(); got != allowed[rounds] {
		t.Errorf("final accessor = %+v, want %+v", got, allowed[rounds])
	}
	h.closeDriver()
}
