package quota

import (
	"slices"
	"strings"
)

// AccountView is the display state of one tracked account. Latest is nil until
// a snapshot exists. The projection slices hold one entry per window or budget
// of Latest, in the same order.
type AccountView struct {
	Key, ProviderType, DisplayAlias string
	Active                          bool
	Latest                          *Snapshot
	Refreshing                      bool
	Err                             string
	Unavailable                     string
	WindowProjections               []Projection
	BudgetProjections               []Projection
}

// Views returns one AccountView per registered account and per unregistered
// account with a snapshot, sorted by DisplayAlias and then Key. DisplayAlias is
// the alphabetically first alias, or the Key when the account has no aliases.
// Active marks the account whose aliases include activeProviderAlias.
func (s *Service) Views(activeProviderAlias string) []AccountView {
	s.ensureLoaded()

	s.mu.Lock()
	views := make([]AccountView, 0, len(s.accounts)+len(s.latest))
	for key, acct := range s.accounts {
		views = append(views, s.viewLocked(key, acct, activeProviderAlias))
	}
	for key := range s.latest {
		if _, registered := s.accounts[key]; !registered {
			views = append(views, s.viewLocked(key, Account{}, activeProviderAlias))
		}
	}
	s.mu.Unlock()

	// Projections read the store, so they are computed with mu released.
	history := s.historyByKey()
	for i := range views {
		v := &views[i]
		if v.Latest == nil {
			continue
		}
		h := history[v.Key]
		for j := range v.Latest.Windows {
			v.WindowProjections = append(v.WindowProjections, ProjectWindow(h, *v.Latest, j))
		}
		for j := range v.Latest.Budgets {
			v.BudgetProjections = append(v.BudgetProjections, ProjectBudget(h, *v.Latest, j))
		}
	}

	slices.SortFunc(views, func(a, b AccountView) int {
		if c := strings.Compare(a.DisplayAlias, b.DisplayAlias); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	return views
}

// viewLocked builds the base view for key without projections. The caller must
// hold s.mu.
func (s *Service) viewLocked(key string, acct Account, activeProviderAlias string) AccountView {
	v := AccountView{
		Key:          key,
		ProviderType: acct.ProviderType,
		DisplayAlias: key,
		Refreshing:   s.inFlight[key],
		Err:          s.errs[key],
		Unavailable:  acct.Unavailable,
	}
	if len(acct.Aliases) > 0 {
		v.DisplayAlias = slices.Min(acct.Aliases)
	}
	v.Active = activeProviderAlias != "" && slices.Contains(acct.Aliases, activeProviderAlias)
	if snap, ok := s.latest[key]; ok {
		v.Latest = &snap
		if v.ProviderType == "" {
			v.ProviderType = snap.ProviderType
		}
	}
	return v
}

// historyByKey reads the store once and groups its snapshots by account key,
// oldest first. It returns nil when there is no store or it cannot be read.
func (s *Service) historyByKey() map[string][]Snapshot {
	if s.store == nil {
		return nil
	}
	snaps, err := s.store.readAll()
	if err != nil {
		s.logf("quota: read history: %v", err)
		return nil
	}
	out := make(map[string][]Snapshot)
	for _, snap := range snaps {
		out[snap.AccountKey] = append(out[snap.AccountKey], snap)
	}
	return out
}
