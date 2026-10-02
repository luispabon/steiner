package agent

import (
	"sort"
	"strings"
)

// DelegationGroupLedger is the durable set of names reserved by one runtime scope.
type DelegationGroupLedger struct {
	Version int      `json:"version"`
	Names   []string `json:"names"`
}

// Clone returns an independent ledger with normalized, sorted names.
func (l DelegationGroupLedger) Clone() DelegationGroupLedger {
	out := DelegationGroupLedger{Version: l.Version}
	for _, name := range l.Names {
		name = strings.TrimSpace(name)
		if name != "" {
			out.Names = append(out.Names, name)
		}
	}
	sort.Strings(out.Names)
	out.Names = uniqueGroupNames(out.Names)
	return out
}

func uniqueGroupNames(names []string) []string {
	if len(names) < 2 {
		return names
	}
	out := names[:1]
	for _, name := range names[1:] {
		if name != out[len(out)-1] {
			out = append(out, name)
		}
	}
	return out
}
