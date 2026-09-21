package provider

import "time"

// Error renders the readable, single-line message shown to users.
func (e *UsageLimitError) Error() string {
	if e == nil {
		return "<nil>"
	}
	label := e.Model
	if label == "" {
		label = e.Provider
	}
	if label == "" {
		label = "unknown provider"
	}
	var head string
	switch e.Kind {
	case UsageLimitKindQuota:
		head = "provider quota exhausted (" + label
		if e.Code != "" {
			head += ", " + e.Code
		}
		head += ")"
	case UsageLimitKindBudget:
		head = "provider budget exceeded (" + label + ")"
	default:
		head = "provider usage limit reached (" + label
		if e.PlanType != "" {
			head += ", plan: " + e.PlanType
		}
		head += ")"
	}
	if e.Message != "" {
		head += ": " + e.Message
	}
	return head + e.resetTail()
}

func (e *UsageLimitError) resetTail() string {
	if !e.HasReset() {
		if e.Kind == UsageLimitKindBudget {
			return " — no reset time reported"
		}
		return " — no reset time reported; check your plan and billing"
	}
	now := usageLimitNow()
	loc := usageLimitLocation()
	r, n := e.ResetsAt.In(loc), now.In(loc)
	d := r.Sub(now).Round(time.Second)
	switch {
	case d <= 0:
		return " — resets now"
	case r.Year() == n.Year() && r.YearDay() == n.YearDay():
		return " — resets at " + r.Format("15:04") + " local (in " + d.String() + ")"
	default:
		return " — resets " + r.Format("Mon 2 Jan 15:04") + " local (in " + d.String() + ")"
	}
}
