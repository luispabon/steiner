package tui

// raceSample returns cases unchanged in a normal build and only the first one
// under -race. The consistency tests that use it compare cached or incremental
// output against a fresh render on a single goroutine, so race instrumentation
// finds nothing in them and only multiplies their cost. The non-race CI job
// runs every case; the race build keeps one so `make check`, which runs only
// the race suite, still exercises each path. Callers list a representative
// case first.
func raceSample[T any](cases []T) []T {
	if raceEnabled && len(cases) > 1 {
		return cases[:1]
	}
	return cases
}

// raceSeeds returns n in a normal build and 1 under -race; see raceSample.
func raceSeeds(n uint64) uint64 {
	if raceEnabled {
		return 1
	}
	return n
}
