package output

import (
	"errors"
	"strings"
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

func TestNewConfigWarningEvent(t *testing.T) {
	t.Run("basic fields", func(t *testing.T) {
		event := NewConfigWarningEvent("max_tokens is deprecated")
		if event.Type != EventTypeConfigWarning {
			t.Fatalf("Type = %q, want %q", event.Type, EventTypeConfigWarning)
		}
		if event.Timestamp.IsZero() {
			t.Fatal("Timestamp is zero")
		}
		if event.Timestamp.Location() != time.UTC {
			t.Fatalf("Location = %v, want UTC", event.Timestamp.Location())
		}
		p, ok := event.Payload.(ConfigWarningEvent)
		if !ok {
			t.Fatalf("Payload type = %T", event.Payload)
		}
		if p.Message != "max_tokens is deprecated" {
			t.Fatalf("Message = %q, want %q", p.Message, "max_tokens is deprecated")
		}
	})

	t.Run("empty message", func(t *testing.T) {
		event := NewConfigWarningEvent("")
		p, ok := event.Payload.(ConfigWarningEvent)
		if !ok {
			t.Fatalf("Payload type = %T", event.Payload)
		}
		if p.Message != "" {
			t.Fatalf("Message = %q, want empty", p.Message)
		}
	})

	t.Run("trims whitespace", func(t *testing.T) {
		event := NewConfigWarningEvent("  \tmax_bytes wins\n  ")
		p, ok := event.Payload.(ConfigWarningEvent)
		if !ok {
			t.Fatalf("Payload type = %T", event.Payload)
		}
		if p.Message != "max_bytes wins" {
			t.Fatalf("Message = %q, want %q", p.Message, "max_bytes wins")
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

func TestNewDelegationWorktreeDisposalEvent(t *testing.T) {
	event := NewDelegationWorktreeDisposalEvent("child-1", true, "")
	if event.Type != EventTypeDelegationWorktreeDisposal {
		t.Fatalf("Type = %q, want %q", event.Type, EventTypeDelegationWorktreeDisposal)
	}
	if event.Timestamp.IsZero() || event.Timestamp.Location() != time.UTC {
		t.Fatalf("Timestamp = %v, want non-zero UTC", event.Timestamp)
	}
	payload, ok := event.Payload.(DelegationWorktreeDisposalEvent)
	if !ok {
		t.Fatalf("Payload type = %T, want DelegationWorktreeDisposalEvent", event.Payload)
	}
	if payload.AgentID != "child-1" || !payload.Removed || payload.Error != "" {
		t.Fatalf("payload = %+v, want successful disposal", payload)
	}

	event = NewDelegationWorktreeDisposalEvent("child-2", false, "cleanup failed")
	payload = event.Payload.(DelegationWorktreeDisposalEvent)
	if payload.Removed || payload.Error != "cleanup failed" {
		t.Fatalf("failure payload = %+v, want error and not removed", payload)
	}
}

func TestNewDelegationStartedEventWithType(t *testing.T) {
	event := NewDelegationStartedEventWithType("child-1", "inspect", "call-1", "model-a", "code")
	if event.Type != EventTypeDelegationStarted {
		t.Fatalf("Type = %q, want %q", event.Type, EventTypeDelegationStarted)
	}
	payload, ok := event.Payload.(DelegationStartedEvent)
	if !ok {
		t.Fatalf("Payload type = %T, want DelegationStartedEvent", event.Payload)
	}
	if payload.AgentID != "child-1" || payload.TaskPreview != "inspect" || payload.CallID != "call-1" || payload.ModelAlias != "model-a" || payload.AgentType != "code" {
		t.Errorf("payload = %+v, want child lifecycle fields", payload)
	}
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

func TestNewContextCompactionEvent(t *testing.T) {
	tests := []struct {
		name    string
		turn    int
		summary []string
		check   func(t *testing.T, event Event)
	}{
		{
			name:    "zero turn",
			turn:    0,
			summary: []string{},
			check: func(t *testing.T, event Event) {
				if event.Type != EventTypeContextDiagnostics {
					t.Fatalf("Type = %q, want %q", event.Type, EventTypeContextDiagnostics)
				}
				if event.Payload == nil {
					t.Fatal("Payload is nil")
				}
			},
		},
		{
			name:    "with turn and one summary",
			turn:    5,
			summary: []string{"summary text"},
			check: func(t *testing.T, event Event) {
				if event.Type != EventTypeContextDiagnostics {
					t.Fatalf("Type = %q", event.Type)
				}
				p, ok := event.Payload.(ContextCompactionEvent)
				if !ok {
					t.Fatalf("Payload type = %T", event.Payload)
				}
				if p.Turn != 5 {
					t.Fatalf("Turn = %d, want 5", p.Turn)
				}
				if p.SummaryPreview != "summary text" {
					t.Fatalf("SummaryPreview = %q, want %q", p.SummaryPreview, "summary text")
				}
			},
		},
		{
			name:    "with multiple summary args (only first used)",
			turn:    1,
			summary: []string{"first", "second", "third"},
			check: func(t *testing.T, event Event) {
				p, ok := event.Payload.(ContextCompactionEvent)
				if !ok {
					t.Fatalf("Payload type = %T", event.Payload)
				}
				if p.SummaryPreview != "first" {
					t.Fatalf("SummaryPreview = %q, want %q", p.SummaryPreview, "first")
				}
			},
		},
		{
			name:    "no summary args",
			turn:    2,
			summary: []string{},
			check: func(t *testing.T, event Event) {
				p, ok := event.Payload.(ContextCompactionEvent)
				if !ok {
					t.Fatalf("Payload type = %T", event.Payload)
				}
				if p.SummaryPreview != "" {
					t.Fatalf("SummaryPreview = %q, want empty", p.SummaryPreview)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := NewContextCompactionEvent(
				tt.turn, 0, 0, 0, 0, 0, false, "", tt.summary...,
			)
			tt.check(t, event)
		})
	}
}

func TestNewContextSessionHealthEvent(t *testing.T) {
	event := NewContextSessionHealthEvent("prompt", 3, 2, "warn", "degraded", "guidance", "note1", "note2")
	if event.Type != EventTypeContextDiagnostics {
		t.Fatalf("Type = %q, want %q", event.Type, EventTypeContextDiagnostics)
	}
	if event.Timestamp.IsZero() {
		t.Fatal("Timestamp is zero")
	}
	if event.Timestamp.Location() != time.UTC {
		t.Fatalf("Location = %v, want UTC", event.Timestamp.Location())
	}
	p, ok := event.Payload.(ContextSessionHealthEvent)
	if !ok {
		t.Fatalf("Payload type = %T", event.Payload)
	}
	if p.Scope != "prompt" {
		t.Fatalf("Scope = %q", p.Scope)
	}
	if p.Turn != 3 {
		t.Fatalf("Turn = %d", p.Turn)
	}
	if p.CompactionCount != 2 {
		t.Fatalf("CompactionCount = %d", p.CompactionCount)
	}
	if p.Severity != "warn" {
		t.Fatalf("Severity = %q", p.Severity)
	}
	if len(p.Notes) != 2 {
		t.Fatalf("Notes length = %d, want 2", len(p.Notes))
	}
}

func TestNewContextBudgetEvent(t *testing.T) {
	event := NewContextBudgetEvent("context", 5, 500, 1000, true, "note1")
	if event.Type != EventTypeContextDiagnostics {
		t.Fatalf("Type = %q", event.Type)
	}
	p, ok := event.Payload.(ContextBudgetEvent)
	if !ok {
		t.Fatalf("Payload type = %T", event.Payload)
	}
	if p.Scope != "context" {
		t.Fatalf("Scope = %q", p.Scope)
	}
	if p.Turn != 5 {
		t.Fatalf("Turn = %d", p.Turn)
	}
	if p.UsedBytes != 500 {
		t.Fatalf("UsedBytes = %d", p.UsedBytes)
	}
	if p.BudgetBytes != 1000 {
		t.Fatalf("BudgetBytes = %d", p.BudgetBytes)
	}
	if !p.Truncated {
		t.Error("Truncated should be true")
	}
	if len(p.Notes) != 1 {
		t.Fatalf("Notes length = %d, want 1", len(p.Notes))
	}
}

func TestNewContextTokenBudgetEvent(t *testing.T) {
	event := NewContextTokenBudgetEvent(
		"context", 3, 1000, 950, 4096, 50.0, 0.75, 100, 1050,
		"ok", false, "note1", "note2",
	)
	if event.Type != EventTypeContextDiagnostics {
		t.Fatalf("Type = %q", event.Type)
	}
	p, ok := event.Payload.(ContextBudgetEvent)
	if !ok {
		t.Fatalf("Payload type = %T", event.Payload)
	}
	if p.PromptTokens != 1000 {
		t.Fatalf("PromptTokens = %d", p.PromptTokens)
	}
	if p.ContextWindow != 4096 {
		t.Fatalf("ContextWindow = %d", p.ContextWindow)
	}
	if p.ContextUsagePercent != 50.0 {
		t.Fatalf("ContextUsagePercent = %v", p.ContextUsagePercent)
	}
	if len(p.Notes) != 2 {
		t.Fatalf("Notes length = %d", len(p.Notes))
	}
}

func TestNewFileAnnotationEvent(t *testing.T) {
	tests := []struct {
		name       string
		turn       int
		path       string
		action     string
		reason     string
		prevTurn   int
		notes      []string
		checkNotes func(t *testing.T, notes []string)
	}{
		{
			name:     "basic annotation",
			turn:     1,
			path:     "file.txt",
			action:   "skip",
			reason:   "too large",
			prevTurn: 0,
			notes:    []string{},
			checkNotes: func(t *testing.T, notes []string) {
				if len(notes) != 0 {
					t.Fatalf("Notes length = %d, want 0", len(notes))
				}
			},
		},
		{
			name:     "with previous turn",
			turn:     5,
			path:     "other.txt",
			action:   "read",
			reason:   "context",
			prevTurn: 3,
			notes:    []string{},
			checkNotes: func(t *testing.T, notes []string) {
				if len(notes) == 0 {
					t.Fatal("expected notes with previous_turn")
				}
				found := false
				for _, note := range notes {
					if note == "previous_turn=3" {
						found = true
						break
					}
				}
				if !found {
					t.Logf("notes = %v", notes)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := NewFileAnnotationEvent(tt.turn, tt.path, tt.action, tt.reason, tt.prevTurn, tt.notes...)
			if event.Type != EventTypeContextDiagnostics {
				t.Fatalf("Type = %q", event.Type)
			}
			p, ok := event.Payload.(ContextFileAnnotationEvent)
			if !ok {
				t.Fatalf("Payload type = %T", event.Payload)
			}
			if p.Path != tt.path {
				t.Fatalf("Path = %q", p.Path)
			}
			if p.Action != tt.action {
				t.Fatalf("Action = %q", p.Action)
			}
			tt.checkNotes(t, p.Notes)
		})
	}
}

func TestNewAssistantMessageEvent(t *testing.T) {
	event := NewAssistantMessageEvent(2, "assistant", "Hello, world!")
	if event.Type != EventTypeAssistantMessage {
		t.Fatalf("Type = %q", event.Type)
	}
	if event.Timestamp.IsZero() {
		t.Fatal("Timestamp is zero")
	}
	if event.Timestamp.Location() != time.UTC {
		t.Fatalf("Location = %v, want UTC", event.Timestamp.Location())
	}
	p, ok := event.Payload.(AssistantMessageEvent)
	if !ok {
		t.Fatalf("Payload type = %T", event.Payload)
	}
	if p.Turn != 2 {
		t.Fatalf("Turn = %d", p.Turn)
	}
	if p.Role != "assistant" {
		t.Fatalf("Role = %q", p.Role)
	}
	if p.Content != "Hello, world!" {
		t.Fatalf("Content = %q", p.Content)
	}
}

func TestNewThinkingChunkEventWithSource(t *testing.T) {
	event := NewThinkingChunkEventWithSource(1, "thinking...", ChunkSourceAssistant)
	if event.Type != EventTypeThinkingChunk {
		t.Fatalf("Type = %q", event.Type)
	}
	p, ok := event.Payload.(ThinkingChunkEvent)
	if !ok {
		t.Fatalf("Payload type = %T", event.Payload)
	}
	if p.Turn != 1 {
		t.Fatalf("Turn = %d", p.Turn)
	}
	if p.Content != "thinking..." {
		t.Fatalf("Content = %q", p.Content)
	}
	if p.Source != ChunkSourceAssistant {
		t.Fatalf("Source = %v", p.Source)
	}
}

func TestNewAssistantChunkEventWithSource(t *testing.T) {
	event := NewAssistantChunkEventWithSource(1, "chunk", ChunkSourceAssistant)
	if event.Type != EventTypeAssistantChunk {
		t.Fatalf("Type = %q", event.Type)
	}
	p, ok := event.Payload.(AssistantChunkEvent)
	if !ok {
		t.Fatalf("Payload type = %T", event.Payload)
	}
	if p.Turn != 1 {
		t.Fatalf("Turn = %d", p.Turn)
	}
	if p.Content != "chunk" {
		t.Fatalf("Content = %q", p.Content)
	}
}

func TestNewStopReasonEvent(t *testing.T) {
	tests := []struct {
		name      string
		turn      int
		reason    string
		err       error
		checkText func(t *testing.T, summary, action string)
	}{
		{
			name:   "complete with turn",
			turn:   5,
			reason: "complete",
			err:    nil,
			checkText: func(t *testing.T, summary, _ string) {
				if summary == "" {
					t.Error("summary should not be empty")
				}
				if !contains(summary, "5") {
					t.Error("summary should contain turn count")
				}
			},
		},
		{
			name:   "max_turns",
			turn:   10,
			reason: "max_turns",
			err:    nil,
			checkText: func(t *testing.T, summary, _ string) {
				if !contains(summary, "turn limit") {
					t.Error("summary should mention turn limit")
				}
			},
		},
		{
			name:   "cancelled",
			turn:   3,
			reason: "cancelled",
			err:    nil,
			checkText: func(t *testing.T, summary, _ string) {
				if !contains(summary, "cancelled") {
					t.Error("summary should mention cancelled")
				}
			},
		},
		{
			name:   "error reason with error",
			turn:   1,
			reason: "error",
			err:    errors.New("test error"),
			checkText: func(t *testing.T, summary, _ string) {
				if summary == "" {
					t.Error("summary should not be empty")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := NewStopReasonEvent(tt.turn, tt.reason, tt.err)
			if event.Type != EventTypeStopReason {
				t.Fatalf("Type = %q", event.Type)
			}
			p, ok := event.Payload.(StopReasonEvent)
			if !ok {
				t.Fatalf("Payload type = %T", event.Payload)
			}
			tt.checkText(t, p.Summary, p.Action)
			if tt.err != nil && p.Error == "" {
				t.Error("Error should not be empty")
			}
		})
	}
}

func TestWithAgentScopeModifier(t *testing.T) {
	event := NewAssistantMessageEvent(1, "test", "content")
	if event.Scope.AgentID != "" {
		t.Fatal("initial AgentID should be empty")
	}
	event = WithAgentScope(event, "agent-1")
	if event.Scope.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1", event.Scope.AgentID)
	}
	// Empty agent ID should be a no-op
	event2 := WithAgentScope(event, "")
	if event2.Scope.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1 (unchanged)", event2.Scope.AgentID)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
