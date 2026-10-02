package main

import (
	"strings"
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
	for group, prefix := range map[string]string{"group-one": "call_0~", "group-two": "call_0~", "group-three": "call_"} {
		if got := accepted[group].BatchID; !strings.HasPrefix(got, prefix) {
			t.Fatalf("group %q batch ID = %q, want prefix %q", group, got, prefix)
		}
	}
}
