package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// TestApprovalOutsidePathsHelper pins the shared helper both approval cards
// use: sanitizing each path onto one line, wrapping to the available width,
// and counting whatever the collected list leaves out.
func TestApprovalOutsidePathsHelper(t *testing.T) {
	t.Parallel()
	style := testStyles(theme.AccentAmber).FgMute

	tests := []struct {
		name     string
		paths    []string
		count    int
		width    int
		wantNil  bool
		want     []string // expected plain lines, trimmed, when exact
		minLines int      // minimum line count when the wrap points are not pinned
	}{
		{
			name:    "empty list and zero count adds nothing",
			width:   40,
			wantNil: true,
		},
		{
			name:    "non-positive width adds nothing",
			paths:   []string{"/outside/a.txt"},
			count:   1,
			width:   0,
			wantNil: true,
		},
		{
			name:  "newline, tab and carriage return stay on one line",
			paths: []string{"/outside/a\nspoofed\tb\r.txt"},
			count: 1,
			width: 60,
			want:  []string{"/outside/a spoofed b.txt"},
		},
		{
			name:  "escape sequence is stripped",
			paths: []string{"/outside/\x1b[31mred\x1b[0m/x.txt", "\x1b]0;pwned\x07"},
			count: 2,
			width: 60,
			want:  []string{"/outside/red/x.txt", "and 1 more outside the project"},
		},
		{
			name:     "long path wraps to the width",
			paths:    []string{"/outside/a/very/long/nested/path/file.txt"},
			count:    1,
			width:    12,
			minLines: 2,
		},
		{
			name:  "paths beyond the collected list are counted",
			paths: []string{"/outside/a.txt", "/outside/b.txt"},
			count: 5,
			width: 60,
			want:  []string{"/outside/a.txt", "/outside/b.txt", "and 3 more outside the project"},
		},
		{
			name:  "no remainder line when the list is complete",
			paths: []string{"/outside/a.txt", "/outside/b.txt"},
			count: 2,
			width: 60,
			want:  []string{"/outside/a.txt", "/outside/b.txt"},
		},
		{
			name:  "empty list with a count reports the remainder",
			count: 4,
			width: 60,
			want:  []string{"and 4 more outside the project"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := approvalOutsidePaths(tt.paths, tt.count, tt.width, style)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("approvalOutsidePaths(%v, %d, %d) = %q, want nil", tt.paths, tt.count, tt.width, got)
				}
				return
			}
			if len(got) < max(1, tt.minLines) {
				t.Fatalf("line count = %d, want at least %d (%q)", len(got), max(1, tt.minLines), got)
			}
			for _, line := range got {
				if strings.ContainsRune(line, '\n') {
					t.Errorf("line %q contains a newline", line)
				}
				if gotWidth := lipgloss.Width(line); gotWidth > tt.width {
					t.Errorf("line width = %d, want <= %d for %q", gotWidth, tt.width, stripANSI(line))
				}
			}
			requireOnlySGR(t, "approval outside paths", strings.Join(got, "\n"))
			if tt.want == nil {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("lines = %q, want %q", got, tt.want)
			}
			for i, want := range tt.want {
				if plain := strings.TrimSpace(stripANSI(got[i])); plain != want {
					t.Errorf("line %d = %q, want %q", i, plain, want)
				}
			}
		})
	}
}

// approvalEventModel builds a model whose content buffer renders approvals
// through the production event route.
func approvalEventModel(t *testing.T) *Model {
	t.Helper()
	return newModel(Config{}, nil)
}

func mutateApprovalRequest(requestID string, deniedPaths []string, count int) output.Event {
	return output.NewApprovalRequestedEvent(output.ApprovalEventParams{
		Turn:            1,
		Tool:            "mutate",
		CallID:          "call-" + requestID,
		RequestID:       requestID,
		Mode:            "paths outside the project root",
		Preview:         `{"path":"a.txt"}`,
		Kind:            "path",
		DeniedPaths:     deniedPaths,
		DeniedPathCount: count,
	})
}

