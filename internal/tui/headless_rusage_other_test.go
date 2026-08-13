//go:build !linux

package tui

// processCPUNanos is a portable fallback returning 0 on non-Linux platforms.
// CPU-based reporting tolerates 0.
func processCPUNanos() int64 { return 0 }
