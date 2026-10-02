package agent

import (
	"sort"
	"strings"
)

// DelegationGroupLedgerVersion is the current DelegationGroupLedger format version.
const DelegationGroupLedgerVersion = 1

// DelegationGroupLedger is the durable set of names reserved by one runtime scope.
type DelegationGroupLedger struct {
	Version int      `json:"version"`
	Names   []string `json:"names"`
}

// NormalizeDelegationGroup returns name with surrounding whitespace trimmed. It
// is the single rule for delegation group names; an empty result means no group.
func NormalizeDelegationGroup(name string) string {
	return strings.TrimSpace(name)
}

// Clone returns an independent ledger with normalized, sorted names.
func (l DelegationGroupLedger) Clone() DelegationGroupLedger {
	out := DelegationGroupLedger{Version: l.Version}
	for _, name := range l.Names {
		name = NormalizeDelegationGroup(name)
		if name != "" {
			out.Names = append(out.Names, name)
		}
	}
	sort.Strings(out.Names)
	out.Names = uniqueGroupNames(out.Names)
	return out
}

// CloneDelegationGroupLedger returns a cloned ledger, or nil for a nil ledger.
func CloneDelegationGroupLedger(l *DelegationGroupLedger) *DelegationGroupLedger {
	if l == nil {
		return nil
	}
	clone := l.Clone()
	return &clone
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
