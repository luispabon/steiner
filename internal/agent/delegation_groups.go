package agent

import (
	"fmt"
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

// Validate checks the ledger version and canonical name constraints.
func (l DelegationGroupLedger) Validate() error {
	if l.Version != 1 {
		return fmt.Errorf("unsupported delegation group ledger version %d", l.Version)
	}
	seen := make(map[string]struct{}, len(l.Names))
	for _, name := range l.Names {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
			return fmt.Errorf("delegation group ledger names must be non-empty and trimmed")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate delegation group ledger name %q", name)
		}
		seen[name] = struct{}{}
	}
	return nil
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
