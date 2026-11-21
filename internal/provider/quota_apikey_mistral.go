package provider

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/quota"
)

// apiKeyQuotaMistral fetches {base}/billing/budget and
// {base}/billing/subscription. Only the nested api_budget and vibe_budget
// objects are mapped: the top-level fields repeat api_budget, so counting both
// would double the usage. The subscription call is best-effort, so its failure
// leaves Plan empty but keeps the budgets. Both endpoints are undocumented and
// in beta, so unknown fields are ignored. The used amount is derived from both
// an initial budget and a usage percentage, so a budget object missing either
// one carries no usable amount and is skipped; a response that reports neither
// budget yields no budgets.
func (f *apiKeyQuotaFetcher) apiKeyQuotaMistral(ctx context.Context) (string, []quota.Budget, error) {
	const label = "billing/budget"
	type budget struct {
		UsagePercentage *float64 `json:"usage_percentage"`
		InitialBudget   *float64 `json:"initial_budget"`
		Currency        string   `json:"currency"`
		ResetsAt        *string  `json:"reset_at"`
	}
	// A null body decodes as a nil pointer; an object with both budgets null is
	// valid and yields no budgets, matching the other API-key providers.
	var body *struct {
		APIBudget  *budget `json:"api_budget"`
		VibeBudget *budget `json:"vibe_budget"`
	}
	if err := f.apiKeyQuotaGet(ctx, f.base+"/billing/budget", label, &body); err != nil {
		return "", nil, err
	}
	if body == nil {
		return "", nil, fmt.Errorf("%s: bad response", label)
	}
	specs := []struct {
		label  string
		budget *budget
	}{
		{"api", body.APIBudget},
		{"vibe", body.VibeBudget},
	}
	budgets := make([]quota.Budget, 0, len(specs))
	for _, s := range specs {
		// The API reports a 0-100 percentage, not an absolute used amount, so a
		// budget row needs both fields; skipping partial rows keeps a false zero
		// out of the dashboard and the stored history trends.
		if s.budget == nil || s.budget.InitialBudget == nil || s.budget.UsagePercentage == nil {
			continue
		}
		budgets = append(budgets, quota.Budget{
			Label:    s.label,
			Used:     *s.budget.InitialBudget * *s.budget.UsagePercentage / 100,
			Limit:    s.budget.InitialBudget,
			Currency: s.budget.Currency,
			ResetsAt: apiKeyQuotaParseTime(s.budget.ResetsAt),
		})
	}
	var sub struct {
		PlanName string `json:"plan_name"`
	}
	var plan string
	// The plan is supplementary; a failed subscription call must not drop the
	// budgets that were already read.
	if err := f.apiKeyQuotaGet(ctx, f.base+"/billing/subscription", "billing/subscription", &sub); err == nil {
		plan = sub.PlanName
	}
	return plan, budgets, nil
}