// requireRenderedWidths fails when any rendered line is wider than the terminal
// width the transcript was rendered at.
func requireRenderedWidths(t *testing.T, label, rendered string, width int) {
	t.Helper()
	for _, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("%s line width = %d, want <= %d for %q", label, got, width, stripANSI(line))
		}
	}
}

// TestApprovalEmbeddedBlockListsOutsidePaths drives a batch mutate approval
// whose tool call is on screen and checks the embedded card names the outside
// paths and the remainder count.
func TestApprovalEmbeddedBlockListsOutsidePaths(t *testing.T) {
	t.Parallel()
	const width = 80
	m := approvalEventModel(t)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "mutate", "call-req-embedded", map[string]any{"path": "a.txt"})})
	m = updateModel(t, m, runtimeEventMsg{Event: mutateApprovalRequest("req-embedded", []string{"/outside/one.txt", "/outside/two.txt"}, 4)})

	rendered := m.content.String(width)
	plain := stripANSI(rendered)
	if !strings.Contains(plain, "APPROVAL REQUIRED") {
		t.Fatalf("rendered card = %q, want the embedded approval block", plain)
	}
	for _, want := range []string{"outside paths:", "/outside/one.txt", "/outside/two.txt", "and 2 more outside the project"} {
		if !strings.Contains(plain, want) {
			t.Errorf("rendered card missing %q:\n%s", want, plain)
		}
	}
	requireOnlySGR(t, "embedded approval card", rendered)
	requireRenderedWidths(t, "embedded approval card", rendered, width)
}

// TestApprovalPillListsOutsidePaths covers the standalone pill: a request with
// no matching tool call on screen still lists the outside paths.
func TestApprovalPillListsOutsidePaths(t *testing.T) {
	t.Parallel()
	const width = 80
	m := approvalEventModel(t)
	m = updateModel(t, m, runtimeEventMsg{Event: mutateApprovalRequest("req-pill", []string{"/outside/pill-one.txt", "/outside/pill-two.txt"}, 3)})

	rendered := m.content.String(width)
	plain := stripANSI(rendered)
	for _, want := range []string{"mutate", "outside paths:", "/outside/pill-one.txt", "/outside/pill-two.txt", "and 1 more outside the project"} {
		if !strings.Contains(plain, want) {
			t.Errorf("rendered pill missing %q:\n%s", want, plain)
		}
	}
	requireOnlySGR(t, "approval pill", rendered)
	requireRenderedWidths(t, "approval pill", rendered, width)
}

