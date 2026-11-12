package agent

import (
	"reflect"
	"time"
)

// stampMessageTimestamp returns msg with Timestamp set to the current UTC time
// when it is zero. A non-zero timestamp is preserved, so a resumed or replayed
// message keeps its original time.
func stampMessageTimestamp(msg Message) Message {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}
	return msg
}

// stampMessageTimestamps returns a copy of msgs with every zero timestamp
// stamped. A non-zero timestamp is preserved. The caller's slice is left
// untouched, matching WithAppendedMessages' copy semantics.
func stampMessageTimestamps(msgs []Message) []Message {
	stamped := make([]Message, len(msgs))
	for i := range msgs {
		stamped[i] = stampMessageTimestamp(msgs[i])
	}
	return stamped
}

// sameMessages reports whether a and b carry the same messages, ignoring their
// per-message timestamps. Timestamps are display metadata stamped at
// persistence time, so they must not decide whether two conversations are
// semantically equal. Every other field is compared as before.
func sameMessages(a, b []Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		left, right := a[i], b[i]
		left.Timestamp = time.Time{}
		right.Timestamp = time.Time{}
		if !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}
