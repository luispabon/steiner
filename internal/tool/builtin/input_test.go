package builtin

import (
	"testing"
)

func TestNormalizeRead(t *testing.T) {
	t.Run("defaults offset to 1 and limit to defaultReadLimit", func(t *testing.T) {
		in := &ReadInput{}
		NormalizeRead(in)
		if in.Offset != 1 {
			t.Errorf("Offset = %d, want 1", in.Offset)
		}
		if in.Limit != defaultReadLimit {
			t.Errorf("Limit = %d, want %d", in.Limit, defaultReadLimit)
		}
	})

	t.Run("caps limit at maxReadLimit", func(t *testing.T) {
		in := &ReadInput{Offset: 1, Limit: 2000}
		NormalizeRead(in)
		if in.Limit != maxReadLimit {
			t.Errorf("Limit = %d, want %d", in.Limit, maxReadLimit)
		}
	})

	t.Run("preserves valid offset", func(t *testing.T) {
		in := &ReadInput{Offset: 10, Limit: 50}
		NormalizeRead(in)
		if in.Offset != 10 {
			t.Errorf("Offset = %d, want 10", in.Offset)
		}
	})

	t.Run("corrects zero offset to 1", func(t *testing.T) {
		in := &ReadInput{Offset: 0, Limit: 50}
		NormalizeRead(in)
		if in.Offset != 1 {
			t.Errorf("Offset = %d, want 1", in.Offset)
		}
	})

	t.Run("corrects negative offset to 1", func(t *testing.T) {
		in := &ReadInput{Offset: -5, Limit: 50}
		NormalizeRead(in)
		if in.Offset != 1 {
			t.Errorf("Offset = %d, want 1", in.Offset)
		}
	})
}

func TestNormalizeGlob(t *testing.T) {
	t.Run("defaults offset to 0 and limit to defaultGlobLimit", func(t *testing.T) {
		in := &GlobInput{}
		NormalizeGlob(in)
		if in.Offset != 0 {
			t.Errorf("Offset = %d, want 0", in.Offset)
		}
		if in.Limit != defaultGlobLimit {
			t.Errorf("Limit = %d, want %d", in.Limit, defaultGlobLimit)
		}
	})

	t.Run("caps limit at maxGlobLimit", func(t *testing.T) {
		in := &GlobInput{Offset: 0, Limit: 2000}
		NormalizeGlob(in)
		if in.Limit != maxGlobLimit {
			t.Errorf("Limit = %d, want %d", in.Limit, maxGlobLimit)
		}
	})

	t.Run("corrects negative offset to 0", func(t *testing.T) {
		in := &GlobInput{Offset: -1, Limit: 50}
		NormalizeGlob(in)
		if in.Offset != 0 {
			t.Errorf("Offset = %d, want 0", in.Offset)
		}
	})
}

func TestNormalizeGrep(t *testing.T) {
	t.Run("defaults head_limit to defaultGrepHeadLimit", func(t *testing.T) {
		in := &GrepInput{}
		NormalizeGrep(in)
		if in.HeadLimit != defaultGrepHeadLimit {
			t.Errorf("HeadLimit = %d, want %d", in.HeadLimit, defaultGrepHeadLimit)
		}
	})

	t.Run("caps head_limit at maxGrepHeadLimit", func(t *testing.T) {
		in := &GrepInput{HeadLimit: 1000}
		NormalizeGrep(in)
		if in.HeadLimit != maxGrepHeadLimit {
			t.Errorf("HeadLimit = %d, want %d", in.HeadLimit, maxGrepHeadLimit)
		}
	})

	t.Run("defaults offset to 0", func(t *testing.T) {
		in := &GrepInput{}
		NormalizeGrep(in)
		if in.Offset != 0 {
			t.Errorf("Offset = %d, want 0", in.Offset)
		}
	})

	t.Run("corrects negative offset to 0", func(t *testing.T) {
		in := &GrepInput{Offset: -5}
		NormalizeGrep(in)
		if in.Offset != 0 {
			t.Errorf("Offset = %d, want 0", in.Offset)
		}
	})
}

