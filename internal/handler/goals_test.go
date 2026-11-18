package handler

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/lnsp/wealth/internal/database/generated"
)

func testGoal(name string, target float64, due time.Time, monthly, returnPct float64, funding *uuid.UUID, priority int32) db.FinancialGoal {
	return db.FinancialGoal{
		ID:                  uuid.New(),
		Name:                name,
		TargetAmount:        numericFromFloat(target),
		TargetDate:          due,
		MonthlyContribution: numericFromFloat(monthly),
		AssumedReturnPct:    numericFromFloat(returnPct),
		FundingAccountID:    funding,
		Priority:            priority,
	}
}

func goalByName(t *testing.T, gs []goalProgress, name string) goalProgress {
	t.Helper()
	for _, g := range gs {
		if g.Name == name {
			return g
		}
	}
	t.Fatalf("goal %q missing from %+v", name, gs)
	return goalProgress{}
}

// Regression for Net Worth F1: every goal used to get total net worth
// (159,526 € on the seed, apartment and car included), so a 15k emergency
// fund showed 1,063 % and the same euros counted towards every goal.
func TestComputeGoalsProgress_MeasuresFundingAccountAndSharesLiquidWealthOnce(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	checking, brokerage, pension := uuid.New(), uuid.New(), uuid.New()
	accounts := map[uuid.UUID]goalAccount{
		checking:  {Type: "checking", Balance: 20000},
		brokerage: {Type: "brokerage", Balance: 11744},
		pension:   {Type: "pension", Balance: 30000},
	}
	goals := []db.FinancialGoal{
		testGoal("Emergency fund", 15000, now.AddDate(2, 0, 0), 0, 2, &checking, 10),
		testGoal("House", 100000, now.AddDate(4, 0, 0), 1000, 5, nil, 50),
		testGoal("Vacation", 5000, now.AddDate(1, 0, 0), 0, 0, nil, 40),
	}
	got := computeGoalsProgress(goals, accounts, now)
	if len(got) != 3 {
		t.Fatalf("got %d goals, want 3", len(got))
	}

	em := goalByName(t, got, "Emergency fund")
	if em.CurrentValue != 20000 || em.FundingSource != "account" || em.Status != "complete" {
		t.Errorf("emergency fund = %+v, want the checking balance 20,000 (complete, account)", em)
	}
	if em.FundingAccountID == nil || *em.FundingAccountID != checking.String() || em.Priority != 10 {
		t.Errorf("emergency fund must echo funding_account_id and priority, got %v / %d", em.FundingAccountID, em.Priority)
	}
	if math.Abs(em.ProgressPct-133.3) > 0.05 {
		t.Errorf("emergency progress = %.1f %%, want 133.3 %%", em.ProgressPct)
	}

	// Liquid pool is the brokerage only: checking is linked to a goal and the
	// pension is not liquid. Priority 40 (vacation) is served before 50.
	vac := goalByName(t, got, "Vacation")
	if vac.CurrentValue != 5000 || vac.FundingSource != "liquid" || vac.FundingAccountID != nil {
		t.Errorf("vacation = %+v, want 5,000 from liquid wealth", vac)
	}
	house := goalByName(t, got, "House")
	if math.Abs(house.CurrentValue-6744) > 0.005 {
		t.Errorf("house current = %.2f, want the 6,744 left in the liquid pool", house.CurrentValue)
	}
	total := em.CurrentValue + vac.CurrentValue + house.CurrentValue
	if math.Abs(total-31744) > 0.01 {
		t.Errorf("goals count %.2f €, want exactly checking + brokerage = 31,744 (no euro twice, no pension)", total)
	}

	// The projection compounds only the funded 6,744 €.
	months := int(now.AddDate(4, 0, 0).Sub(now).Hours() / (24 * 30.44))
	want := goalFutureValue(6744, 1000, goalMonthlyRate(5), months)
	if math.Abs(house.ProjectedValue-want) > 0.01 {
		t.Errorf("house projected = %.2f, want %.2f", house.ProjectedValue, want)
	}
	if house.Status != "behind" {
		t.Errorf("house status = %q, want behind (projection %.0f of 100,000)", house.Status, house.ProjectedValue)
	}
}

func TestComputeGoalsProgress_SharedAccountInPriorityOrder(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	savings := uuid.New()
	closed := uuid.New() // linked account that is no longer active
	accounts := map[uuid.UUID]goalAccount{savings: {Type: "savings", Balance: 20000}}
	goals := []db.FinancialGoal{
		testGoal("Car", 3000, now.AddDate(1, 0, 0), 0, 0, &savings, 20),
		testGoal("Emergency", 15000, now.AddDate(1, 0, 0), 0, 0, &savings, 10),
		testGoal("Old", 1000, now.AddDate(1, 0, 0), 0, 0, &closed, 30),
	}
	got := computeGoalsProgress(goals, accounts, now)
	if g := goalByName(t, got, "Emergency"); g.CurrentValue != 15000 {
		t.Errorf("emergency (priority 10) = %.2f, want its full 15,000 target", g.CurrentValue)
	}
	// Last goal on the account keeps the remainder, so over-funding shows.
	if g := goalByName(t, got, "Car"); g.CurrentValue != 5000 || g.ProgressPct != 166.7 {
		t.Errorf("car = %.2f (%.1f %%), want the remaining 5,000 (166.7 %%)", g.CurrentValue, g.ProgressPct)
	}
	// A closed funding account falls back to liquid wealth, which is empty
	// here because the only liquid account is linked.
	if g := goalByName(t, got, "Old"); g.CurrentValue != 0 || g.FundingSource != "liquid" {
		t.Errorf("goal on a closed account = %+v, want 0 from liquid wealth", g)
	}
	if got[0].Name != "Car" {
		t.Errorf("response order changed: %q first, want the stored order", got[0].Name)
	}
}

func TestGoalStatus_OnTrackNeedsFullTarget(t *testing.T) {
	cases := []struct {
		current, projected float64
		months             int
		want               string
	}{
		{10000, 10000, 12, "complete"},
		{5000, 20000, 0, "behind"},
		{5000, 11000, 12, "ahead"},
		{5000, 10000, 12, "on_track"},
		{5000, 9999, 12, "at_risk"}, // was "on_track": 80-110 % counted as on track
		{5000, 8000, 12, "at_risk"},
		{5000, 7999, 12, "behind"},
	}
	for _, c := range cases {
		if got := goalStatus(c.current, c.projected, 10000, c.months); got != c.want {
			t.Errorf("goalStatus(%v, %v, 10000, %d) = %q, want %q", c.current, c.projected, c.months, got, c.want)
		}
	}
}

func TestComputeGoalsProgress_EstimatedDate(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	cash := uuid.New()
	accounts := map[uuid.UUID]goalAccount{cash: {Type: "checking", Balance: 0}}
	goals := []db.FinancialGoal{
		testGoal("Saving", 12000, now.AddDate(3, 0, 0), 1000, 0, &cash, 50),
		testGoal("Never", 12000, now.AddDate(3, 0, 0), 0, 0, nil, 50),
	}
	got := computeGoalsProgress(goals, accounts, now)
	if g := goalByName(t, got, "Saving"); g.EstimatedDate != "2027-11" {
		t.Errorf("estimated = %q, want 2027-11 (12 × 1,000 €)", g.EstimatedDate)
	}
	if g := goalByName(t, got, "Never"); g.EstimatedDate != "" {
		t.Errorf("estimated = %q, want empty when the target is never reached", g.EstimatedDate)
	}
}
