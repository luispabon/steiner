//go:build !race

package agent

// raceEnabled is consumed only by the perfguard-tagged allocation-ceiling
// guard, so it is dead in the default build (mirrors internal/tui, which
// happens to also reference the const from headless_test.go).
//
//nolint:unused
const raceEnabled = false
