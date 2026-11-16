//go:build claudesub_probe

package provider

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// claudeSubProbeE7Name is E7's experiment ID, which its trial count is keyed on.
const claudeSubProbeE7Name = "E7-crash-resume-continue"

// claudeSubProbeE7MinTrials and claudeSubProbeE7MaxTrials bound the trial count
// STEINER_CLAUDESUB_PROBE_E7_TRIALS accepts.
const (
	claudeSubProbeE7MinTrials = 1
	claudeSubProbeE7MaxTrials = 5
)

// claudeSubProbeParseOnly parses the experiment selection: a comma-separated list
// of experiment ID prefixes, upper-cased and trimmed. An empty value selects every
// experiment.
func claudeSubProbeParseOnly(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	fields := strings.Split(raw, ",")
	prefixes := make([]string, 0, len(fields))
	for _, field := range fields {
		prefix := strings.ToUpper(strings.TrimSpace(field))
		if prefix == "" {
			return nil, fmt.Errorf("probe experiment selection %q has an empty entry", raw)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

// claudeSubProbeSelectionLabel describes the raw selection for the plan output.
func claudeSubProbeSelectionLabel(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "all experiments"
	}
	return strings.TrimSpace(raw)
}

// claudeSubProbeSelect keeps the experiments whose ID starts with one of prefixes,
// in table order. An unknown prefix is an error, so a typo fails instead of
// silently running nothing.
func claudeSubProbeSelect(all []claudeSubProbeExperiment, prefixes []string) ([]claudeSubProbeExperiment, error) {
	if len(prefixes) == 0 {
		return append([]claudeSubProbeExperiment(nil), all...), nil
	}
	matched := make([]bool, len(prefixes))
	selected := make([]claudeSubProbeExperiment, 0, len(all))
	for _, experiment := range all {
		name := strings.ToUpper(experiment.name)
		for i, prefix := range prefixes {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			matched[i] = true
			selected = append(selected, experiment)
			break
		}
	}
	var unknown []string
	for i, prefix := range prefixes {
		if !matched[i] {
			unknown = append(unknown, prefix)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("probe experiment selection names unknown IDs %v; the probe runs %v", unknown, claudeSubProbeExperimentNames(all))
	}
	return selected, nil
}

// claudeSubProbeExperimentNames lists experiment IDs in table order.
func claudeSubProbeExperimentNames(experiments []claudeSubProbeExperiment) []string {
	names := make([]string, 0, len(experiments))
	for _, experiment := range experiments {
		names = append(names, experiment.name)
	}
	return names
}

// claudeSubProbeSelectsE7 reports whether E7 is among experiments.
func claudeSubProbeSelectsE7(experiments []claudeSubProbeExperiment) bool {
	for _, experiment := range experiments {
		if experiment.name == claudeSubProbeE7Name {
			return true
		}
	}
	return false
}

// claudeSubProbeWithE7Trials returns selected with E7 planned for trials trials.
// The experiment's bound is its own step plan, so the two cannot drift.
func claudeSubProbeWithE7Trials(selected []claudeSubProbeExperiment, trials int) []claudeSubProbeExperiment {
	out := append([]claudeSubProbeExperiment(nil), selected...)
	for i := range out {
		if out[i].name != claudeSubProbeE7Name {
			continue
		}
		plan := claudeSubProbeE7Steps(trials)
		out[i].steps = plan
		out[i].timeout = plan.plan()
	}
	return out
}

// claudeSubProbeSelectedBounds is what the selected experiments claim together.
func claudeSubProbeSelectedBounds(selected []claudeSubProbeExperiment) time.Duration {
	total := time.Duration(0)
	for _, experiment := range selected {
		total += experiment.timeout
	}
	return total
}

// claudeSubProbeE7TrialCount resolves E7's trial count. Without an override it is
// claudeSubProbeCrashTrials, raised by one when E7 is the only selected experiment
// because the freed ceiling pays for it. An override is honoured exactly and fails
// when the selection cannot fit it; the derived count is lowered until it fits, so
// only a selection that cannot fit one trial fails.
func claudeSubProbeE7TrialCount(raw string, selected []claudeSubProbeExperiment) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" {
		count, err := strconv.Atoi(trimmed)
		if err != nil {
			return 0, fmt.Errorf("probe E7 trial count %q is not a number", raw)
		}
		if count < claudeSubProbeE7MinTrials || count > claudeSubProbeE7MaxTrials {
			return 0, fmt.Errorf("probe E7 trial count %d is outside %d to %d", count, claudeSubProbeE7MinTrials, claudeSubProbeE7MaxTrials)
		}
		if total := claudeSubProbeSelectedBounds(claudeSubProbeWithE7Trials(selected, count)); total >= claudeSubProbeRunTimeout {
			return 0, fmt.Errorf("probe E7 trial count %d claims %s with the selected experiments, at or above the probe ceiling %s", count, total, claudeSubProbeRunTimeout)
		}
		return count, nil
	}

	requested := claudeSubProbeCrashTrials
	if len(selected) == 1 && claudeSubProbeSelectsE7(selected) {
		requested = claudeSubProbeCrashTrials + 1
	}
	for count := requested; count >= claudeSubProbeE7MinTrials; count-- {
		if claudeSubProbeSelectedBounds(claudeSubProbeWithE7Trials(selected, count)) < claudeSubProbeRunTimeout {
			return count, nil
		}
	}
	return 0, fmt.Errorf("the selected experiments do not fit the probe ceiling %s even with %d E7 trial", claudeSubProbeRunTimeout, claudeSubProbeE7MinTrials)
}

// TestClaudeSubProbeSelection checks the selection parsing and matching, including
// the failure for an unknown prefix and for an empty entry.
func TestClaudeSubProbeSelection(t *testing.T) {
	all := claudeSubProbeExperimentTable(claudeSubProbeConfig{Model: claudeSubProbeDefaultModel})
	if len(all) != 7 {
		t.Fatalf("the experiment table has %d experiments, want 7", len(all))
	}
	cases := []struct {
		name    string
		raw     string
		want    []string
		wantErr string
	}{
		{name: "no selection runs everything", want: claudeSubProbeExperimentNames(all)},
		{name: "one prefix", raw: "E7", want: []string{claudeSubProbeE7Name}},
		{name: "lower case", raw: " e7 ", want: []string{claudeSubProbeE7Name}},
		{name: "two prefixes keep table order", raw: "E7, E1", want: []string{"E1-text-interrupt-reuse", claudeSubProbeE7Name}},
		{name: "unknown prefix", raw: "E9", wantErr: "unknown IDs"},
		{name: "one unknown among known", raw: "E1,E9", wantErr: "unknown IDs"},
		{name: "empty entry", raw: "E1,", wantErr: "empty entry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefixes, err := claudeSubProbeParseOnly(tc.raw)
			if tc.wantErr == "empty entry" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parse error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse error = %v, want nil", err)
			}
			selected, err := claudeSubProbeSelect(all, prefixes)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("select error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("select error = %v, want nil", err)
			}
			if got := claudeSubProbeExperimentNames(selected); strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("selected %v, want %v", got, tc.want)
			}
		})
	}
}

