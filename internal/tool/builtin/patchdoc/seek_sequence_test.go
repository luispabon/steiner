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

func TestSeekSequenceUnicodeNormalise(t *testing.T) {
	t.Parallel()

	lines := []string{"alpha\u00A0\u2014beta", "\u201Cquoted\u201D"}
	pattern := []string{"alpha -beta", "\"quoted\""}

	got, ok := SeekSequence(lines, pattern, 0, false)
	if !ok || got != 0 {
		t.Fatalf("SeekSequence() = (%d,%v), want (0,true)", got, ok)
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