// TestApprovalGroupedEntryListsOutsidePaths covers an approval embedded in one
// entry of a grouped tool-call segment.
func TestApprovalGroupedEntryListsOutsidePaths(t *testing.T) {
	t.Parallel()
	const width = 80
	m := approvalEventModel(t)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "mutate", "call-first", map[string]any{"path": "first.txt"})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "mutate", "call-req-group", map[string]any{"path": "second.txt"})})
	// One path carries an SGR sequence and the other an OSC sequence: the
	// grouped renderer strips both before the card reaches the terminal.
	m = updateModel(t, m, runtimeEventMsg{Event: mutateApprovalRequest("req-group", []string{"/outside/\x1b[31mgroup-a\x1b[0m.txt", "\x1b]0;pwned\x07/outside/group-b.txt"}, 3)})

	if len(m.content.segments) != 1 || m.content.segments[0].kind != segmentToolCallGroup {
		t.Fatalf("segments = %#v, want one tool-call group", m.content.segments)
	}
	rendered := m.content.String(width)
	plain := stripANSI(rendered)
	for _, want := range []string{"outside paths:", "/outside/group-a.txt", "/outside/group-b.txt", "and 1 more outside the project"} {
		if !strings.Contains(plain, want) {
			t.Errorf("rendered group missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "pwned") {
		t.Errorf("rendered group kept an OSC payload:\n%s", plain)
	}
	requireOnlySGR(t, "grouped approval card", rendered)
	requireRenderedWidths(t, "grouped approval card", rendered, width)
}

// TestApprovalWithoutOutsidePathsRendersNoBlock pins that approvals without a
// path list, and MCP approvals, keep their existing card.
func TestApprovalWithoutOutsidePathsRendersNoBlock(t *testing.T) {
	t.Parallel()
	const width = 80

	t.Run("non-path approval", func(t *testing.T) {
		t.Parallel()
		m := approvalEventModel(t)
		m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call-bash", map[string]any{"command": "ls"})})
		m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{
			Turn: 1, Tool: "bash", CallID: "call-bash", RequestID: "req-bash", Mode: "prompt", Preview: `{"command":"ls"}`,
		})})

		rendered := stripANSI(m.content.String(width))
		for _, unwanted := range []string{"outside paths:", "more outside the project"} {
			if strings.Contains(rendered, unwanted) {
				t.Errorf("non-path approval rendered %q:\n%s", unwanted, rendered)
			}
		}
		for _, want := range []string{"APPROVAL REQUIRED", "Allow once"} {
			if !strings.Contains(rendered, want) {
				t.Errorf("non-path approval missing %q:\n%s", want, rendered)
			}
		}
	})

	t.Run("mcp pill", func(t *testing.T) {
		t.Parallel()
		m := approvalEventModel(t)
		m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{
			Turn: 1, Tool: "mcp__srv__search", CallID: "call-mcp", RequestID: "req-mcp", Mode: "prompt",
			Preview:     `{"query":"x"}`,
			Kind:        "mcp",
			Server:      "srv",
			ToolName:    "search",
			DeniedPaths: []string{"/outside/mcp.txt"},
		})})

		rendered := stripANSI(m.content.String(width))
		if strings.Contains(rendered, "outside paths:") {
			t.Errorf("MCP pill rendered an outside paths block:\n%s", rendered)
		}
		if !strings.Contains(rendered, "srv → search") {
			t.Errorf("MCP pill missing its label:\n%s", rendered)
		}
	})
}

// TestPromoteNextApprovalKeepsOutsidePaths covers each promotion source: the
// promoted request must keep the paths collected for it, or the card loses them
// as soon as it reaches the tray.
func TestPromoteNextApprovalKeepsOutsidePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		build     func() *Model
		wantPaths []string
		wantCount int
	}{
		{
			name: "single tool call",
			build: func() *Model {
				m := approvalEventModel(t)
				m.content.segments = []contentSegment{{kind: segmentToolCall, toolData: &toolCallSegment{
					tool:                    "mutate",
					callID:                  "call-tool",
					approvalPending:         true,
					approvalIdentity:        "req-tool",
					approvalDeniedPaths:     []string{"/outside/tool.txt"},
					approvalDeniedPathCount: 2,
				}}}
				return m
			},
			wantPaths: []string{"/outside/tool.txt"},
			wantCount: 2,
		},
		{
			name: "grouped tool call entry",
			build: func() *Model {
				m := approvalEventModel(t)
				m.content.segments = []contentSegment{{kind: segmentToolCallGroup, toolGroupData: &toolCallGroupSegment{entries: []*toolCallSegment{
					{tool: "mutate", callID: "call-group", approvalPending: true, approvalIdentity: "req-group", approvalDeniedPaths: []string{"/outside/group.txt"}, approvalDeniedPathCount: 3},
				}}}}
				return m
			},
			wantPaths: []string{"/outside/group.txt"},
			wantCount: 3,
		},
		{
			name: "approval pill",
			build: func() *Model {
				m := approvalEventModel(t)
				m.content.segments = []contentSegment{{kind: segmentApprovalPill, approvalData: &approvalPillData{
					tool:            "mutate",
					identity:        "req-pill",
					deniedPaths:     []string{"/outside/pill.txt"},
					deniedPathCount: 4,
				}}}
				return m
			},
			wantPaths: []string{"/outside/pill.txt"},
			wantCount: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := tt.build()
			if !m.promoteNextApproval() {
				t.Fatal("promoteNextApproval reported no promotable request")
			}
			if len(m.approval.deniedPaths) != len(tt.wantPaths) {
				t.Fatalf("promoted paths = %v, want %v", m.approval.deniedPaths, tt.wantPaths)
			}
			for i, want := range tt.wantPaths {
				if got := m.approval.deniedPaths[i]; got != want {
					t.Errorf("promoted path %d = %q, want %q", i, got, want)
				}
			}
			if got := m.approval.deniedPathCount; got != tt.wantCount {
				t.Errorf("promoted path count = %d, want %d", got, tt.wantCount)
			}
		})
	}
}

