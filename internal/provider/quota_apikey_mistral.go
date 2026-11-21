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
// in beta, so unknown fields are ignored and missing fields are tolerated.
func (f *apiKeyQuotaFetcher) apiKeyQuotaMistral(ctx context.Context) (string, []quota.Budget, error) {
	const label = "billing/budget"
	type budget struct {
		UsagePercentage *float64 `json:"usage_percentage"`
		InitialBudget   *float64 `json:"initial_budget"`
		Currency        string   `json:"currency"`
		ResetsAt        *string  `json:"reset_at"`
	}
	var body struct {
		APIBudget  *budget `json:"api_budget"`
		VibeBudget *budget `json:"vibe_budget"`
	}
	if err := f.apiKeyQuotaGet(ctx, f.base+"/billing/budget", label, &body); err != nil {
		return "", nil, err
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
		if s.budget == nil {
			continue
		}
		out := quota.Budget{
			Label:    s.label,
			Currency: s.budget.Currency,
			ResetsAt: apiKeyQuotaParseTime(s.budget.ResetsAt),
		}
		if s.budget.InitialBudget != nil {
			out.Limit = s.budget.InitialBudget
			// The API reports a 0-100 percentage, not an absolute used amount.
			if s.budget.UsagePercentage != nil {
				out.Used = *s.budget.InitialBudget * *s.budget.UsagePercentage / 100
			}
		}
		budgets = append(budgets, out)
	}
	if len(budgets) == 0 {
		return "", nil, fmt.Errorf("%s: bad response", label)
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
