//go:build claudesub_probe

package provider

import (
	"reflect"
	"strings"
	"testing"
)

// TestClaudeSubProbeContinuationNote checks the note E7 sends after the original
// prompt: the exact specified text around the quoted partial, in a second text
// block of the same user line.
func TestClaudeSubProbeContinuationNote(t *testing.T) {
	partial := "item-1 one\nitem-2 two"
	want := "Your previous reply to the message above was cut off by a connection failure before it finished. " +
		"This is the text it had produced so far, quoted exactly between the markers:\n" +
		"<<<PARTIAL\n" + partial + "\nPARTIAL>>>\n" +
		"Continue the reply from exactly where that text ends. Do not repeat any of it and do not mention the interruption."
	if got := claudeSubProbeContinuationNote(partial); got != want {
		t.Fatalf("continuation note = %q, want %q", got, want)
	}

	blocks := claudeSubProbeContinuationBlocks(claudeSubProbePromptE7T2, partial)
	if len(blocks) != 2 {
		t.Fatalf("continuation line carries %d blocks, want 2", len(blocks))
	}
	if got := blocks[0]["text"]; got != claudeSubProbePromptE7T2 {
		t.Fatalf("first block = %q, want the exact second prompt", got)
	}
	if got := blocks[1]["text"]; got != want {
		t.Fatalf("second block = %q, want the note", got)
	}
	for i, block := range blocks {
		if got := block["type"]; got != "text" {
			t.Fatalf("block %d type = %v, want text", i, got)
		}
	}
}

// TestClaudeSubProbeSeamAnalysis checks what the item numbers say about a
// continuation: clean, duplicate, gap, restart, a fragment that completes a cut
// item number, and commentary.
func TestClaudeSubProbeSeamAnalysis(t *testing.T) {
	cases := []struct {
		name         string
		partial      string
		continuation string
		want         claudeSubProbeSeam
		wantVerdict  string
	}{
		{
			name:         "clean continuation",
			partial:      "item-1 one\nitem-2 two\n",
			continuation: "item-3 three\nitem-4 four\n",
			want:         claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, ContinuationFirst: 3, MaxItem: 4},
			wantVerdict:  "clean",
		},
		{
			name:         "duplicate item",
			partial:      "item-1 one\nitem-2 two\n",
			continuation: "item-2 two\nitem-3 three\n",
			want:         claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, ContinuationFirst: 2, Duplicate: []int{2}, MaxItem: 3},
			wantVerdict:  "duplicate",
		},
		{
			name:         "gap in the numbering",
			partial:      "item-1 one\nitem-2 two\n",
			continuation: "item-4 four\nitem-5 five\n",
			want:         claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, ContinuationFirst: 4, Missing: []int{3}, MaxItem: 5},
			wantVerdict:  "gap",
		},
		{
			name:         "restart at item one",
			partial:      "item-1 one\nitem-2 two\n",
			continuation: "item-1 one\nitem-2 two\n",
			want:         claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, ContinuationFirst: 1, Duplicate: []int{1, 2}, MaxItem: 2},
			wantVerdict:  "restart",
		},
		{
			name:         "continuation completes a cut item number",
			partial:      "item-1 one\nitem-2 two\nitem-3 three\nitem-4",
			continuation: "1 forty-one\n",
			want: claudeSubProbeSeam{PartialLastComplete: 3, PartialMax: 4, ContinuationFirst: 41,
				Missing: claudeSubProbeItemRange(5, 40), CutDigits: "4", FragmentCompletes: true, MaxItem: 41},
			wantVerdict: "gap",
		},
		{
			name:         "commentary with clean numbering",
			partial:      "item-1 one\nitem-2 two\n",
			continuation: "Continuing from item-3:\nitem-3 three\n",
			want:         claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, ContinuationFirst: 3, MaxItem: 3},
			wantVerdict:  "commentary",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := claudeSubProbeAnalyseSeam(tc.partial, tc.continuation)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("seam = %+v, want %+v", got, tc.want)
			}
			mentioned := claudeSubProbeMentionsInterruption(tc.continuation)
			if verdict := claudeSubProbeCrashVerdict(got, mentioned); verdict != tc.wantVerdict {
				t.Fatalf("verdict = %q, want %q (mentioned=%v)", verdict, tc.wantVerdict, mentioned)
			}
		})
	}
}