// TestApprovalDecisionCarriesOutsidePaths covers a decision event that reports
// the outside paths: the embedded control keeps them for the rest of the run.
func TestApprovalDecisionCarriesOutsidePaths(t *testing.T) {
	t.Parallel()
	m := approvalEventModel(t)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "mutate", "call-decision", map[string]any{"path": "a.txt"})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{
		Turn: 1, Tool: "mutate", CallID: "call-decision", RequestID: "req-decision", Mode: "paths outside the project root", Kind: "path",
	})})

	control := findApprovalControl(&m.content, "req-decision")
	if control == nil {
		t.Fatal("no approval control for the request")
	}
	if len(control.approvalDeniedPaths) != 0 {
		t.Fatalf("request paths = %v, want none before the decision", control.approvalDeniedPaths)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(output.ApprovalEventParams{
		Turn: 1, Tool: "mutate", CallID: "call-decision", RequestID: "req-decision", Mode: "paths outside the project root", Kind: "path",
		Message:         "approved",
		DeniedPaths:     []string{"/outside/decided.txt"},
		DeniedPathCount: 2,
	})})

	control = findApprovalControl(&m.content, "req-decision")
	if control == nil {
		t.Fatal("approval control disappeared after the decision")
	}
	if got, want := control.approvalDeniedPaths, []string{"/outside/decided.txt"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("control paths = %v, want %v", got, want)
	}
	if got, want := control.approvalDeniedPathCount, 2; got != want {
		t.Fatalf("control path count = %d, want %d", got, want)
	}
}

