package prompt

import (
	"testing"
	"time"
)

func TestSessionDateRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		date SessionDate
		want string
	}{
		{
			name: "BST zone",
			date: NewSessionDate(time.Date(2026, 9, 13, 12, 30, 45, 0, time.FixedZone("BST", 3600))),
			want: "Current date: 2026-09-13 (BST, UTC+01:00), recorded when this session started.",
		},
		{
			name: "UTC zone",
			date: NewSessionDate(time.Date(2026, 9, 13, 12, 30, 45, 0, time.UTC)),
			want: "Current date: 2026-09-13 (UTC, UTC+00:00), recorded when this session started.",
		},
		{
			name: "negative offset zone",
			date: NewSessionDate(time.Date(2026, 9, 13, 12, 30, 45, 0, time.FixedZone("EST", -18000))),
			want: "Current date: 2026-09-13 (EST, UTC-05:00), recorded when this session started.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.date.render()
			if got != tt.want {
				t.Fatalf("render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSessionDateSameCalendarDaySameZoneIdentical(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("BST", 3600)
	t1 := NewSessionDate(time.Date(2026, 9, 13, 0, 1, 0, 0, zone))
	t2 := NewSessionDate(time.Date(2026, 9, 13, 23, 59, 0, 0, zone))

	if got1, got2 := t1.render(), t2.render(); got1 != got2 {
		t.Fatalf("render() differs on same calendar day: %q vs %q", got1, got2)
	}
}

func TestSessionDateIsZero(t *testing.T) {
	t.Parallel()

	if !(SessionDate{}).IsZero() {
		t.Fatal("zero SessionDate: IsZero() = false, want true")
	}

	if NewSessionDate(time.Now()).IsZero() {
		t.Fatal("non-zero SessionDate: IsZero() = true, want false")
	}
}
