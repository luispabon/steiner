//go:build linux

package tui

import "syscall"

// processCPUNanos returns the whole-process user+system CPU time in nanoseconds.
// It exposes renderer flush cost, which viewNanos (event-loop only) cannot see.
func processCPUNanos() int64 {
	var ru syscall.Rusage
	// Getrusage failing only loses the CPU metric; timing guards tolerate 0.
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return ru.Utime.Nano() + ru.Stime.Nano()
}