// TestApprovalGroupedDecisionCarriesOutsidePaths covers the group branch of
// applyApprovalDecisionPaths: a decision that is the first event to report the
// outside paths must put both fields on the grouped entry that raised the
// request, and on no other entry in the group.
func TestApprovalGroupedDecisionCarriesOutsidePaths(t *testing.T) {
	t.Parallel()
	m := approvalEventModel(t)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "mutate", "call-group-first", map[string]any{"path": "first.txt"})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "mutate", "call-req-group-decided", map[string]any{"path": "second.txt"})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{
		Turn: 1, Tool: "mutate", CallID: "call-req-group-decided", RequestID: "req-group-decided",
		Mode: "paths outside the project root", Preview: `{"path":"second.txt"}`, Kind: "path",
	})})

	if len(m.content.segments) != 1 || m.content.segments[0].kind != segmentToolCallGroup || m.content.segments[0].toolGroupData == nil {
		t.Fatalf("segments = %#v, want one tool-call group", m.content.segments)
	}
	control := findApprovalControl(&m.content, "req-group-decided")
	if control == nil {
		t.Fatal("no grouped approval control for the request")
	}
	if len(control.approvalDeniedPaths) != 0 || control.approvalDeniedPathCount != 0 {
		t.Fatalf("grouped request paths = %v/%d, want none before the decision", control.approvalDeniedPaths, control.approvalDeniedPathCount)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(output.ApprovalEventParams{
		Turn: 1, Tool: "mutate", CallID: "call-req-group-decided", RequestID: "req-group-decided",
		Mode: "paths outside the project root", Kind: "path", Message: "approved",
		DeniedPaths:     []string{"/outside/group-decided.txt"},
		DeniedPathCount: 2,
	})})

	segment := &m.content.segments[0]
	if segment.kind != segmentToolCallGroup || segment.toolGroupData == nil {
		t.Fatalf("segment = %#v, want the tool-call group", segment)
	}
	wantPaths := []string{"/outside/group-decided.txt"}
	matched := 0
	for _, entry := range segment.toolGroupData.entries {
		if entry == nil {
			continue
		}
		if entry.approvalIdentity != "req-group-decided" {
			if len(entry.approvalDeniedPaths) != 0 || entry.approvalDeniedPathCount != 0 {
				t.Errorf("entry %q paths = %v/%d, want none", entry.callID, entry.approvalDeniedPaths, entry.approvalDeniedPathCount)
			}
			continue
		}
		matched++
		if len(entry.approvalDeniedPaths) != len(wantPaths) {
			t.Errorf("grouped control paths = %v, want %v", entry.approvalDeniedPaths, wantPaths)
			continue
		}
		for i, want := range wantPaths {
			if got := entry.approvalDeniedPaths[i]; got != want {
				t.Errorf("grouped control path %d = %q, want %q", i, got, want)
			}
		}
		if got := entry.approvalDeniedPathCount; got != 2 {
			t.Errorf("grouped control path count = %d, want 2", got)
		}
	}
	if matched != 1 {
		t.Fatalf("matched %d grouped controls, want exactly 1", matched)
	}
	if !segment.renderDirty {
		t.Error("group segment = clean, want renderDirty so the card rerenders")
	}
}

// TestApprovalDecisionWithoutPathsKeepsRequestPaths pins the guard at the top
// of applyApprovalDecisionPaths: a decision event that carries no paths leaves
// the ones the request recorded on the control and in the tray.
func TestApprovalDecisionWithoutPathsKeepsRequestPaths(t *testing.T) {
	t.Parallel()

	t.Run("embedded control", func(t *testing.T) {
		t.Parallel()
		m := approvalEventModel(t)
		m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "mutate", "call-req-keep", map[string]any{"path": "a.txt"})})
		m = updateModel(t, m, runtimeEventMsg{Event: mutateApprovalRequest("req-keep", []string{"/outside/keep-a.txt", "/outside/keep-b.txt"}, 5)})

		control := findApprovalControl(&m.content, "req-keep")
		if control == nil {
			t.Fatal("no approval control for the request")
		}
		if len(control.approvalDeniedPaths) != 2 || control.approvalDeniedPathCount != 5 {
			t.Fatalf("request paths = %v/%d, want the two paths and count 5", control.approvalDeniedPaths, control.approvalDeniedPathCount)
		}

		m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(output.ApprovalEventParams{
			Turn: 1, Tool: "mutate", CallID: "call-req-keep", RequestID: "req-keep",
			Mode: "paths outside the project root", Kind: "path", Message: "approved",
		})})

		control = findApprovalControl(&m.content, "req-keep")
		if control == nil {
			t.Fatal("approval control disappeared after the decision")
		}
		wantPaths := []string{"/outside/keep-a.txt", "/outside/keep-b.txt"}
		if len(control.approvalDeniedPaths) != len(wantPaths) {
			t.Fatalf("control paths = %v, want %v", control.approvalDeniedPaths, wantPaths)
		}
		for i, want := range wantPaths {
			if got := control.approvalDeniedPaths[i]; got != want {
				t.Errorf("control path %d = %q, want %q", i, got, want)
			}
		}
		if got := control.approvalDeniedPathCount; got != 5 {
			t.Errorf("control path count = %d, want 5", got)
		}
	})

	// A pathless decision for a queued request must not clear the tray of the
	// request it did not resolve.
	t.Run("active tray", func(t *testing.T) {
		t.Parallel()
		m := newModel(Config{Controller: approvalHeadController{head: "req-tray-a"}}, nil)
		m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "mutate", "call-req-tray-a", map[string]any{"path": "a.txt"})})
		m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "mutate", "call-req-tray-b", map[string]any{"path": "b.txt"})})
		m = updateModel(t, m, runtimeEventMsg{Event: mutateApprovalRequest("req-tray-a", []string{"/outside/tray-a.txt"}, 3)})
		m = updateModel(t, m, runtimeEventMsg{Event: mutateApprovalRequest("req-tray-b", nil, 0)})

		if !m.approval.active || m.approval.identity != "req-tray-a" {
			t.Fatalf("active approval = %#v, want req-tray-a prompting", m.approval)
		}
		m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(output.ApprovalEventParams{
			Turn: 1, Tool: "mutate", CallID: "call-req-tray-b", RequestID: "req-tray-b",
			Mode: "paths outside the project root", Kind: "path", Message: "approved",
		})})

		if got, want := m.approval.deniedPaths, []string{"/outside/tray-a.txt"}; len(got) != 1 || got[0] != want[0] {
			t.Fatalf("tray paths = %v, want %v", got, want)
		}
		if got, want := m.approval.deniedPathCount, 3; got != want {
			t.Fatalf("tray path count = %d, want %d", got, want)
		}
	})
}

