package agent

import (
	"strings"
	"testing"
	"time"
)

func TestRenderSubAgentResultEnvelopeRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label string
		c     SubAgentCompletion
	}{
		{
			label: "plain",
			c: SubAgentCompletion{
				AgentID:          "code-1",
				AgentType:        "code",
				Status:           "ok",
				ParentCallID:     "call-123",
				ObjectivePreview: "Implement feature X",
				TurnCount:        5,
				TokenCount:       2000,
				Duration:         3500 * time.Millisecond,
				Body:             `{"output":"success","status":"ok"}`,
			},
		},
		{
			label: "body with close tag",
			c: SubAgentCompletion{
				AgentID:          "code-1",
				AgentType:        "code",
				Status:           "ok",
				ParentCallID:     "call-123",
				ObjectivePreview: "Test",
				TurnCount:        1,
				TokenCount:       100,
				Duration:         1 * time.Second,
				Body:             `body contains literal </steiner-sub-agent-result> inside`,
			},
		},
		{
			label: "attributes with quotes and backslashes",
			c: SubAgentCompletion{
				AgentID:          `agent"id\test`,
				AgentType:        `type"with\slash`,
				Status:           `sta"tus\back`,
				ParentCallID:     `call"id\slash`,
				ObjectivePreview: "Objective",
				TurnCount:        1,
				TokenCount:       50,
				Duration:         100 * time.Millisecond,
				Body:             `{"status":"ok"}`,
			},
		},
		{
			label: "empty body",
			c: SubAgentCompletion{
				AgentID:          "code-2",
				AgentType:        "code",
				Status:           "empty",
				ParentCallID:     "call-456",
				ObjectivePreview: "Empty result",
				TurnCount:        0,
				TokenCount:       0,
				Duration:         0,
				Body:             "",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			text := RenderSubAgentResultEnvelope(tc.c)

			// Parse back
			parsed, ok := ParseSubAgentResultEnvelope(text)
			if !ok {
				t.Fatalf("ParseSubAgentResultEnvelope failed")
			}

			// Verify structure and attributes round-trip
			if parsed.AgentID != tc.c.AgentID {
				t.Errorf("AgentID = %q, want %q", parsed.AgentID, tc.c.AgentID)
			}
			if parsed.AgentType != tc.c.AgentType {
				t.Errorf("AgentType = %q, want %q", parsed.AgentType, tc.c.AgentType)
			}
			if parsed.Status != tc.c.Status {
				t.Errorf("Status = %q, want %q", parsed.Status, tc.c.Status)
			}
			if parsed.CallID != tc.c.ParentCallID {
				t.Errorf("CallID = %q, want %q", parsed.CallID, tc.c.ParentCallID)
			}

			// Verify inner content structure and body is preserved
			if !strings.Contains(parsed.Inner, "This is a sub-agent result") {
				t.Error("Inner missing framing text")
			}
			if !strings.Contains(parsed.Inner, tc.c.ObjectivePreview) {
				t.Errorf("Inner missing objective %q", tc.c.ObjectivePreview)
			}
			if !strings.Contains(parsed.Inner, tc.c.Body) {
				t.Errorf("Inner missing body %q", tc.c.Body)
			}

			// Verify byte length matches
			if len(parsed.Inner) != len(text)-strings.Index(text, "\n")-1-len("\n</steiner-sub-agent-result>") {
				t.Errorf("bytes= mismatch for %s", tc.label)
			}
		})
	}
}

func TestRenderPendingSubAgentsLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label    string
		pending  []PendingSubAgent
		wantLine string
	}{
		{
			label:    "empty",
			pending:  nil,
			wantLine: "",
		},
		{
			label:    "empty slice",
			pending:  []PendingSubAgent{},
			wantLine: "",
		},
		{
			label: "single agent",
			pending: []PendingSubAgent{
				{AgentID: "code-1", AgentType: "code", State: SubAgentRunning},
			},
			wantLine: "<steiner-sub-agents-pending>code-1 (code, running)</steiner-sub-agents-pending>",
		},
		{
			label: "multiple agents in order",
			pending: []PendingSubAgent{
				{AgentID: "code-3", AgentType: "code", State: SubAgentRunning},
				{AgentID: "review-5", AgentType: "review", State: SubAgentQueued},
				{AgentID: "explore-7", AgentType: "explore", State: SubAgentFinished},
			},
			wantLine: "<steiner-sub-agents-pending>code-3 (code, running); review-5 (review, queued); explore-7 (explore, finished)</steiner-sub-agents-pending>",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			got := RenderPendingSubAgentsLine(tc.pending)
			if got != tc.wantLine {
				t.Errorf("got %q, want %q", got, tc.wantLine)
			}
		})
	}
}

