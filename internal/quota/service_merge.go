package quota

// mergePassive overlays a passive observation onto prev and returns a new
// snapshot. Observed windows overwrite earlier windows with the same Label, and
// windows not observed are kept. Empty ProviderType, Plan, and Credits keep the
// previous values. A non-empty Budgets list replaces the previous budgets. The
// result takes the observation's FetchedAt and Source.
func mergePassive(prev, obs Snapshot) Snapshot {
	out := prev
	out.Windows = append([]Window(nil), prev.Windows...)
	for _, w := range obs.Windows {
		out.Windows = upsertWindow(out.Windows, w)
	}
	if obs.ProviderType != "" {
		out.ProviderType = obs.ProviderType
	}
	if obs.Plan != "" {
		out.Plan = obs.Plan
	}
	if obs.Credits != "" {
		out.Credits = obs.Credits
	}
	if len(obs.Budgets) > 0 {
		out.Budgets = append([]Budget(nil), obs.Budgets...)
	}
	out.FetchedAt = obs.FetchedAt
	out.Source = obs.Source
	return out
}

// upsertWindow replaces the window with w's Label in windows, or appends w when
// no window has that Label. It modifies windows in place.
func upsertWindow(windows []Window, w Window) []Window {
	for i := range windows {
		if windows[i].Label == w.Label {
			windows[i] = w
			return windows
		}
	}
	return append(windows, w)
}
