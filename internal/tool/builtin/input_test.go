package builtin

import (
	"math"
	"testing"
	"time"
)

func TestNormalizeRead(t *testing.T) {
	t.Run("defaults offset to 1 and limit to defaultReadLimit", func(t *testing.T) {
		in := &ReadInput{}
		normalizeRead(in)
		if in.Offset != 1 {
			t.Errorf("Offset = %d, want 1", in.Offset)
		}
		if in.Limit != defaultReadLimit {
			t.Errorf("Limit = %d, want %d", in.Limit, defaultReadLimit)
		}
	})

	t.Run("caps limit at maxReadLimit", func(t *testing.T) {
		in := &ReadInput{Offset: 1, Limit: 2000}
		normalizeRead(in)
		if in.Limit != maxReadLimit {
			t.Errorf("Limit = %d, want %d", in.Limit, maxReadLimit)
		}
	})

	t.Run("preserves valid offset", func(t *testing.T) {
		in := &ReadInput{Offset: 10, Limit: 50}
		normalizeRead(in)
		if in.Offset != 10 {
			t.Errorf("Offset = %d, want 10", in.Offset)
		}
	})

	t.Run("corrects zero offset to 1", func(t *testing.T) {
		in := &ReadInput{Offset: 0, Limit: 50}
		normalizeRead(in)
		if in.Offset != 1 {
			t.Errorf("Offset = %d, want 1", in.Offset)
		}
	})

	t.Run("corrects negative offset to 1", func(t *testing.T) {
		in := &ReadInput{Offset: -5, Limit: 50}
		normalizeRead(in)
		if in.Offset != 1 {
			t.Errorf("Offset = %d, want 1", in.Offset)
		}
	})
}

func TestNormalizeGlob(t *testing.T) {
	t.Run("defaults offset to 0 and limit to defaultGlobLimit", func(t *testing.T) {
		in := &GlobInput{}
		normalizeGlob(in)
		if in.Offset != 0 {
			t.Errorf("Offset = %d, want 0", in.Offset)
		}
		if in.Limit != defaultGlobLimit {
			t.Errorf("Limit = %d, want %d", in.Limit, defaultGlobLimit)
		}
	})

	t.Run("caps limit at maxGlobLimit", func(t *testing.T) {
		in := &GlobInput{Offset: 0, Limit: 2000}
		normalizeGlob(in)
		if in.Limit != maxGlobLimit {
			t.Errorf("Limit = %d, want %d", in.Limit, maxGlobLimit)
		}
	})

	t.Run("corrects negative offset to 0", func(t *testing.T) {
		in := &GlobInput{Offset: -1, Limit: 50}
		normalizeGlob(in)
		if in.Offset != 0 {
			t.Errorf("Offset = %d, want 0", in.Offset)
		}
	})
}

func TestNormalizeGrep(t *testing.T) {
	t.Run("defaults head_limit to defaultGrepHeadLimit", func(t *testing.T) {
		in := &GrepInput{}
		normalizeGrep(in)
		if in.HeadLimit != defaultGrepHeadLimit {
			t.Errorf("HeadLimit = %d, want %d", in.HeadLimit, defaultGrepHeadLimit)
		}
	})

	t.Run("caps head_limit at maxGrepHeadLimit", func(t *testing.T) {
		in := &GrepInput{HeadLimit: 1000}
		normalizeGrep(in)
		if in.HeadLimit != maxGrepHeadLimit {
			t.Errorf("HeadLimit = %d, want %d", in.HeadLimit, maxGrepHeadLimit)
		}
	})

	t.Run("defaults offset to 0", func(t *testing.T) {
		in := &GrepInput{}
		normalizeGrep(in)
		if in.Offset != 0 {
			t.Errorf("Offset = %d, want 0", in.Offset)
		}
	})

	t.Run("corrects negative offset to 0", func(t *testing.T) {
		in := &GrepInput{Offset: -5}
		normalizeGrep(in)
		if in.Offset != 0 {
			t.Errorf("Offset = %d, want 0", in.Offset)
		}
	})
}

func TestNormalizeLS(t *testing.T) {
	t.Run("defaults offset to 0 and limit to defaultLSLimit", func(t *testing.T) {
		in := &LSInput{}
		normalizeLS(in)
		if in.Offset != 0 {
			t.Errorf("Offset = %d, want 0", in.Offset)
		}
		if in.Limit != defaultLSLimit {
			t.Errorf("Limit = %d, want %d", in.Limit, defaultLSLimit)
		}
	})

	t.Run("caps limit at maxLSLimit", func(t *testing.T) {
		in := &LSInput{Offset: 0, Limit: 2000}
		normalizeLS(in)
		if in.Limit != maxLSLimit {
			t.Errorf("Limit = %d, want %d", in.Limit, maxLSLimit)
		}
	})

	t.Run("corrects negative offset to 0", func(t *testing.T) {
		in := &LSInput{Offset: -1}
		normalizeLS(in)
		if in.Offset != 0 {
			t.Errorf("Offset = %d, want 0", in.Offset)
		}
	})
}

