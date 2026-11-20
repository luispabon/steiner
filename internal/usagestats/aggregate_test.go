package usagestats

import (
	"reflect"
	"testing"
	"time"
)

func TestWindowSince_hourBoundary(t *testing.T) {
	// The clock is 10:30; buckets exist for 09:00 and 10:00.
	tests := []struct {
		name         string
		from         time.Time
		wantRequests int
	}{
		{"before both buckets", prevHour.Add(-time.Hour), 2},
		{"start of the earlier bucket", prevHour, 2},
		{"mid-bucket still includes the whole bucket", prevHour.Add(30 * time.Minute), 2},
		{"current bucket only", baseTime, 1},
		{"after the current bucket", baseTime.Add(time.Hour), 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateTest(t)
			r := newTestRecorder(t, fixedClock(baseTime))
			r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 10, At: prevHour})
			r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 10, At: baseTime})

			if got := r.WindowSince(tc.from).Total.Requests; got != tc.wantRequests {
				t.Errorf("WindowSince(%s) requests = %d, want %d", tc.from.Format(time.RFC3339), got, tc.wantRequests)
			}
		})
	}
}

func TestWindow_delegatesToWindowSince(t *testing.T) {
	isolateTest(t)
	r := newTestRecorder(t, fixedClock(baseTime))
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 10, At: prevHour})
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 10, At: baseTime})

	if got, want := r.Window(time.Hour), r.WindowSince(baseTime.Add(-time.Hour)); !reflect.DeepEqual(got, want) {
		t.Errorf("Window(1h) = %+v, want WindowSince(now-1h) = %+v", got, want)
	}
}

func TestWindow_reportAggregatesAndOrdering(t *testing.T) {
	isolateTest(t)
	r := newTestRecorder(t, fixedClock(baseTime))
	r.Record(Observation{ProviderAlias: "zebra", ProviderType: "openai", BackendModelID: "m1", PromptTokens: 10, Source: SourceParent, At: baseTime})
	r.Record(Observation{ProviderAlias: "alpha", ProviderType: "anthropic", BackendModelID: "m2", PromptTokens: 20, Source: SourceSubAgent, At: baseTime})
	r.Record(Observation{ProviderAlias: "alpha", ProviderType: "anthropic", BackendModelID: "m1", PromptTokens: 30, Source: SourceAdvisor, At: baseTime})

	report := r.Window(time.Hour)

	if report.Models != 2 {
		t.Errorf("Models = %d, want 2", report.Models)
	}
	if report.Providers != 2 {
		t.Errorf("Providers = %d, want 2", report.Providers)
	}
	wantTotal := Totals{Requests: 3, InputTokens: 60}
	if report.Total != wantTotal {
		t.Errorf("Total = %+v, want %+v", report.Total, wantTotal)
	}

	wantRows := []struct{ alias, model string }{
		{"alpha", "m1"},
		{"alpha", "m2"},
		{"zebra", "m1"},
	}
	if len(report.Rows) != len(wantRows) {
		t.Fatalf("rows = %d, want %d", len(report.Rows), len(wantRows))
	}
	for i, want := range wantRows {
		row := report.Rows[i]
		if row.ProviderAlias != want.alias || row.BackendModelID != want.model {
			t.Errorf("row %d = %s/%s, want %s/%s", i, row.ProviderAlias, row.BackendModelID, want.alias, want.model)
		}
	}
}

func TestBySource_ordersAndFilters(t *testing.T) {
	isolateTest(t)
	r := newTestRecorder(t, fixedClock(baseTime))
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 10, Source: SourceSubAgent, At: baseTime})
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 20, Source: SourceAdvisor, At: baseTime})
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 30, Source: SourceParent, At: baseTime})
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 40, Source: SourceUnknown, At: baseTime})
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 50, Source: SourceParent, At: prevHour.Add(-2 * time.Hour)})

	got := r.BySource(baseTime.Add(-time.Hour))
	want := []SourceTotals{
		{Source: SourceParent, Totals: Totals{Requests: 1, InputTokens: 30}},
		{Source: SourceSubAgent, Totals: Totals{Requests: 1, InputTokens: 10}},
		{Source: SourceAdvisor, Totals: Totals{Requests: 1, InputTokens: 20}},
		{Source: SourceUnknown, Totals: Totals{Requests: 1, InputTokens: 40}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BySource = %+v, want %+v", got, want)
	}
}

