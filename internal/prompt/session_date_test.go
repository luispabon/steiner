package prompt

import (
	"strings"
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

func TestSessionDatePartsRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		at         time.Time
		wantDate   string
		wantZone   string
		wantOffset string
	}{
		{
			name:       "BST zone",
			at:         time.Date(2026, 9, 13, 12, 30, 45, 0, time.FixedZone("BST", 3600)),
			wantDate:   "2026-09-13",
			wantZone:   "BST",
			wantOffset: "+01:00",
		},
		{
			name:       "UTC zone",
			at:         time.Date(2026, 9, 13, 12, 30, 45, 0, time.UTC),
			wantDate:   "2026-09-13",
			wantZone:   "UTC",
			wantOffset: "+00:00",
		},
		{
			name:       "negative offset zone",
			at:         time.Date(2026, 9, 13, 12, 30, 45, 0, time.FixedZone("EST", -18000)),
			wantDate:   "2026-09-13",
			wantZone:   "EST",
			wantOffset: "-05:00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := NewSessionDate(tt.at)
			date, zone, offset := original.Parts()
			if date != tt.wantDate || zone != tt.wantZone || offset != tt.wantOffset {
				t.Fatalf("Parts() = (%q, %q, %q), want (%q, %q, %q)", date, zone, offset, tt.wantDate, tt.wantZone, tt.wantOffset)
			}

			restored, ok := NewSessionDateFromParts(date, zone, offset)
			if !ok {
				t.Fatalf("NewSessionDateFromParts(%q, %q, %q) reported invalid", date, zone, offset)
			}
			if got, want := restored.render(), original.render(); got != want {
				t.Fatalf("restored render() = %q, want %q", got, want)
			}
		})
	}
}

func TestSessionDateFromPartsRejectsInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		date   string
		zone   string
		offset string
	}{
		{name: "empty"},
		{name: "missing zone", date: "2026-09-13", offset: "+01:00"},
		{name: "bad date", date: "13/09/2026", zone: "BST", offset: "+01:00"},
		{name: "bad offset", date: "2026-09-13", zone: "BST", offset: "UTC+01:00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NewSessionDateFromParts(tt.date, tt.zone, tt.offset)
			if ok {
				t.Fatalf("NewSessionDateFromParts(%q, %q, %q) ok = true, want false", tt.date, tt.zone, tt.offset)
			}
			if !got.IsZero() {
				t.Fatalf("invalid parts returned non-zero date %v", got)
			}
		})
	}
}

// TestSessionDateRestoredOnLaterDayRendersSameLine pins the cache-prefix
// invariant behind persisting the date: a session started near midnight and
// resumed on the next day in another zone must render the original line, so
// the Codex wire prefix that carries it stays byte-identical.
func TestSessionDateRestoredOnLaterDayRendersSameLine(t *testing.T) {
	t.Parallel()

	started := NewSessionDate(time.Date(2026, 9, 13, 23, 59, 0, 0, time.FixedZone("BST", 3600)))
	date, zone, offset := started.Parts()
	restored, ok := NewSessionDateFromParts(date, zone, offset)
	if !ok {
		t.Fatalf("NewSessionDateFromParts(%q, %q, %q) reported invalid", date, zone, offset)
	}

	want := "Current date: 2026-09-13 (BST, UTC+01:00), recorded when this session started."
	if got := started.render(); got != want {
		t.Fatalf("started render() = %q, want %q", got, want)
	}
	if got := restored.render(); got != want {
		t.Fatalf("restored render() = %q, want %q", got, want)
	}
	if !strings.Contains(restored.render(), "BST") {
		t.Fatalf("restored render() = %q, want the BST zone abbreviation", restored.render())
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
