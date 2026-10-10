package interactive

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

// generationStates returns each generation's backend state as a string, so a
// nil state reads as the empty string.
func generationStates(l agent.ConversationLineage) []string {
	var out []string
	for _, g := range l.Generations {
		out = append(out, string(g.BackendState))
	}
	return out
}

func lineageWithStates(l agent.ConversationLineage, states ...string) agent.ConversationLineage {
	for i, state := range states {
		l.Generations[i].BackendState = json.RawMessage(state)
	}
	return l
}

func TestMergeRunLineageBackendState(t *testing.T) {
	t.Parallel()
	user := func(text string) agent.Message { return agent.Message{Role: agent.MessageRoleUser, Content: text} }
	summary := agent.Message{Role: agent.MessageRoleSummary, Content: "sum"}
	tests := []struct {
		name  string
		prior agent.ConversationLineage
		run   agent.ConversationLineage
		want  []string
	}{
		{
			name:  "run state replaces prior latest state",
			prior: lineageWithStates(lineageFromMessages([]agent.Message{user("a")}), `{"s":"old"}`),
			run:   lineageWithStates(lineageFromMessages([]agent.Message{user("a"), user("b")}), `{"s":"new"}`),
			want:  []string{`{"s":"new"}`},
		},
		{
			name:  "state cleared by the run is not resurrected from prior",
			prior: lineageWithStates(lineageFromMessages([]agent.Message{user("a")}), `{"s":"old"}`),
			run:   lineageFromMessages([]agent.Message{user("a"), user("b")}),
			want:  []string{""},
		},
		{
			name:  "empty prior adopts run state",
			prior: agent.ConversationLineage{},
			run:   lineageWithStates(lineageFromMessages([]agent.Message{user("a")}), `{"s":"new"}`),
			want:  []string{`{"s":"new"}`},
		},
		{
			name:  "earlier generation state survives and latest takes run state",
			prior: lineageWithStates(lineageFromMessages([]agent.Message{user("old")}).WithNewGeneration(nil, []agent.Message{user("a")}), `{"g":1}`, `{"g":2}`),
			run:   lineageWithStates(lineageFromMessages([]agent.Message{user("a"), user("b")}), `{"g":"run"}`),
			want:  []string{`{"g":1}`, `{"g":"run"}`},
		},
		{
			name:  "generations added by the run keep their own state",
			prior: lineageWithStates(lineageFromMessages([]agent.Message{user("old")}), `{"s":"old"}`),
			run:   lineageWithStates(lineageFromMessages([]agent.Message{user("old")}).WithNewGeneration([]agent.Message{summary}, []agent.Message{user("c")}), `{"s":"run"}`, `{"s":"appended"}`),
			want:  []string{`{"s":"run"}`, `{"s":"appended"}`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := generationStates(mergeRunLineage(tt.prior, tt.run)); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("backend states = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMergeRunLineageBackendStateNoAliasing(t *testing.T) {
	t.Parallel()
	prior := lineageWithStates(lineageFromMessages([]agent.Message{{Role: agent.MessageRoleUser, Content: "a"}}), `{"s":"old"}`)
	run := lineageWithStates(lineageFromMessages([]agent.Message{{Role: agent.MessageRoleUser, Content: "a"}}), `{"s":"run"}`)

	merged := mergeRunLineage(prior, run)
	run.Generations[0].BackendState[6] = 'X'
	merged.Generations[0].BackendState[6] = 'Y'

	if got := string(run.Generations[0].BackendState); got != `{"s":"Xun"}` {
		t.Errorf("run backend state = %s", got)
	}
	if got := string(prior.Generations[0].BackendState); got != `{"s":"old"}` {
		t.Errorf("prior backend state mutated to %s", got)
	}
}

// TestMergeRunLineageAppendedGenerationStateIndependent checks that every
// generation the run appends keeps its own backend state after adoption, and
// that the merged lineage shares no bytes with the run.
func TestMergeRunLineageAppendedGenerationStateIndependent(t *testing.T) {
	t.Parallel()
	user := func(text string) agent.Message { return agent.Message{Role: agent.MessageRoleUser, Content: text} }
	summary := agent.Message{Role: agent.MessageRoleSummary, Content: "sum"}
	prior := lineageWithStates(lineageFromMessages([]agent.Message{user("old")}), `{"s":"old"}`)
	run := lineageWithStates(
		lineageFromMessages([]agent.Message{user("old"), user("a")}).
			WithNewGeneration([]agent.Message{summary}, []agent.Message{user("b")}).
			WithNewGeneration([]agent.Message{summary}, []agent.Message{user("c")}),
		`{"s":"run"}`, `{"s":"first"}`, `{"s":"second"}`,
	)

	merged := mergeRunLineage(prior, run)
	if got, want := generationStates(merged), []string{`{"s":"run"}`, `{"s":"first"}`, `{"s":"second"}`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("backend states = %q, want %q", got, want)
	}

	run.Generations[2].BackendState[6] = 'X'
	merged.Generations[2].BackendState[6] = 'Y'
	if got := string(run.Generations[2].BackendState); got != `{"s":"Xecond"}` {
		t.Errorf("run appended state = %s, want the run's own bytes", got)
	}
	if got := string(merged.Generations[2].BackendState); got != `{"s":"Yecond"}` {
		t.Errorf("merged appended state = %s, want the merged bytes", got)
	}
}

func TestDriverRunBackendStateSeedAndAdoption(t *testing.T) {
	t.Parallel()
	conversation := []agent.Message{{Role: agent.MessageRoleUser, Content: "hi"}}
	tests := []struct {
		name      string
		seed      agent.ConversationLineage
		wantSeed  []string
		wantAdopt string
	}{
		{
			name:      "loaded lineage state reaches the run and is adopted back",
			seed:      lineageWithStates(lineageFromMessages(conversation), `{"s":"loaded"}`),
			wantSeed:  []string{`{"s":"loaded"}`},
			wantAdopt: `{"s":"next"}`,
		},
		{
			name:      "new session passes an empty lineage",
			seed:      agent.ConversationLineage{},
			wantSeed:  nil,
			wantAdopt: `{"s":"next"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got RunInput
			s := testNewSession(t, Dependencies{})
			s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
				got = in
				result := lineageWithStates(lineageFromMessages(in.Conversation), `{"s":"next"}`)
				return RunResult{Conversation: in.Conversation, Lineage: result}, nil
			}})

			out, err := s.driverRun(context.Background(), agent.DriverRunInput{Conversation: conversation, Lineage: tt.seed}, "")
			if err != nil {
				t.Fatalf("driverRun() error = %v", err)
			}
			if gotSeed := generationStates(got.Lineage); !reflect.DeepEqual(gotSeed, tt.wantSeed) {
				t.Errorf("RunInput backend states = %q, want %q", gotSeed, tt.wantSeed)
			}
			if gotAdopt := generationStates(out.Lineage); !reflect.DeepEqual(gotAdopt, []string{tt.wantAdopt}) {
				t.Errorf("adopted backend states = %q, want %q", gotAdopt, tt.wantAdopt)
			}
		})
	}
}