func TestLostSubAgentCompletion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label string
		entry SubAgentLedgerEntry
		check func(t *testing.T, c SubAgentCompletion)
	}{
		{
			label: "without worktree path",
			entry: SubAgentLedgerEntry{
				AgentID:      "code-1",
				AgentType:    "code",
				ParentCallID: "call-xyz",
			},
			check: func(t *testing.T, c SubAgentCompletion) {
				if c.Status != "lost" {
					t.Errorf("Status = %q, want lost", c.Status)
				}
				if !c.Quiet {
					t.Error("Quiet should be true")
				}
				if !strings.Contains(c.Body, "lost") || !strings.Contains(c.Body, "session restarted") {
					t.Errorf("Body missing expected text: %q", c.Body)
				}
				if strings.Contains(c.Body, "/") {
					t.Error("Body should not contain worktree path")
				}
			},
		},
		{
			label: "with worktree path",
			entry: SubAgentLedgerEntry{
				AgentID:      "code-2",
				AgentType:    "code",
				ParentCallID: "call-abc",
				WorktreePath: "/path/to/worktree",
			},
			check: func(t *testing.T, c SubAgentCompletion) {
				if c.Status != "lost" {
					t.Errorf("Status = %q, want lost", c.Status)
				}
				if !c.Quiet {
					t.Error("Quiet should be true")
				}
				if !strings.Contains(c.Body, "/path/to/worktree") {
					t.Errorf("Body missing worktree path: %q", c.Body)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			c := LostSubAgentCompletion(tc.entry)
			if c.AgentID != tc.entry.AgentID {
				t.Errorf("AgentID mismatch")
			}
			if c.AgentType != tc.entry.AgentType {
				t.Errorf("AgentType mismatch")
			}
			if c.ParentCallID != tc.entry.ParentCallID {
				t.Errorf("ParentCallID mismatch")
			}
			tc.check(t, c)
		})
	}
}

func TestBuildDeliveryMessage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label   string
		parts   DeliveryParts
		wantOK  bool
		wantSrc string
		check   func(t *testing.T, m Message)
	}{
		{
			label:   "empty parts",
			parts:   DeliveryParts{},
			wantOK:  false,
			wantSrc: "",
		},
		{
			label: "results only",
			parts: DeliveryParts{
				Completions: []SubAgentCompletion{
					{
						Seq:              1,
						AgentID:          "code-1",
						AgentType:        "code",
						Status:           "ok",
						ParentCallID:     "call-1",
						ObjectivePreview: "Task 1",
						TurnCount:        2,
						TokenCount:       500,
						Duration:         1 * time.Second,
						Body:             `{"status":"ok"}`,
					},
				},
			},
			wantOK:  true,
			wantSrc: MessageSourceSubAgentResult,
			check: func(t *testing.T, m Message) {
				if len(m.Images) != 0 {
					t.Error("expected no images")
				}
				if !strings.Contains(m.Content, "code-1") {
					t.Error("content should contain agent ID")
				}
			},
		},
		{
			label: "results with pending only",
			parts: DeliveryParts{
				Pending: []PendingSubAgent{
					{AgentID: "code-2", AgentType: "code", State: SubAgentRunning},
				},
				Completions: []SubAgentCompletion{
					{
						Seq:              1,
						AgentID:          "code-1",
						AgentType:        "code",
						Status:           "ok",
						ParentCallID:     "call-1",
						ObjectivePreview: "Done",
						TurnCount:        1,
						TokenCount:       100,
						Duration:         0,
						Body:             `{}`,
					},
				},
			},
			wantOK:  true,
			wantSrc: MessageSourceSubAgentResult,
			check: func(t *testing.T, m Message) {
				if !strings.Contains(m.Content, "steiner-sub-agents-pending") {
					t.Error("content should contain pending line")
				}
			},
		},
		{
			label: "results plus user text",
			parts: DeliveryParts{
				Completions: []SubAgentCompletion{
					{
						Seq:              1,
						AgentID:          "code-1",
						AgentType:        "code",
						Status:           "ok",
						ParentCallID:     "call-1",
						ObjectivePreview: "Done",
						TurnCount:        1,
						TokenCount:       100,
						Duration:         0,
						Body:             `{}`,
					},
				},
				UserText: "Here is some user feedback",
			},
			wantOK:  true,
			wantSrc: "", // real user message
			check: func(t *testing.T, m Message) {
				if m.Source != "" {
					t.Errorf("Source should be empty, got %q", m.Source)
				}
				if !strings.Contains(m.Content, "Here is some user feedback") {
					t.Error("content should contain user text")
				}
			},
		},
		{
			label: "user text only",
			parts: DeliveryParts{
				UserText: "Just a user message",
			},
			wantOK:  true,
			wantSrc: "",
			check: func(t *testing.T, m Message) {
				if m.Source != "" {
					t.Error("Source should be empty for text-only message")
				}
				if m.Content != "Just a user message" {
					t.Errorf("Content mismatch: %q", m.Content)
				}
			},
		},
		{
			label: "pending only",
			parts: DeliveryParts{
				Pending: []PendingSubAgent{
					{AgentID: "code-1", AgentType: "code", State: SubAgentRunning},
				},
			},
			wantOK:  true,
			wantSrc: MessageSourceSubAgentResult,
			check: func(t *testing.T, m Message) {
				if !strings.Contains(m.Content, "steiner-sub-agents-pending") {
					t.Error("content should contain pending line")
				}
			},
		},
		{
			label: "completions out of sequence order get sorted",
			parts: DeliveryParts{
				Completions: []SubAgentCompletion{
					{Seq: 3, AgentID: "a", ParentCallID: "c3", AgentType: "t", ObjectivePreview: "O3", Body: "B3"},
					{Seq: 1, AgentID: "b", ParentCallID: "c1", AgentType: "t", ObjectivePreview: "O1", Body: "B1"},
					{Seq: 2, AgentID: "c", ParentCallID: "c2", AgentType: "t", ObjectivePreview: "O2", Body: "B2"},
				},
			},
			wantOK:  true,
			wantSrc: MessageSourceSubAgentResult,
			check: func(t *testing.T, m Message) {
				// Verify order: look for the agent IDs in order
				idx1 := strings.Index(m.Content, "agent_id=\"b\"")
				idx2 := strings.Index(m.Content, "agent_id=\"c\"")
				idx3 := strings.Index(m.Content, "agent_id=\"a\"")
				if idx1 > idx2 || idx2 > idx3 {
					t.Error("completions not in Seq order")
				}
			},
		},
		{
			label: "results with images (real user message)",
			parts: DeliveryParts{
				Completions: []SubAgentCompletion{
					{
						Seq:              1,
						AgentID:          "code-1",
						AgentType:        "code",
						Status:           "ok",
						ParentCallID:     "call-1",
						ObjectivePreview: "Done",
						TurnCount:        1,
						TokenCount:       100,
						Duration:         0,
						Body:             `{}`,
					},
				},
				Images: []ImageBlock{{ID: "img-1"}},
			},
			wantOK:  true,
			wantSrc: "", // has images, so not pure sub_agent_result
			check: func(t *testing.T, m Message) {
				if len(m.Images) == 0 {
					t.Error("Images not preserved")
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			m, ok := BuildDeliveryMessage(tc.parts)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}

			if m.Role != MessageRoleUser {
				t.Errorf("Role = %q, want user", m.Role)
			}
			if m.Source != tc.wantSrc {
				t.Errorf("Source = %q, want %q", m.Source, tc.wantSrc)
			}
			tc.check(t, m)
		})
	}
}

