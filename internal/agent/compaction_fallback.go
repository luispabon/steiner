package agent

import "time"

// compactionCacheCold reports whether the provider prompt cache is presumed
// cold at now. A non-positive after disables the check. With no recorded main
// request (zero last.At) the cache is cold. Otherwise it is cold only once more
// than after has passed since last.At.
func compactionCacheCold(now time.Time, last LastMainRequest, after time.Duration) bool {
	if after <= 0 {
		return false
	}
	if last.At.IsZero() {
		return true
	}
	return now.Sub(last.At) > after
}
