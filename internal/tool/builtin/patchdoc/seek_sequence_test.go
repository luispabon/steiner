package patchdoc

import "testing"

func TestSeekSequenceExact(t *testing.T) {
	t.Parallel()

	lines := []string{"alpha", "beta", "gamma"}
	pattern := []string{"beta", "gamma"}

	got, ok := SeekSequence(lines, pattern, 0, false)
	if !ok || got != 1 {
		t.Fatalf("SeekSequence() = (%d,%v), want (1,true)", got, ok)
	}
}

func TestSeekSequenceTrimRight(t *testing.T) {
	t.Parallel()

	lines := []string{"alpha ", "beta\t\t"}
	pattern := []string{"alpha", "beta"}

	got, ok := SeekSequence(lines, pattern, 0, false)
	if !ok || got != 0 {
		t.Fatalf("SeekSequence() = (%d,%v), want (0,true)", got, ok)
	}
}

func TestSeekSequenceTrimBoth(t *testing.T) {
	t.Parallel()

	lines := []string{"  alpha  ", "\t beta\t"}
	pattern := []string{"alpha", "beta"}

	got, ok := SeekSequence(lines, pattern, 0, false)
	if !ok || got != 0 {
		t.Fatalf("SeekSequence() = (%d,%v), want (0,true)", got, ok)
	}
}

func TestSeekSequenceTrimLeadingIndent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		lines   []string
		pattern []string
		want    int
		ok      bool
	}{
		{
			name:    "4-space indent matches 2-space indent",
			lines:   []string{"    alpha"},
			pattern: []string{"  alpha"},
			want:    0,
			ok:      true,
		},
		{
			name:    "tab indent matches space indent",
			lines:   []string{"\talpha"},
			pattern: []string{"  alpha"},
			want:    0,
			ok:      true,
		},
		{
			name:    "no indent matches indented pattern",
			lines:   []string{"alpha"},
			pattern: []string{"    alpha"},
			want:    0,
			ok:      true,
		},
		{
			name:    "different content after indent does not match",
			lines:   []string{"    alpha"},
			pattern: []string{"  beta"},
			want:    0,
			ok:      false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := SeekSequence(tt.lines, tt.pattern, 0, false)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("SeekSequence() = (%d,%v), want (%d,%v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSeekSequenceUnicodeNormalise(t *testing.T) {
	t.Parallel()

	lines := []string{"alpha\u00A0\u2014beta", "\u201Cquoted\u201D"}
	pattern := []string{"alpha -beta", "\"quoted\""}

	got, ok := SeekSequence(lines, pattern, 0, false)
	if !ok || got != 0 {
		t.Fatalf("SeekSequence() = (%d,%v), want (0,true)", got, ok)
	}
}

func TestSeekSequenceFuzzyFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		lines   []string
		pattern []string
		want    int
		ok      bool
	}{
		{
			name:    "single char substitution matches",
			lines:   []string{"alpja"},
			pattern: []string{"alpha"},
			want:    0,
			ok:      true,
		},
		{
			name:    "single char insertion matches",
			lines:   []string{"alphxa"},
			pattern: []string{"alpha"},
			want:    0,
			ok:      true,
		},
		{
			name:    "distance exactly at threshold matches",
			lines:   []string{"abcdefghxy"},
			pattern: []string{"abcdefghij"},
			want:    0,
			ok:      true,
		},
		{
			name:    "distance above threshold does not match",
			lines:   []string{"abxyef"},
			pattern: []string{"abcdef"},
			want:    0,
			ok:      false,
		},
		{
			name:    "short pattern threshold 1",
			lines:   []string{"cot"},
			pattern: []string{"cat"},
			want:    0,
			ok:      true,
		},
		{
			name:    "fuzzy match precedence behind stricter matchers",
			lines:   []string{"abx", "abc"},
			pattern: []string{"abc"},
			want:    1,
			ok:      true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := SeekSequence(tt.lines, tt.pattern, 0, false)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("SeekSequence() = (%d,%v), want (%d,%v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSeekSequenceEOF(t *testing.T) {
	t.Parallel()

	lines := []string{"alpha", "beta", "gamma", "delta"}
	pattern := []string{"gamma", "delta"}

	got, ok := SeekSequence(lines, pattern, 0, true)
	if !ok || got != 2 {
		t.Fatalf("SeekSequence() = (%d,%v), want (2,true)", got, ok)
	}
}

func TestSeekSequenceEmptyPattern(t *testing.T) {
	t.Parallel()

	got, ok := SeekSequence([]string{"alpha"}, nil, 2, false)
	if !ok || got != 2 {
		t.Fatalf("SeekSequence() = (%d,%v), want (2,true)", got, ok)
	}
}

func TestSeekSequencePatternLongerThanLines(t *testing.T) {
	t.Parallel()

	got, ok := SeekSequence([]string{"alpha"}, []string{"alpha", "beta"}, 0, false)
	if ok || got != 0 {
		t.Fatalf("SeekSequence() = (%d,%v), want (0,false)", got, ok)
	}
}

func TestSeekSequenceExactWinsBeforeLooserMatch(t *testing.T) {
	t.Parallel()

	lines := []string{" foo", "bar", "foo", "bar"}
	pattern := []string{"foo", "bar"}

	got, ok := SeekSequence(lines, pattern, 0, false)
	if !ok || got != 2 {
		t.Fatalf("SeekSequence() = (%d,%v), want (2,true)", got, ok)
	}
}

func TestSeekSequenceLiteralApostrophe(t *testing.T) {
	t.Parallel()

	lines := []string{"alpha", "user's setting", "omega"}
	pattern := []string{"user's setting"}

	got, ok := SeekSequence(lines, pattern, 0, false)
	if !ok || got != 1 {
		t.Fatalf("SeekSequence() = (%d,%v), want (1,true)", got, ok)
	}
}
