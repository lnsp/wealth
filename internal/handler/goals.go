package handler

import (
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	db "github.com/lnsp/wealth/internal/database/generated"
)

// isLiquidAccountType reports whether an account's cash and securities count
// as liquid wealth: everything except pension accounts (locked until
// retirement) and the legacy property / liability account types. Physical
// assets and their loans are not accounts, so they are never liquid. The Net
// Worth "Liquid wealth" card uses the same rule (NetWorth.tsx).
func isLiquidAccountType(accType string) bool {
	switch accType {
	case "pension", "real_estate", "liability":
		return false
	default:
		return true
	}
}

// goalAccount is an account's current EUR balance (cash plus holdings at
// market value) as the goals calculation sees it.
type goalAccount struct {
	Type    string
	Balance float64
}

// goalProgress is one goal in the /portfolio/goals response.
type goalProgress struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	TargetAmount        float64 `json:"target_amount"`
	TargetDate          string  `json:"target_date"`
	CurrentValue        float64 `json:"current_value"`
	ProgressPct         float64 `json:"progress_pct"`
	ProjectedValue      float64 `json:"projected_value"`
	MonthlyContribution float64 `json:"monthly_contribution"`
	AssumedReturnPct    float64 `json:"assumed_return_pct"`
	// Status: "complete", "ahead" (projection ≥ 110 % of target),
	// "on_track" (≥ 100 %), "at_risk" (≥ 80 %) or "behind".
	Status           string  `json:"status"`
	MonthsRemaining  int     `json:"months_remaining"`
	FundingAccountID *string `json:"funding_account_id"`
	// FundingSource says what CurrentValue measures: "account" (the funding
	// account's balance) or "liquid" (a share of liquid wealth).
	FundingSource string `json:"funding_source"`
	Priority      int    `json:"priority"`
	// EstimatedDate is the month ("2006-01") in which the projection first
	// reaches the target; empty when it is already reached or never is
	// within 100 years.
	EstimatedDate string `json:"estimated_date"`
}

// goalFutureValue compounds pv monthly and adds pmt at the end of each month
// for n months: FV = PV·(1+r)^n + PMT·((1+r)^n − 1)/r.
func goalFutureValue(pv, pmt, monthlyRate float64, n int) float64 {
	if n <= 0 {
		return pv
	}
	fn := float64(n)
	if math.Abs(monthlyRate) <= 1e-9 {
		return pv + pmt*fn
	}
	g := math.Pow(1+monthlyRate, fn)
	if math.IsNaN(g) || math.IsInf(g, 0) {
		return pv
	}
	return pv*g + pmt*(g-1)/monthlyRate
}

// goalMonthlyRate converts an annual return in percent to the equivalent
// monthly rate.
func goalMonthlyRate(annualPct float64) float64 {
	annual := annualPct / 100
	if annual <= -0.9999 {
		annual = -0.9999
	}
	return math.Pow(1+annual, 1.0/12.0) - 1
}

// goalStatus classifies a goal from its funded value and the projection at
// the target date.
func goalStatus(current, projected, target float64, months int) string {
	switch {
	case current >= target:
		return "complete"
	case months <= 0:
		return "behind"
	case projected >= target*1.1:
		return "ahead"
	case projected >= target:
		return "on_track"
	case projected >= target*0.8:
		return "at_risk"
	default:
		return "behind"
	}
}

// goalPriority is the stored priority, defaulting to 50 ("long-term").
func goalPriority(g db.FinancialGoal) int {
	if g.Priority <= 0 {
		return 50
	}
	return int(g.Priority)
}

