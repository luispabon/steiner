package usagestats

import (
	"testing"
	"time"
)

func TestMonthAgo(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "Dec 31 clamps to Nov 30",
			now:  time.Date(2024, 12, 31, 8, 15, 0, 0, time.UTC),
			want: time.Date(2024, 11, 30, 8, 15, 0, 0, time.UTC),
		},
		{
			name: "Jan 31 clamps to Dec 31",
			now:  time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC),
			want: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "Mar 31 clamps to Feb 28",
			now:  time.Date(2025, 3, 31, 12, 0, 0, 0, time.UTC),
			want: time.Date(2025, 2, 28, 12, 0, 0, 0, time.UTC),
		},
		{
			name: "Mar 31 clamps to Feb 29 in a leap year",
			now:  time.Date(2024, 3, 31, 12, 0, 0, 0, time.UTC),
			want: time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC),
		},
		{
			name: "Mar 30 clamps to Feb 28 in a non-leap year",
			now:  time.Date(2025, 3, 30, 6, 0, 0, 0, time.UTC),
			want: time.Date(2025, 2, 28, 6, 0, 0, 0, time.UTC),
		},
		{
			name: "ordinary day keeps day and time",
			now:  time.Date(2024, 5, 15, 6, 45, 30, 0, time.UTC),
			want: time.Date(2024, 4, 15, 6, 45, 30, 0, time.UTC),
		},
		{
			name: "non-UTC input converts to UTC",
			now:  time.Date(2024, 3, 31, 12, 0, 0, 0, time.FixedZone("CEST", 2*60*60)),
			want: time.Date(2024, 2, 29, 10, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MonthAgo(tc.now); !got.Equal(tc.want) {
				t.Errorf("MonthAgo(%s) = %s, want %s", tc.now, got, tc.want)
			}
		})
	}
}