// TestClaudeSubProbeCutItemDigits checks the cut detection behind the fragment
// flag: only an unterminated final line ending in item-<digits> counts.
func TestClaudeSubProbeCutItemDigits(t *testing.T) {
	cases := []struct {
		name    string
		partial string
		want    string
		wantCut bool
	}{
		{name: "cut inside the number", partial: "item-1 one\nitem-4", want: "4", wantCut: true},
		{name: "cut inside item-", partial: "item-1 one\nitem-", wantCut: true},
		{name: "completed line", partial: "item-1 one\nitem-4 four\n"},
		{name: "cut inside the item text", partial: "item-1 one\nitem-4 fo"},
		{name: "empty partial"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			digits, cut := claudeSubProbeCutItemDigits(tc.partial)
			if cut != tc.wantCut || digits != tc.want {
				t.Fatalf("cut digits = %q (cut=%v), want %q (cut=%v)", digits, cut, tc.want, tc.wantCut)
			}
		})
	}
}

// TestClaudeSubProbeMentionsInterruption checks the words the probe treats as the
// model talking about the interruption.
func TestClaudeSubProbeMentionsInterruption(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{text: "item-41 forty-one\n", want: false},
		{text: "The interruption cut the list short.\nitem-41\n", want: true},
		{text: "My previous reply was cut off.\n", want: true},
		{text: "Continuing:\nitem-41 forty-one\n", want: true},
	}
	for _, tc := range cases {
		if got := claudeSubProbeMentionsInterruption(tc.text); got != tc.want {
			t.Fatalf("mentions interruption for %q = %v, want %v", tc.text, got, tc.want)
		}
	}
}

// TestClaudeSubProbeCrashExitFinding checks that a trial only counts as measured
// when the spawned CLI really died of the signal.
func TestClaudeSubProbeCrashExitFinding(t *testing.T) {
	cases := []struct {
		name    string
		exit    *claudeSubProbeExit
		wantErr string
	}{
		{name: "killed by signal", exit: &claudeSubProbeExit{StatusKnown: true, Status: -1, WaitErr: "signal: killed"}},
		{name: "no evidence", wantErr: "no exit evidence"},
		{name: "unknown status", exit: &claudeSubProbeExit{}, wantErr: "status is unknown"},
		{name: "clean exit", exit: &claudeSubProbeExit{StatusKnown: true}, wantErr: "without a wait error"},
		{name: "zero status with a wait error", exit: &claudeSubProbeExit{StatusKnown: true, WaitErr: "exit status 0"}, wantErr: "exited 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			finding, err := claudeSubProbeCrashExitFinding(tc.exit)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("exit finding error = %v, want nil (finding %q)", err, finding)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("exit finding error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// TestClaudeSubProbeExpectedCrash checks that only the process an experiment
// killed itself is exempt from the exit-before-teardown and teardown-result
// failures.
func TestClaudeSubProbeExpectedCrash(t *testing.T) {
	crashed := func(expected bool) *claudeSubProbeProcessReport {
		return &claudeSubProbeProcessReport{
			Name:          "E7 trial 1 parent",
			ExpectedCrash: expected,
			Exit:          &claudeSubProbeExit{StatusKnown: true, Status: -1, WaitErr: "signal: killed", ExitedBeforeTeardown: true},
			TeardownErr:   "claude cli exited: signal: killed",
		}
	}
	if err := claudeSubProbeProcessFailure(crashed(false)); err == nil {
		t.Fatal("a process that exited before teardown passed without ExpectedCrash")
	}
	if err := claudeSubProbeProcessFailure(crashed(true)); err != nil {
		t.Fatalf("a process the experiment killed failed its own check: %v", err)
	}
	truncated := crashed(true)
	truncated.TimelineTruncated = true
	if err := claudeSubProbeProcessFailure(truncated); err == nil || !strings.Contains(err.Error(), "entry cap") {
		t.Fatalf("truncated crash process error = %v, want the entry cap failure", err)
	}
}

// claudeSubProbeItemRange is the inclusive item number range from..to, which is
// what a numbering gap over several items looks like.
func claudeSubProbeItemRange(from, to int) []int {
	out := make([]int, 0, to-from+1)
	for number := from; number <= to; number++ {
		out = append(out, number)
	}
	return out
}
