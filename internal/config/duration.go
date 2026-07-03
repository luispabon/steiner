package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Duration is a wrapper around int64 nanoseconds that can be unmarshalled from
// YAML strings like "30s", "5m", etc.
type Duration struct {
	value int64
	set   bool
}

// newDuration parses a duration string and returns a Duration.
func newDuration(d string) (Duration, error) {
	parsed, err := parseDurationString(d)
	if err != nil {
		return Duration{}, err
	}
	return Duration{value: parsed.Nanoseconds(), set: true}, nil
}

// MustDuration parses a duration string and panics on error.
func MustDuration(d string) Duration {
	parsed, err := newDuration(d)
	if err != nil {
		panic(err)
	}
	return parsed
}

// Duration returns the duration value in nanoseconds.
func (d Duration) Duration() int64 {
	return d.value
}

// IsZero returns true if the duration is not set or is zero.
func (d Duration) IsZero() bool {
	return !d.set || d.value == 0
}

// IsUnset reports whether the duration was never explicitly configured, as
// opposed to being explicitly set to zero. Callers that treat zero as a
// meaningful value (e.g. "disabled") must use this instead of IsZero to seed
// a type-based default without clobbering an explicit zero.
func (d Duration) IsUnset() bool {
	return !d.set
}

// String returns the duration as a string.
func (d Duration) String() string {
	if !d.set {
		return ""
	}
	return formatDurationNanos(d.value)
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) {
	if !d.set {
		return nil, nil
	}
	return d.String(), nil
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a scalar string")
	}
	if node.Tag == "!!null" || strings.TrimSpace(node.Value) == "" {
		*d = Duration{}
		return nil
	}
	parsed, err := parseDurationString(node.Value)
	if err != nil {
		return err
	}
	*d = Duration{value: parsed.Nanoseconds(), set: true}
	return nil
}