func TestNormalizeBash(t *testing.T) {
	t.Run("defaults timeout and max_output_chars", func(t *testing.T) {
		in := &BashInput{}
		normalizeBash(in, defaultBashTimeoutCapSeconds)
		if in.TimeoutSeconds != defaultBashTimeoutSeconds {
			t.Errorf("TimeoutSeconds = %d, want %d", in.TimeoutSeconds, defaultBashTimeoutSeconds)
		}
		if in.MaxOutputChars != defaultBashMaxOutputChars {
			t.Errorf("MaxOutputChars = %d, want %d", in.MaxOutputChars, defaultBashMaxOutputChars)
		}
	})

	t.Run("caps timeout at configured cap", func(t *testing.T) {
		in := &BashInput{TimeoutSeconds: 500}
		normalizeBash(in, 300)
		if in.TimeoutSeconds != 300 {
			t.Errorf("TimeoutSeconds = %d, want 300", in.TimeoutSeconds)
		}
	})

	t.Run("preserves request equal to configured cap", func(t *testing.T) {
		in := &BashInput{TimeoutSeconds: 300}
		normalizeBash(in, 300)
		if in.TimeoutSeconds != 300 {
			t.Errorf("TimeoutSeconds = %d, want 300", in.TimeoutSeconds)
		}
	})

	t.Run("defaults request to cap when cap is below 30", func(t *testing.T) {
		in := &BashInput{}
		normalizeBash(in, 10)
		if in.TimeoutSeconds != 10 {
			t.Errorf("TimeoutSeconds = %d, want 10", in.TimeoutSeconds)
		}
	})

	t.Run("clamps negative request to cap below the 30s default", func(t *testing.T) {
		in := &BashInput{TimeoutSeconds: -5}
		normalizeBash(in, 10)
		if in.TimeoutSeconds != 10 {
			t.Errorf("TimeoutSeconds = %d, want 10", in.TimeoutSeconds)
		}
	})

	t.Run("very large request clamps without overflow", func(t *testing.T) {
		in := &BashInput{TimeoutSeconds: math.MaxInt}
		normalizeBash(in, 300)
		if in.TimeoutSeconds != 300 {
			t.Errorf("TimeoutSeconds = %d, want 300", in.TimeoutSeconds)
		}
	})

	t.Run("caps max_output_chars at maxBashMaxOutputChars", func(t *testing.T) {
		in := &BashInput{MaxOutputChars: 500000}
		normalizeBash(in, defaultBashTimeoutCapSeconds)
		if in.MaxOutputChars != maxBashMaxOutputChars {
			t.Errorf("MaxOutputChars = %d, want %d", in.MaxOutputChars, maxBashMaxOutputChars)
		}
	})

	t.Run("preserves valid values", func(t *testing.T) {
		in := &BashInput{TimeoutSeconds: 10, MaxOutputChars: 5000}
		normalizeBash(in, defaultBashTimeoutCapSeconds)
		if in.TimeoutSeconds != 10 {
			t.Errorf("TimeoutSeconds = %d, want 10", in.TimeoutSeconds)
		}
		if in.MaxOutputChars != 5000 {
			t.Errorf("MaxOutputChars = %d, want 5000", in.MaxOutputChars)
		}
	})

	t.Run("corrects zero timeout to default", func(t *testing.T) {
		in := &BashInput{TimeoutSeconds: 0, MaxOutputChars: 100}
		normalizeBash(in, defaultBashTimeoutCapSeconds)
		if in.TimeoutSeconds != defaultBashTimeoutSeconds {
			t.Errorf("TimeoutSeconds = %d, want %d", in.TimeoutSeconds, defaultBashTimeoutSeconds)
		}
	})

	t.Run("corrects negative timeout to default", func(t *testing.T) {
		in := &BashInput{TimeoutSeconds: -1}
		normalizeBash(in, defaultBashTimeoutCapSeconds)
		if in.TimeoutSeconds != defaultBashTimeoutSeconds {
			t.Errorf("TimeoutSeconds = %d, want %d", in.TimeoutSeconds, defaultBashTimeoutSeconds)
		}
	})
}

func TestResolveBashTimeoutCapSeconds(t *testing.T) {
	tests := []struct {
		name string
		cap  time.Duration
		want int
	}{
		{"zero falls back to 120", 0, defaultBashTimeoutCapSeconds},
		{"negative falls back to 120", -1 * time.Second, defaultBashTimeoutCapSeconds},
		{"sub-second falls back to 120", 1500 * time.Millisecond, defaultBashTimeoutCapSeconds},
		{"default 120 preserved", 120 * time.Second, 120},
		{"configured 300 preserved", 300 * time.Second, 300},
		{"below 30 preserved", 10 * time.Second, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveBashTimeoutCapSeconds(tt.cap); got != tt.want {
				t.Errorf("resolveBashTimeoutCapSeconds(%v) = %d, want %d", tt.cap, got, tt.want)
			}
		})
	}
}
