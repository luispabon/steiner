//go:build race

package agent

// raceEnabled is consumed only by the perfguard-tagged allocation-ceiling
// guard; this variant applies when the perfguard suite runs under -race.
//
//nolint:unused
const raceEnabled = true
