//go:build claudesub_probe

package provider

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestClaudeSubProbeContinuationNote checks the note E7 sends after the original
// prompt: the exact specified text around the quoted partial, in a second text
// block of the same user line.
func TestClaudeSubProbeContinuationNote(t *testing.T) {
	partial := "item-1 one\nitem-2 two"
	want := "Your previous reply to the message above was cut off by an error before it finished. " +
		"This is the text it had produced so far, quoted exactly between the markers:\n" +
		"<<<PARTIAL\n" + partial + "\nPARTIAL>>>\n" +
		"Continue the reply from exactly where that text ends. " +
		"The quoted text ends exactly at the end marker, so if the next character of your reply should be a line break or a space, start with it. " +
		"Do not repeat any of it and do not mention the interruption."
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
// continuation, including the verdicts that report a failed or partial measurement
// before any numbering judgement: empty, no-items and short.
func TestClaudeSubProbeSeamAnalysis(t *testing.T) {
	cases := []struct {
		name         string
		partial      string
		continuation string
		errored      bool
		want         claudeSubProbeSeam
		wantVerdict  string
	}{
		{
			name:         "clean continuation",
			partial:      claudeSubProbeItemText(1, 2),
			continuation: claudeSubProbeItemText(3, 200),
			want:         claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, ContinuationFirst: 3, ContinuationItems: 198, MaxItem: 200},
			wantVerdict:  "clean",
		},
		{
			name:         "duplicate item",
			partial:      claudeSubProbeItemText(1, 2),
			continuation: "item-2 two\n" + claudeSubProbeItemText(3, 200),
			want:         claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, ContinuationFirst: 2, ContinuationItems: 199, Duplicate: []int{2}, MaxItem: 200},
			wantVerdict:  "duplicate",
		},
		{
			name:         "gap in the numbering",
			partial:      claudeSubProbeItemText(1, 2),
			continuation: claudeSubProbeItemText(4, 200),
			want:         claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, ContinuationFirst: 4, ContinuationItems: 197, Missing: []int{3}, MaxItem: 200},
			wantVerdict:  "gap",
		},
		{
			name:         "restart at item one",
			partial:      claudeSubProbeItemText(1, 2),
			continuation: claudeSubProbeItemText(1, 200),
			want:         claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, ContinuationFirst: 1, ContinuationItems: 200, Duplicate: []int{1, 2}, MaxItem: 200},
			wantVerdict:  "restart",
		},
		{
			name:         "continuation completes a cut item prefix",
			partial:      "item-1\nitem-2\nitem-",
			continuation: "3\n" + claudeSubProbeItemText(4, 200),
			want: claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, ContinuationFirst: 3, ContinuationItems: 198,
				FragmentCompletes: true, MaxItem: 200},
			wantVerdict: "clean",
		},
		{
			name:         "continuation completes a cut item number",
			partial:      "item-1 one\nitem-2 two\nitem-3 three\nitem-4",
			continuation: "1 forty-one\n" + claudeSubProbeItemText(42, 200),
			want: claudeSubProbeSeam{PartialLastComplete: 3, PartialMax: 3, ContinuationFirst: 41, ContinuationItems: 160,
				Missing: claudeSubProbeItemRange(4, 40), CutDigits: "4", FragmentCompletes: true, MaxItem: 200},
			wantVerdict: "gap",
		},
		{
			name:         "line break after a whole final item number",
			partial:      claudeSubProbeItemText(1, 40) + "item-41",
			continuation: "\n" + claudeSubProbeItemText(42, 200),
			want: claudeSubProbeSeam{PartialLastComplete: 40, PartialMax: 41, ContinuationFirst: 42, ContinuationItems: 159,
				CutDigits: "41", MaxItem: 200},
			wantVerdict: "clean",
		},
		{
			name:         "no line break after a whole final item number",
			partial:      claudeSubProbeItemText(1, 40) + "item-41",
			continuation: claudeSubProbeItemText(42, 200),
			want: claudeSubProbeSeam{PartialLastComplete: 40, PartialMax: 41, ContinuationFirst: 42, ContinuationItems: 159,
				MissingLineBreak: true, CutDigits: "41", MaxItem: 200},
			wantVerdict: "line-break-missing",
		},
		{
			name:         "no line break after an ambiguous short whole item number",
			partial:      claudeSubProbeItemText(1, 18) + "item-19",
			continuation: claudeSubProbeItemText(20, 200),
			want: claudeSubProbeSeam{PartialLastComplete: 18, PartialMax: 19, ContinuationFirst: 20, ContinuationItems: 181,
				MissingLineBreak: true, CutDigits: "19", MaxItem: 200},
			wantVerdict: "line-break-missing",
		},
		{
			name:         "ambiguous short item number completed by the continuation",
			partial:      claudeSubProbeItemText(1, 14) + "item-1",
			continuation: "5\n" + claudeSubProbeItemText(16, 200),
			want: claudeSubProbeSeam{PartialLastComplete: 14, PartialMax: 14, ContinuationFirst: 15, ContinuationItems: 186,
				CutDigits: "1", FragmentCompletes: true, MaxItem: 200},
			wantVerdict: "clean",
		},
		{
			name:         "cut item number completed by the continuation without a gap",
			partial:      claudeSubProbeItemText(1, 40) + "item-4",
			continuation: "1 forty-one\n" + claudeSubProbeItemText(42, 200),
			want: claudeSubProbeSeam{PartialLastComplete: 40, PartialMax: 40, ContinuationFirst: 41, ContinuationItems: 160,
				CutDigits: "4", FragmentCompletes: true, MaxItem: 200},
			wantVerdict: "clean",
		},
		{
			name:         "list stops short of the end",
			partial:      claudeSubProbeItemText(1, 2),
			continuation: claudeSubProbeItemText(3, 100),
			want:         claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, ContinuationFirst: 3, ContinuationItems: 98, MaxItem: 100},
			wantVerdict:  "short",
		},
		{
			name:         "continuation carries no items",
			partial:      claudeSubProbeItemText(1, 2),
			continuation: "The list continues below.\n",
			want:         claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, MaxItem: 2},
			wantVerdict:  "no-items",
		},
		{
			name:        "empty continuation",
			partial:     claudeSubProbeItemText(1, 2),
			want:        claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, MaxItem: 2},
			wantVerdict: "empty",
		},
		{
			name:         "errored continuation with clean numbering",
			partial:      claudeSubProbeItemText(1, 2),
			continuation: claudeSubProbeItemText(3, 200),
			errored:      true,
			want:         claudeSubProbeSeam{PartialLastComplete: 2, PartialMax: 2, ContinuationFirst: 3, ContinuationItems: 198, MaxItem: 200, Errored: true},
			wantVerdict:  "empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := claudeSubProbeAnalyseSeam(tc.partial, tc.continuation)
			got.Errored = tc.errored
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("seam = %+v, want %+v", got, tc.want)
			}
			mentioned := claudeSubProbeMentionsInterruption(tc.continuation)
			if verdict := claudeSubProbeCrashVerdict(got, tc.continuation, mentioned); verdict != tc.wantVerdict {
				t.Fatalf("verdict = %q, want %q (mentioned=%v)", verdict, tc.wantVerdict, mentioned)
			}
		})
	}

	t.Run("commentary with a complete clean list", func(t *testing.T) {
		continuation := "Continuing from where the text stopped:\n" + claudeSubProbeItemText(3, 200)
		seam := claudeSubProbeAnalyseSeam(claudeSubProbeItemText(1, 2), continuation)
		if verdict := claudeSubProbeCrashVerdict(seam, continuation, claudeSubProbeMentionsInterruption(continuation)); verdict != "commentary" {
			t.Fatalf("verdict = %q, want commentary", verdict)
		}
	})
}