func TestIsRealUserMessage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label string
		m     Message
		want  bool
	}{
		{
			label: "user message with no source",
			m:     Message{Role: MessageRoleUser, Source: ""},
			want:  true,
		},
		{
			label: "user message with other source",
			m:     Message{Role: MessageRoleUser, Source: "some_other_source"},
			want:  true,
		},
		{
			label: "user message as sub_agent_result",
			m:     Message{Role: MessageRoleUser, Source: MessageSourceSubAgentResult},
			want:  false,
		},
		{
			label: "assistant message",
			m:     Message{Role: MessageRoleAssistant, Source: ""},
			want:  false,
		},
		{
			label: "tool message",
			m:     Message{Role: MessageRoleTool, Source: ""},
			want:  false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			got := IsRealUserMessage(tc.m)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseSubAgentResultEnvelopeErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label string
		raw   string
	}{
		{
			label: "missing open tag",
			raw:   "not an envelope",
		},
		{
			label: "truncated at size",
			raw:   `<steiner-sub-agent-result agent_id="a" type="b" status="c" call_id="d" bytes="999">`,
		},
		{
			label: "bad size value",
			raw:   `<steiner-sub-agent-result agent_id="a" type="b" status="c" call_id="d" bytes="abc">body</steiner-sub-agent-result>`,
		},
		{
			label: "missing close tag",
			raw:   `<steiner-sub-agent-result agent_id="a" type="b" status="c" call_id="d" bytes="4">body`,
		},
		{
			label: "size mismatch (too small)",
			raw:   `<steiner-sub-agent-result agent_id="a" type="b" status="c" call_id="d" bytes="2">body</steiner-sub-agent-result>`,
		},
		{
			label: "extra content after close tag",
			raw:   `<steiner-sub-agent-result agent_id="a" type="b" status="c" call_id="d" bytes="4">body</steiner-sub-agent-result>extra`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			_, ok := ParseSubAgentResultEnvelope(tc.raw)
			if ok {
				t.Errorf("expected failure for %q", tc.label)
			}
		})
	}
}