// requireTextOrder fails when any anchor is missing from text or the anchors do
// not appear in the given left-to-right order.
func requireTextOrder(t *testing.T, label, text string, anchors ...string) {
	t.Helper()
	previous, previousAnchor := -1, ""
	for _, anchor := range anchors {
		index := strings.Index(text, anchor)
		if index < 0 {
			t.Fatalf("%s missing %q:\n%s", label, anchor, text)
		}
		if index < previous {
			t.Fatalf("%s puts %q at %d, before %q at %d:\n%s", label, anchor, index, previousAnchor, previous, text)
		}
		previous, previousAnchor = index, anchor
	}
}

// TestApprovalCardsOrderOutsidePaths pins where each card renders the outside
// paths block relative to its other lines: after the arguments preview and the
// no-persist warning, and before the decision buttons.
func TestApprovalCardsOrderOutsidePaths(t *testing.T) {
	t.Parallel()
	const width = 80

	t.Run("embedded card", func(t *testing.T) {
		t.Parallel()
		m := approvalEventModel(t)
		m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "mutate", "call-order-embedded", map[string]any{"path": "a.txt"})})
		m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{
			Turn: 1, Tool: "mutate", CallID: "call-order-embedded", RequestID: "req-order-embedded",
			Mode: "paths outside the project root", Preview: `{"note":"preview-anchor"}`, Kind: "path",
			DeniedPaths: []string{"/outside/order-embedded.txt"}, DeniedPathCount: 1,
		})})

		rendered := m.content.String(width)
		requireTextOrder(t, "embedded approval card", stripANSI(rendered), "preview-anchor", "outside paths:", "Allow once")
		requireRenderedWidths(t, "embedded approval card", rendered, width)
	})

	t.Run("standalone pill", func(t *testing.T) {
		t.Parallel()
		m := approvalEventModel(t)
		m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{
			Turn: 1, Tool: "mutate", CallID: "call-order-pill", RequestID: "req-order-pill",
			Mode: "paths outside the project root", Preview: `{"path":"a.txt"}`, Kind: "path", NoPersist: true,
			DeniedPaths: []string{"/outside/order-pill.txt"}, DeniedPathCount: 1,
		})})

		rendered := m.content.String(width)
		requireTextOrder(t, "approval pill", stripANSI(rendered), "One-time retry", "outside paths:")
		requireRenderedWidths(t, "approval pill", rendered, width)
	})
}
