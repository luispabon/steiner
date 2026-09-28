package agent

import (
	"reflect"
	"testing"
)

func TestInjectTurnBudgetNoticeIfDue_ThresholdSequences(t *testing.T) {
	tests := []struct {
		name       string
		startTurn  int
		maxTurns   int
		turns      []int
		wantTurns  []int
		wantParams [][2]int
	}{
		{
			name:      "120 turn cap",
			maxTurns:  120,
			turns:     []int{59, 60, 60, 89, 90, 107, 108, 120, 121},
			wantTurns: []int{60, 90, 108},
		},
		{
			name:      "ceil thresholds",
			maxTurns:  7,
			turns:     []int{3, 4, 5, 6, 7},
			wantTurns: []int{4, 6, 7},
		},
		{
			name:      "skipped thresholds emit once",
			maxTurns:  10,
			turns:     []int{10, 10},
			wantTurns: []int{10},
		},
		{
			name:       "follow-up run measures from start turn",
			startTurn:  120,
			maxTurns:   240,
			turns:      []int{121, 180, 210, 228},
			wantTurns:  []int{180, 210, 228},
			wantParams: [][2]int{{60, 120}, {90, 120}, {108, 120}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := []Message{{Role: MessageRoleUser, Content: "hello"}}
			state := RunState{
				TurnCount:       test.startTurn,
				BudgetStartTurn: test.startTurn,
				Conversation:    cloneMessages(original),
				Lineage:         newConversationLineage(original),
			}
			var gotParams [][2]int
			req := RunRequest{
				Limits: Limits{MaxTurns: test.maxTurns},
				TurnBudgetNotice: func(turnsUsed, maxTurns int) string {
					gotParams = append(gotParams, [2]int{turnsUsed, maxTurns})
					return "notice"
				},
			}

			var gotTurns []int
			for _, turn := range test.turns {
				state.TurnCount = turn
				before := cloneMessages(state.Conversation)
				state = injectTurnBudgetNoticeIfDue(state, req)
				if !reflect.DeepEqual(state.Conversation[:len(before)], before) {
					t.Fatalf("existing conversation changed at turn %d", turn)
				}
				if len(state.Conversation) > len(before) {
					if len(state.Conversation) != len(before)+1 {
						t.Fatalf("turn %d appended %d messages, want one", turn, len(state.Conversation)-len(before))
					}
					notice := state.Conversation[len(state.Conversation)-1]
					if notice.Role != MessageRoleUser || notice.Turn != turn {
						t.Fatalf("appended notice = %+v, want user message at turn %d", notice, turn)
					}
					gotTurns = append(gotTurns, notice.Turn)
				}
			}
			if !reflect.DeepEqual(gotTurns, test.wantTurns) {
				t.Fatalf("notice turns = %v, want %v", gotTurns, test.wantTurns)
			}
			if test.wantParams != nil && !reflect.DeepEqual(gotParams, test.wantParams) {
				t.Fatalf("notice callback params = %v, want %v", gotParams, test.wantParams)
			}
			if !reflect.DeepEqual(state.Conversation, state.Lineage.FullMessages()) {
				t.Fatal("conversation and lineage diverged")
			}
		})
	}
}

func TestInjectTurnBudgetNoticeIfDue_Disabled(t *testing.T) {
	for _, test := range []struct {
		name string
		req  RunRequest
	}{
		{name: "nil callback", req: RunRequest{Limits: Limits{MaxTurns: 10}}},
		{name: "zero cap", req: RunRequest{Limits: Limits{MaxTurns: 0}, TurnBudgetNotice: func(int, int) string { return "notice" }}},
		{name: "negative cap", req: RunRequest{Limits: Limits{MaxTurns: -1}, TurnBudgetNotice: func(int, int) string { return "notice" }}},
	} {
		t.Run(test.name, func(t *testing.T) {
			messages := []Message{{Role: MessageRoleUser, Content: "unchanged"}}
			state := RunState{TurnCount: 100, Conversation: cloneMessages(messages), Lineage: newConversationLineage(messages)}
			got := injectTurnBudgetNoticeIfDue(state, test.req)
			if !reflect.DeepEqual(got.Conversation, messages) || got.BudgetNoticesIssued != 0 {
				t.Fatalf("state changed for disabled notice: %+v", got)
			}
		})
	}
}