func TestNormalizeLS(t *testing.T) {
	t.Run("defaults offset to 0 and limit to defaultLSLimit", func(t *testing.T) {
		in := &LSInput{}
		NormalizeLS(in)
		if in.Offset != 0 {
			t.Errorf("Offset = %d, want 0", in.Offset)
		}
		if in.Limit != defaultLSLimit {
			t.Errorf("Limit = %d, want %d", in.Limit, defaultLSLimit)
		}
	})

	t.Run("caps limit at maxLSLimit", func(t *testing.T) {
		in := &LSInput{Offset: 0, Limit: 2000}
		NormalizeLS(in)
		if in.Limit != maxLSLimit {
			t.Errorf("Limit = %d, want %d", in.Limit, maxLSLimit)
		}
	})

	t.Run("corrects negative offset to 0", func(t *testing.T) {
		in := &LSInput{Offset: -1}
		NormalizeLS(in)
		if in.Offset != 0 {
			t.Errorf("Offset = %d, want 0", in.Offset)
		}
	})
}

func TestNormalizeBash(t *testing.T) {
	t.Run("defaults timeout and max_output_chars", func(t *testing.T) {
		in := &BashInput{}
		NormalizeBash(in)
		if in.TimeoutSeconds != defaultBashTimeoutSeconds {
			t.Errorf("TimeoutSeconds = %d, want %d", in.TimeoutSeconds, defaultBashTimeoutSeconds)
		}
		if in.MaxOutputChars != defaultBashMaxOutputChars {
			t.Errorf("MaxOutputChars = %d, want %d", in.MaxOutputChars, defaultBashMaxOutputChars)
		}
	})

	t.Run("caps timeout at maxBashTimeoutSeconds", func(t *testing.T) {
		in := &BashInput{TimeoutSeconds: 500}
		NormalizeBash(in)
		if in.TimeoutSeconds != maxBashTimeoutSeconds {
			t.Errorf("TimeoutSeconds = %d, want %d", in.TimeoutSeconds, maxBashTimeoutSeconds)
		}
	})

	t.Run("caps max_output_chars at maxBashMaxOutputChars", func(t *testing.T) {
		in := &BashInput{MaxOutputChars: 500000}
		NormalizeBash(in)
		if in.MaxOutputChars != maxBashMaxOutputChars {
			t.Errorf("MaxOutputChars = %d, want %d", in.MaxOutputChars, maxBashMaxOutputChars)
		}
	})

	t.Run("preserves valid values", func(t *testing.T) {
		in := &BashInput{TimeoutSeconds: 10, MaxOutputChars: 5000}
		NormalizeBash(in)
		if in.TimeoutSeconds != 10 {
			t.Errorf("TimeoutSeconds = %d, want 10", in.TimeoutSeconds)
		}
		if in.MaxOutputChars != 5000 {
			t.Errorf("MaxOutputChars = %d, want 5000", in.MaxOutputChars)
		}
	})

	t.Run("corrects zero timeout to default", func(t *testing.T) {
		in := &BashInput{TimeoutSeconds: 0, MaxOutputChars: 100}
		NormalizeBash(in)
		if in.TimeoutSeconds != defaultBashTimeoutSeconds {
			t.Errorf("TimeoutSeconds = %d, want %d", in.TimeoutSeconds, defaultBashTimeoutSeconds)
		}
	})

	t.Run("corrects negative timeout to default", func(t *testing.T) {
		in := &BashInput{TimeoutSeconds: -1}
		NormalizeBash(in)
		if in.TimeoutSeconds != defaultBashTimeoutSeconds {
			t.Errorf("TimeoutSeconds = %d, want %d", in.TimeoutSeconds, defaultBashTimeoutSeconds)
		}
	})
}
