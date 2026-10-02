package main

import (
	"regexp"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

func TestNamedGroupsAcceptedWhenProviderRepeatsOrOmitsCallIDs(t *testing.T) {
	prov := newAsyncScript("one", "two", "three")
	prov.parent = []func(provider.ChatRequest) provider.ChatResponse{
		step(toolCallsResponse(subAgentCall("call_0", "one", "group-one"))),
		step(toolCallsResponse(subAgentCall("call_0", "two", "group-two"))),
		step(toolCallsResponse(subAgentCall("", "three", "group-three"))),
		step(textResponse("waiting")),
	}
	h := newAsyncHarness(t, prov, 4)

	submit(t, h.session, "dispatch three named groups")
	recvStartedSet(t, prov, "one", "two", "three")
	waitRuns(t, h.session)

	accepted := map[string]output.DelegationAcceptedEvent{}
	for _, event := range h.events.snapshot() {
		if payload, ok := event.Payload.(output.DelegationAcceptedEvent); ok {
			accepted[payload.Group] = payload
		}
	}
	batches := map[string]string{}
	for _, group := range []string{"group-one", "group-two", "group-three"} {
		payload, ok := accepted[group]
		if !ok || payload.BatchID == "" || payload.AgentID == "" {
			t.Fatalf("group %q admission = %+v (found %v), want accepted with a batch ID", group, payload, ok)
		}
		if other, dup := batches[payload.BatchID]; dup {
			t.Fatalf("groups %q and %q share batch ID %q", other, group, payload.BatchID)
		}
		batches[payload.BatchID] = group
	}
	for group, pattern := range map[string]*regexp.Regexp{
		"group-one":   regexp.MustCompile(`^call_0~`),
		"group-two":   regexp.MustCompile(`^call_0~`),
		"group-three": regexp.MustCompile(`^call_[0-9a-f]{8}_[0-9]+~`),
	} {
		if got := accepted[group].BatchID; !pattern.MatchString(got) {
			t.Fatalf("group %q batch ID = %q, want match %s", group, got, pattern)
		}
	}
}
