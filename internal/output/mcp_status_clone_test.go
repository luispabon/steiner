package output

import "testing"

// TestNewMCPStatusEventClonesPayload proves the event owns its payload: the
// producer builds the maps from live managed state and may mutate or reuse them
// after emitting.
func TestNewMCPStatusEventClonesPayload(t *testing.T) {
	servers := map[string]MCPServerState{
		"srv-a": {State: "connected", Tools: []MCPAdvertisedTool{{Name: "alpha", Outcome: "registered"}}},
	}
	origins := map[string]MCPToolOrigin{
		"mcp__srv_a__alpha": {Server: "srv-a", Tool: "alpha"},
	}
	serverTools := servers["srv-a"].Tools

	event := NewMCPStatusEvent(true, servers, origins)

	servers["srv-a"] = MCPServerState{State: "failed"}
	servers["srv-b"] = MCPServerState{State: "connected"}
	serverTools[0] = MCPAdvertisedTool{Name: "mutated", Outcome: "denied"}
	origins["mcp__srv_a__alpha"] = MCPToolOrigin{Server: "hacked", Tool: "alpha"}

	payload, ok := event.Payload.(MCPStatusEvent)
	if !ok {
		t.Fatalf("Payload type = %T", event.Payload)
	}
	if got := payload.Servers["srv-a"]; got.State != "connected" {
		t.Fatalf("Servers[srv-a].State = %q, want connected (payload aliased caller map)", got.State)
	}
	if len(payload.Servers) != 1 {
		t.Fatalf("Servers = %v, want 1 entry (payload aliased caller map)", payload.Servers)
	}
	if got := payload.Servers["srv-a"].Tools; len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("Servers[srv-a].Tools = %+v, want the advertised tool slice copied", got)
	}
	if got := payload.Origins["mcp__srv_a__alpha"].Server; got != "srv-a" {
		t.Fatalf("Origins[mcp__srv_a__alpha].Server = %q, want srv-a (payload aliased caller map)", got)
	}
}