func TestDaily_zeroFillAndOrder(t *testing.T) {
	isolateTest(t)
	r := newTestRecorder(t, fixedClock(baseTime))
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 10, At: time.Date(2024, 1, 1, 5, 0, 0, 0, time.UTC)})
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 20, At: time.Date(2024, 1, 3, 23, 30, 0, 0, time.UTC)})

	got := r.Daily(
		time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 4, 2, 0, 0, 0, time.UTC),
	)
	want := []PeriodTotals{
		{Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Totals: Totals{Requests: 1, InputTokens: 10}},
		{Start: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
		{Start: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC), Totals: Totals{Requests: 1, InputTokens: 20}},
		{Start: time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Daily = %+v, want %+v", got, want)
	}

	if reversed := r.Daily(want[3].Start, want[0].Start); reversed != nil {
		t.Errorf("Daily with to before from = %+v, want nil", reversed)
	}
}

func TestWeekly_boundaries(t *testing.T) {
	// 2024-01-14 is a Sunday; 2024-01-15 is a Monday.
	sundayLate := time.Date(2024, 1, 14, 23, 59, 0, 0, time.UTC)
	mondayEarly := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	isolateTest(t)
	r := newTestRecorder(t, fixedClock(baseTime))
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 10, At: sundayLate})
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 20, At: mondayEarly})

	got := r.Weekly(time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC))
	want := []PeriodTotals{
		{Start: time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC), Totals: Totals{Requests: 1, InputTokens: 10}},
		{Start: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Totals: Totals{Requests: 1, InputTokens: 20}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Weekly = %+v, want %+v", got, want)
	}
}

func TestWeekly_zeroFill(t *testing.T) {
	isolateTest(t)
	r := newTestRecorder(t, fixedClock(baseTime))
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 10, At: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)})
	r.Record(Observation{ProviderAlias: "p", BackendModelID: "m", PromptTokens: 20, At: time.Date(2024, 1, 21, 23, 0, 0, 0, time.UTC)})

	got := r.Weekly(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 21, 23, 0, 0, 0, time.UTC))
	if len(got) != 3 {
		t.Fatalf("weeks = %d, want 3", len(got))
	}
	wantRequests := []int{1, 0, 1}
	for i, want := range wantRequests {
		if got[i].Requests != want {
			t.Errorf("week %d requests = %d, want %d", i, got[i].Requests, want)
		}
	}
}

func TestSessionWindow_perModelAndSource(t *testing.T) {
	isolateTest(t)
	r := newTestRecorder(t, fixedClock(baseTime))
	r.Record(Observation{ProviderAlias: "p", ProviderType: "t", BackendModelID: "m1", PromptTokens: 100, CacheReadTokens: 50, CompletionTokens: 10, Source: SourceParent, At: baseTime})
	r.Record(Observation{ProviderAlias: "p", ProviderType: "t", BackendModelID: "m1", PromptTokens: 20, Source: SourceAdvisor, At: baseTime})
	r.Record(Observation{ProviderAlias: "p", ProviderType: "t", BackendModelID: "m2", PromptTokens: 30, Source: SourceSubAgent, At: baseTime})

	got := r.SessionWindow()
	want := Report{
		Rows: []Row{
			{
				ProviderAlias: "p", ProviderType: "t", BackendModelID: "m1",
				Totals: Totals{Requests: 2, InputTokens: 70, CacheReadTokens: 50, CompletionTokens: 10},
				Sources: []SourceTotals{
					{Source: SourceParent, Totals: Totals{Requests: 1, InputTokens: 50, CacheReadTokens: 50, CompletionTokens: 10}},
					{Source: SourceAdvisor, Totals: Totals{Requests: 1, InputTokens: 20}},
				},
			},
			{
				ProviderAlias: "p", ProviderType: "t", BackendModelID: "m2",
				Totals:  Totals{Requests: 1, InputTokens: 30},
				Sources: []SourceTotals{{Source: SourceSubAgent, Totals: Totals{Requests: 1, InputTokens: 30}}},
			},
		},
		Total:     Totals{Requests: 3, InputTokens: 100, CacheReadTokens: 50, CompletionTokens: 10},
		Models:    2,
		Providers: 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SessionWindow = %+v, want %+v", got, want)
	}

	r.ResetSession()
	if cleared := r.SessionWindow(); len(cleared.Rows) != 0 {
		t.Errorf("SessionWindow after ResetSession = %+v, want no rows", cleared)
	}
}