// TestClaudeSubProbeSelectedExperiments checks that the selection reaches the
// experiment table: only the selected experiments come back, E7's bound follows its
// resolved trial count, and an unknown prefix fails the selection.
func TestClaudeSubProbeSelectedExperiments(t *testing.T) {
	e7Only, err := claudeSubProbeExperiments(claudeSubProbeConfig{Model: claudeSubProbeDefaultModel, Only: "E7"})
	if err != nil {
		t.Fatalf("selecting E7 error = %v", err)
	}
	if names := claudeSubProbeExperimentNames(e7Only); len(names) != 1 || names[0] != claudeSubProbeE7Name {
		t.Fatalf("E7-only selection = %v, want [%s]", names, claudeSubProbeE7Name)
	}
	if got := e7Only[0].steps.trials; got != claudeSubProbeCrashTrials+1 {
		t.Fatalf("E7-only trials = %d, want %d", got, claudeSubProbeCrashTrials+1)
	}
	if got, want := e7Only[0].timeout, claudeSubProbeE7Steps(claudeSubProbeCrashTrials+1).plan(); got != want {
		t.Fatalf("E7-only bound = %s, want its %s plan", got, want)
	}

	full, err := claudeSubProbeExperiments(claudeSubProbeConfig{Model: claudeSubProbeDefaultModel})
	if err != nil {
		t.Fatalf("full selection error = %v", err)
	}
	if len(full) != 7 {
		t.Fatalf("full selection = %d experiments, want 7", len(full))
	}
	for _, experiment := range full {
		if experiment.name == claudeSubProbeE7Name && experiment.steps.trials != claudeSubProbeCrashTrials {
			t.Fatalf("full-run E7 trials = %d, want the default %d", experiment.steps.trials, claudeSubProbeCrashTrials)
		}
	}

	if _, err := claudeSubProbeExperiments(claudeSubProbeConfig{Model: claudeSubProbeDefaultModel, Only: "E9"}); err == nil || !strings.Contains(err.Error(), "unknown IDs") {
		t.Fatalf("unknown selection error = %v, want the unknown-ID failure", err)
	}
}

