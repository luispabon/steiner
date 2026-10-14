package quota

import (
	"testing"
	"time"
)

func win(label string, used float64) Window {
	return Window{Label: label, Duration: time.Hour, UsedPercent: used, ResetsAt: testBase.Add(time.Hour), Status: "ok"}
}

func TestMergePassive(t *testing.T) {
	prev := Snapshot{
		AccountKey:   "a",
		ProviderType: "claude",
		Plan:         "pro",
		Credits:      "12",
		Windows:      []Window{win("5h", 10), win("weekly", 40)},
		Budgets:      []Budget{{Label: "extra", Used: 3}},
		FetchedAt:    testBase,
		Source:       SourceFetch,
	}
	obsAt := testBase.Add(10 * time.Minute)

	tests := []struct {
		name string
		obs  Snapshot
		want Snapshot
	}{
		{
			name: "matching label overwrites and unmatched windows stay",
			obs:  Snapshot{AccountKey: "a", Windows: []Window{win("5h", 30)}, FetchedAt: obsAt, Source: SourcePassive},
			want: Snapshot{
				AccountKey: "a", ProviderType: "claude", Plan: "pro", Credits: "12",
				Windows:   []Window{win("5h", 30), win("weekly", 40)},
				Budgets:   []Budget{{Label: "extra", Used: 3}},
				FetchedAt: obsAt, Source: SourcePassive,
			},
		},
		{
			name: "new label is appended",
			obs:  Snapshot{AccountKey: "a", Windows: []Window{win("daily", 5)}, FetchedAt: obsAt, Source: SourcePassive},
			want: Snapshot{
				AccountKey: "a", ProviderType: "claude", Plan: "pro", Credits: "12",
				Windows:   []Window{win("5h", 10), win("weekly", 40), win("daily", 5)},
				Budgets:   []Budget{{Label: "extra", Used: 3}},
				FetchedAt: obsAt, Source: SourcePassive,
			},
		},
		{
			name: "empty plan and credits and budgets keep previous values",
			obs:  Snapshot{AccountKey: "a", FetchedAt: obsAt, Source: SourcePassive},
			want: Snapshot{
				AccountKey: "a", ProviderType: "claude", Plan: "pro", Credits: "12",
				Windows:   []Window{win("5h", 10), win("weekly", 40)},
				Budgets:   []Budget{{Label: "extra", Used: 3}},
				FetchedAt: obsAt, Source: SourcePassive,
			},
		},
		{
			name: "non-empty plan, credits, and budgets replace previous values",
			obs: Snapshot{
				AccountKey: "a", Plan: "max", Credits: "5",
				Budgets:   []Budget{{Label: "extra", Used: 9}},
				FetchedAt: obsAt, Source: SourcePassive,
			},
			want: Snapshot{
				AccountKey: "a", ProviderType: "claude", Plan: "max", Credits: "5",
				Windows:   []Window{win("5h", 10), win("weekly", 40)},
				Budgets:   []Budget{{Label: "extra", Used: 9}},
				FetchedAt: obsAt, Source: SourcePassive,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergePassive(prev, tt.obs)
			if !sameState(got, tt.want) || !got.FetchedAt.Equal(tt.want.FetchedAt) || got.Source != tt.want.Source {
				t.Errorf("mergePassive = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMergePassiveDoesNotAliasPrevious(t *testing.T) {
	prev := Snapshot{AccountKey: "a", Windows: []Window{win("5h", 10)}, FetchedAt: testBase}
	_ = mergePassive(prev, Snapshot{AccountKey: "a", Windows: []Window{win("5h", 30)}, FetchedAt: testBase})
	if prev.Windows[0].UsedPercent != 10 {
		t.Errorf("merge modified previous snapshot window to %v, want 10", prev.Windows[0].UsedPercent)
	}
}
