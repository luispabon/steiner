package output

import (
	"errors"
	"testing"
	"time"
)

func TestNewOneshotFinishedEvent(t *testing.T) {
	t.Run("without error", func(t *testing.T) {
		event := NewOneshotFinishedEvent("run-123", nil)
		if event.Type != EventTypeOneshotFinished {
			t.Fatalf("Type = %q, want %q", event.Type, EventTypeOneshotFinished)
		}
		if event.Timestamp.IsZero() {
			t.Fatal("Timestamp is zero")
		}
		if event.Timestamp.Location() != time.UTC {
			t.Fatalf("Location = %v, want UTC", event.Timestamp.Location())
		}
		p, ok := event.Payload.(OneshotFinishedEvent)
		if !ok {
			t.Fatalf("Payload type = %T", event.Payload)
		}
		if p.RunID != "run-123" {
			t.Fatalf("RunID = %q", p.RunID)
		}
		if p.Err != "" {
			t.Fatalf("Err = %q", p.Err)
		}
	})

	t.Run("with error", func(t *testing.T) {
		event := NewOneshotFinishedEvent("run-456", errors.New("something broke"))
		p, ok := event.Payload.(OneshotFinishedEvent)
		if !ok {
			t.Fatalf("Payload type = %T", event.Payload)
		}
		if p.RunID != "run-456" {
			t.Fatalf("RunID = %q", p.RunID)
		}
		if p.Err != "something broke" {
			t.Fatalf("Err = %q", p.Err)
		}
	})

	t.Run("UTC timestamp", func(t *testing.T) {
		event := NewOneshotFinishedEvent("", nil)
		if event.Timestamp.Location() != time.UTC {
			t.Fatalf("Location = %v", event.Timestamp.Location())
		}
	})
}

func TestNewSandboxStatusEvent(t *testing.T) {
	t.Run("basic fields", func(t *testing.T) {
		event := NewSandboxStatusEvent("ready", "Sandbox is ready")
		if event.Type != EventTypeSandboxStatus {
			t.Fatalf("Type = %q, want %q", event.Type, EventTypeSandboxStatus)
		}
		if event.Timestamp.IsZero() {
			t.Fatal("Timestamp is zero")
		}
		if event.Timestamp.Location() != time.UTC {
			t.Fatalf("Location = %v, want UTC", event.Timestamp.Location())
		}
		p, ok := event.Payload.(SandboxStatusEvent)
		if !ok {
			t.Fatalf("Payload type = %T", event.Payload)
		}
		if p.Status != "ready" {
			t.Fatalf("Status = %q, want %q", p.Status, "ready")
		}
		if p.Message != "Sandbox is ready" {
			t.Fatalf("Message = %q, want %q", p.Message, "Sandbox is ready")
		}
	})

	t.Run("empty message", func(t *testing.T) {
		event := NewSandboxStatusEvent("unsupported", "")
		p, ok := event.Payload.(SandboxStatusEvent)
		if !ok {
			t.Fatalf("Payload type = %T", event.Payload)
		}
		if p.Status != "unsupported" {
			t.Fatalf("Status = %q", p.Status)
		}
		if p.Message != "" {
			t.Fatalf("Message = %q, want empty", p.Message)
		}
	})

	t.Run("trims whitespace", func(t *testing.T) {
		event := NewSandboxStatusEvent("  enabled  ", "  \tmessage\n  ")
		p, ok := event.Payload.(SandboxStatusEvent)
		if !ok {
			t.Fatalf("Payload type = %T", event.Payload)
		}
		if p.Status != "enabled" {
			t.Fatalf("Status = %q, want %q", p.Status, "enabled")
		}
		if p.Message != "message" {
			t.Fatalf("Message = %q, want %q", p.Message, "message")
		}
	})
}

func TestNewModeChangedEvent(t *testing.T) {
	t.Run("basic fields", func(t *testing.T) {
		event := NewModeChangedEvent("oneshot")
		if event.Type != EventTypeModeChanged {
			t.Fatalf("Type = %q, want %q", event.Type, EventTypeModeChanged)
		}
		if event.Timestamp.IsZero() {
			t.Fatal("Timestamp is zero")
		}
		if event.Timestamp.Location() != time.UTC {
			t.Fatalf("Location = %v, want UTC", event.Timestamp.Location())
		}
		p, ok := event.Payload.(ModeChangedEvent)
		if !ok {
			t.Fatalf("Payload type = %T", event.Payload)
		}
		if p.Mode != "oneshot" {
			t.Fatalf("Mode = %q, want %q", p.Mode, "oneshot")
		}
	})

	t.Run("trims whitespace", func(t *testing.T) {
		event := NewModeChangedEvent("  interactive  ")
		p, ok := event.Payload.(ModeChangedEvent)
		if !ok {
			t.Fatalf("Payload type = %T", event.Payload)
		}
		if p.Mode != "interactive" {
			t.Fatalf("Mode = %q, want %q", p.Mode, "interactive")
		}
	})
}

func TestNewMCPStatusEvent(t *testing.T) {
	event := NewMCPStatusEvent(true, map[string]MCPServerState{
		"srv-a": {State: "connected", Transport: "stdio", Tools: []MCPAdvertisedTool{
			{Name: "alpha", Outcome: "registered"},
			{Name: "beta", Outcome: "filtered"},
			{Name: "gamma", Outcome: "denied"},
		}},
		"srv-b": {State: "failed", Error: "boom"},
	}, map[string]MCPToolOrigin{
		"mcp__srv_a__echo": {Server: "srv-a", Tool: "echo"},
	})

	if event.Type != EventTypeMCPStatus {
		t.Fatalf("Type = %q, want %q", event.Type, EventTypeMCPStatus)
	}
	if event.Timestamp.IsZero() {
		t.Fatal("Timestamp is zero")
	}
	if event.Timestamp.Location() != time.UTC {
		t.Fatalf("Location = %v, want UTC", event.Timestamp.Location())
	}
	p, ok := event.Payload.(MCPStatusEvent)
	if !ok {
		t.Fatalf("Payload type = %T", event.Payload)
	}
	if !p.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if len(p.Servers) != 2 {
		t.Fatalf("Servers = %v, want 2 entries", p.Servers)
	}
	if got := p.Servers["srv-a"]; got.State != "connected" || got.Transport != "stdio" || len(got.Tools) != 3 {
		t.Fatalf("Servers[srv-a] = %+v, want connected stdio with 3 advertised tools", got)
	}
	for i, want := range []MCPAdvertisedTool{
		{Name: "alpha", Outcome: "registered"},
		{Name: "beta", Outcome: "filtered"},
		{Name: "gamma", Outcome: "denied"},
	} {
		if got := p.Servers["srv-a"].Tools[i]; got != want {
			t.Fatalf("Servers[srv-a].Tools[%d] = %+v, want %+v", i, got, want)
		}
	}
	if got := p.Servers["srv-b"]; got.State != "failed" || got.Error != "boom" {
		t.Fatalf("Servers[srv-b] = %+v, want failed with boom", got)
	}
	if got := p.Origins["mcp__srv_a__echo"]; got.Server != "srv-a" || got.Tool != "echo" {
		t.Fatalf("Origins[mcp__srv_a__echo] = %+v, want srv-a/echo", got)
	}
}