// TestClaudeSubProbeE7TrialBounds checks the E7 trial-count arithmetic: the
// derived default, the override range, the ceiling check, and that the bound is
// always E7's own step plan for the resolved count.
func TestClaudeSubProbeE7TrialBounds(t *testing.T) {
	all := claudeSubProbeExperimentTable(claudeSubProbeConfig{Model: claudeSubProbeDefaultModel})
	e7Only, err := claudeSubProbeSelect(all, []string{"E7"})
	if err != nil {
		t.Fatalf("select E7: %v", err)
	}
	cases := []struct {
		name       string
		raw        string
		selected   []claudeSubProbeExperiment
		wantTrials int
		wantErr    string
	}{
		{name: "full run keeps the default", selected: all, wantTrials: claudeSubProbeCrashTrials},
		{name: "E7 alone gets one more trial", selected: e7Only, wantTrials: claudeSubProbeCrashTrials + 1},
		{name: "explicit one", raw: "1", selected: e7Only, wantTrials: 1},
		{name: "explicit maximum", raw: "5", selected: e7Only, wantTrials: claudeSubProbeE7MaxTrials},
		{name: "below the minimum", raw: "0", selected: e7Only, wantErr: "outside 1 to 5"},
		{name: "above the maximum", raw: "6", selected: e7Only, wantErr: "outside 1 to 5"},
		{name: "not a number", raw: "three", selected: e7Only, wantErr: "not a number"},
		{name: "override that cannot fit the ceiling", raw: "5", selected: all, wantErr: "probe ceiling"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			count, err := claudeSubProbeE7TrialCount(tc.raw, tc.selected)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("trial count error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("trial count error = %v, want nil", err)
			}
			if count != tc.wantTrials {
				t.Fatalf("trials = %d, want %d", count, tc.wantTrials)
			}
			plan := claudeSubProbeE7Steps(count)
			want := claudeSubProbeInitWorstCase + time.Duration(count)*(claudeSubProbeInitWorstCase+
				claudeSubProbeCrashReply+claudeSubProbeCrashStream+claudeSubProbeCrashExit+claudeSubProbeCrashContinuation)
			if got := plan.plan(); got != want {
				t.Fatalf("plan(%d) = %s, want %s", count, got, want)
			}
			total := claudeSubProbeSelectedBounds(claudeSubProbeWithE7Trials(tc.selected, count))
			if total >= claudeSubProbeRunTimeout {
				t.Fatalf("resolved %d trials claim %s, at or above the ceiling %s", count, total, claudeSubProbeRunTimeout)
			}
		})
	}
}