// TestClaudeSubProbeVerdictSummary checks the aggregate a repeated experiment
// reports: one entry per verdict in name order, including a lost line break.
func TestClaudeSubProbeVerdictSummary(t *testing.T) {
	cases := []struct {
		name     string
		verdicts []string
		want     string
	}{
		{name: "no trials", want: ""},
		{name: "one verdict", verdicts: []string{"clean"}, want: "clean=1"},
		{name: "repeated and new verdicts", verdicts: []string{"line-break-missing", "clean", "line-break-missing"},
			want: "clean=1 line-break-missing=2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeSubProbeVerdictSummary(tc.verdicts); got != tc.want {
				t.Fatalf("verdict summary = %q, want %q", got, tc.want)
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
// when the spawned CLI died of the SIGKILL the probe sent.
func TestClaudeSubProbeCrashExitFinding(t *testing.T) {
	cases := []struct {
		name    string
		exit    *claudeSubProbeExit
		wantErr string
	}{
		{name: "killed by signal", exit: &claudeSubProbeExit{StatusKnown: true, Status: -1, WaitErr: "signal: killed"}},
		{name: "no evidence", wantErr: "no exit evidence"},
		{name: "unknown status", exit: &claudeSubProbeExit{}, wantErr: "exited before kill"},
		{name: "clean exit", exit: &claudeSubProbeExit{StatusKnown: true}, wantErr: "exited before kill"},
		{name: "zero status", exit: &claudeSubProbeExit{StatusKnown: true, Status: 0, WaitErr: "exit status 0"}, wantErr: "exited before kill"},
		{name: "status minus one without the signal", exit: &claudeSubProbeExit{StatusKnown: true, Status: -1, WaitErr: "wait: no child processes"}, wantErr: "exited before kill"},
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

// TestClaudeSubProbeExpectedCrash checks that ExpectedCrash exempts only the
// crash-derived evidence: the SIGKILL exit and the existing bounded-teardown
// allowlist. Any other teardown error still fails the experiment.
func TestClaudeSubProbeExpectedCrash(t *testing.T) {
	killed := func(expected bool, teardown string) *claudeSubProbeProcessReport {
		return &claudeSubProbeProcessReport{
			Name:          "E7 trial 1 parent",
			ExpectedCrash: expected,
			Exit:          &claudeSubProbeExit{StatusKnown: true, Status: -1, WaitErr: "signal: killed", ExitedBeforeTeardown: true},
			TeardownErr:   teardown,
		}
	}
	cases := []struct {
		name    string
		report  *claudeSubProbeProcessReport
		wantErr string
	}{
		{name: "killed process not marked", report: killed(false, "claude cli exited: signal: killed"), wantErr: "exited before teardown"},
		{name: "killed process marked", report: killed(true, "claude cli exited: signal: killed")},
		{name: "marked with the bounded-teardown allowlist", report: killed(true, "stop claude_subscription router: teardown did not complete")},
		{name: "marked with an unrelated teardown error", report: killed(true, "remove claude_subscription session dir: permission denied"), wantErr: "teardown reported"},
		{name: "marked with a clean exit", report: &claudeSubProbeProcessReport{
			Name:          "E7 trial 1 parent",
			ExpectedCrash: true,
			Exit:          &claudeSubProbeExit{StatusKnown: true, Status: 0, WaitErr: "exit status 0", ExitedBeforeTeardown: true},
			TeardownErr:   "claude cli exited: exit status 0",
		}, wantErr: "exited before teardown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := claudeSubProbeProcessFailure(tc.report)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("process failure = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("process failure = %v, want %q", err, tc.wantErr)
			}
		})
	}

	truncated := killed(true, "claude cli exited: signal: killed")
	truncated.TimelineTruncated = true
	if err := claudeSubProbeProcessFailure(truncated); err == nil || !strings.Contains(err.Error(), "entry cap") {
		t.Fatalf("truncated crash process error = %v, want the entry cap failure", err)
	}
}

// TestClaudeSubProbeGateResultBudget checks that the init gate uses the budget the
// experiment declares for its gating turn, so E7's long continuation is not cut to
// the probe's fixed warmup budget.
func TestClaudeSubProbeGateResultBudget(t *testing.T) {
	if got := claudeSubProbeGateResultBudget(0); got != claudeSubProbeWarmupResult {
		t.Fatalf("default gating result budget = %s, want %s", got, claudeSubProbeWarmupResult)
	}
	if got := claudeSubProbeGateResultBudget(claudeSubProbeCrashContinuation); got != claudeSubProbeCrashContinuation {
		t.Fatalf("declared gating result budget = %s, want %s", got, claudeSubProbeCrashContinuation)
	}
	if claudeSubProbeCrashContinuation <= claudeSubProbeWarmupResult {
		t.Fatalf("E7's continuation budget %s does not exceed the fixed gating budget %s", claudeSubProbeCrashContinuation, claudeSubProbeWarmupResult)
	}
}

// TestClaudeSubProbeE7PlanShape checks the E7 step plan: one repetition per trial,
// the declared continuation budget in every trial, and two processes per trial, so
// the body, the gate and the teardown term cannot drift apart.
func TestClaudeSubProbeE7PlanShape(t *testing.T) {
	for _, trials := range []int{claudeSubProbeCrashTrials, claudeSubProbeE7OnlyTrials} {
		plan := claudeSubProbeE7Steps(trials)
		if plan.trials != trials {
			t.Fatalf("plan trials = %d, want %d", plan.trials, trials)
		}
		if plan.processes != 2*trials {
			t.Fatalf("plan processes = %d, want %d", plan.processes, 2*trials)
		}
		if plan.gate != 0 {
			t.Fatalf("plan gate = %s, want 0: every trial charges its own parent and resumed gates", plan.gate)
		}
		if len(plan.extra) != 2*trials {
			t.Fatalf("plan extras = %d, want two init gates per trial (%d)", len(plan.extra), 2*trials)
		}
		for _, extra := range plan.extra {
			if extra != claudeSubProbeInitWorstCase {
				t.Fatalf("plan extra = %s, want an init gate %s", extra, claudeSubProbeInitWorstCase)
			}
		}
		for trial := 0; trial < trials; trial++ {
			base := trial * 4
			want := []time.Duration{claudeSubProbeCrashReply, claudeSubProbeCrashStream, claudeSubProbeCrashExit, claudeSubProbeCrashContinuation}
			for i, budget := range want {
				if got := plan.waits[base+i]; got != budget {
					t.Fatalf("trial %d wait %d = %s, want %s", trial+1, i, got, budget)
				}
			}
		}
	}
}

// claudeSubProbeItemText renders one item line per number from..to, which is the
// numbering the E7 list prompt asks for.
func claudeSubProbeItemText(from, to int) string {
	var builder strings.Builder
	for number := from; number <= to; number++ {
		fmt.Fprintf(&builder, "item-%d\n", number)
	}
	return builder.String()
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