// computeGoalsProgress measures each goal against the money set aside for it.
//
//   - A goal with a funding account counts that account's balance. Goals that
//     share an account draw from it in priority order, each up to its target;
//     the last of them also gets whatever is left, so an over-funded account
//     shows as over-funded rather than vanishing.
//   - Goals without one (or whose account is closed) share liquid wealth:
//     the liquid accounts no goal is linked to, handed out in priority order,
//     each goal capped at its target.
//
// So no euro counts towards two goals, and pension money, property and the
// car never count towards a goal unless a pension account is linked to it.
// The projection compounds only that funded value.
func computeGoalsProgress(goals []db.FinancialGoal, accounts map[uuid.UUID]goalAccount, now time.Time) []goalProgress {
	ordered := make([]db.FinancialGoal, len(goals))
	copy(ordered, goals)
	sort.SliceStable(ordered, func(i, j int) bool {
		pi, pj := goalPriority(ordered[i]), goalPriority(ordered[j])
		if pi != pj {
			return pi < pj
		}
		if !ordered[i].TargetDate.Equal(ordered[j].TargetDate) {
			return ordered[i].TargetDate.Before(ordered[j].TargetDate)
		}
		return ordered[i].ID.String() < ordered[j].ID.String()
	})

	fundedBy := func(g db.FinancialGoal) (uuid.UUID, bool) {
		if g.FundingAccountID == nil {
			return uuid.Nil, false
		}
		if _, ok := accounts[*g.FundingAccountID]; !ok {
			return uuid.Nil, false
		}
		return *g.FundingAccountID, true
	}

	// The last goal (in priority order) drawing on each funding account.
	lastOnAccount := make(map[uuid.UUID]uuid.UUID)
	for _, g := range ordered {
		if acct, ok := fundedBy(g); ok {
			lastOnAccount[acct] = g.ID
		}
	}

	remaining := make(map[uuid.UUID]float64, len(accounts))
	liquidPool := 0.0
	for id, a := range accounts {
		remaining[id] = math.Max(0, a.Balance)
		if _, linked := lastOnAccount[id]; !linked && isLiquidAccountType(a.Type) {
			liquidPool += a.Balance
		}
	}
	liquidPool = math.Max(0, liquidPool)

	current := make(map[uuid.UUID]float64, len(ordered))
	source := make(map[uuid.UUID]string, len(ordered))
	for _, g := range ordered {
		target := math.Max(0, numericToFloat(g.TargetAmount))
		if acct, ok := fundedBy(g); ok {
			take := math.Min(remaining[acct], target)
			if lastOnAccount[acct] == g.ID {
				take = remaining[acct]
			}
			remaining[acct] -= take
			current[g.ID] = take
			source[g.ID] = "account"
			continue
		}
		take := math.Min(liquidPool, target)
		liquidPool -= take
		current[g.ID] = take
		source[g.ID] = "liquid"
	}

	results := make([]goalProgress, 0, len(goals))
	for _, g := range goals { // response keeps the stored order (by target date)
		target := numericToFloat(g.TargetAmount)
		monthly := numericToFloat(g.MonthlyContribution)
		returnPct := numericToFloat(g.AssumedReturnPct)
		cur := current[g.ID]

		months := int(g.TargetDate.Sub(now).Hours() / (24 * 30.44))
		if months < 0 {
			months = 0
		}
		rate := goalMonthlyRate(returnPct)
		projected := goalFutureValue(cur, monthly, rate, months)
		if math.IsNaN(projected) || math.IsInf(projected, 0) {
			projected = cur
		}

		progressPct := 0.0
		if target > 0 {
			progressPct = cur / target * 100
		}
		status := goalStatus(cur, projected, target, months)

		estimated := ""
		if status != "complete" {
			for m := 1; m <= 1200; m++ {
				if goalFutureValue(cur, monthly, rate, m) >= target {
					estimated = time.Date(now.Year(), now.Month()+time.Month(m), 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
					break
				}
			}
		}

		var fundingID *string
		if g.FundingAccountID != nil {
			s := g.FundingAccountID.String()
			fundingID = &s
		}

		results = append(results, goalProgress{
			ID:                  g.ID.String(),
			Name:                g.Name,
			TargetAmount:        target,
			TargetDate:          g.TargetDate.Format("2006-01-02"),
			CurrentValue:        math.Round(cur*100) / 100,
			ProgressPct:         math.Round(progressPct*10) / 10,
			ProjectedValue:      math.Round(projected*100) / 100,
			MonthlyContribution: monthly,
			AssumedReturnPct:    returnPct,
			Status:              status,
			MonthsRemaining:     months,
			FundingAccountID:    fundingID,
			FundingSource:       source[g.ID],
			Priority:            goalPriority(g),
			EstimatedDate:       estimated,
		})
	}
	return results
}
