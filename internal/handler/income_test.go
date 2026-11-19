package handler

import (
	"testing"
	"time"

	"github.com/lnsp/wealth/internal/analytics"
)

func ip(v int) *int         { return &v }
func fp(v float64) *float64 { return &v }
func sp(v string) *string   { return &v }

func TestIncomeStreamParse(t *testing.T) {
	ok := []incomeStreamJSON{
		{Kind: "salary", Name: "Base", Amount: 110000, Growth: 0.03, Month: ip(1), StartDate: "2026-11-19",
			Steps: []incomeStepJSON{{Date: "2027-04-01", Amount: 125000}}},
		{Kind: "bonus", Name: "Bonus", Amount: 0.15, Month: ip(3), PayoutFactor: fp(1), StartDate: "2026-11-19"},
		{Kind: "rsu_refresh", Name: "Refresh", Currency: "usd", Amount: 50000, Month: ip(3), VestMonths: ip(48), VestEvery: ip(1), StartDate: "2026-11-19"},
		{Kind: "one_off", Name: "Signing", Amount: 20000, StartDate: "2027-01-15", EndDate: sp("")},
	}
	for _, s := range ok {
		ps, err := s.parse()
		if err != nil {
			t.Errorf("%s: %v", s.Kind, err)
			continue
		}
		if s.Kind == "rsu_refresh" && ps.params.Currency != "USD" {
			t.Errorf("currency not normalised: %q", ps.params.Currency)
		}
	}
	bad := map[string]incomeStreamJSON{
		"unknown kind":        {Kind: "rent", Name: "x", StartDate: "2026-01-01"},
		"no name":             {Kind: "salary", StartDate: "2026-01-01"},
		"bonus without month": {Kind: "bonus", Name: "b", Amount: 0.1, PayoutFactor: fp(1), StartDate: "2026-01-01"},
		"bonus as euros":      {Kind: "bonus", Name: "b", Amount: 15000, Month: ip(3), PayoutFactor: fp(1), StartDate: "2026-01-01"},
		"odd vesting":         {Kind: "rsu_refresh", Name: "r", Amount: 1, Month: ip(3), VestMonths: ip(48), VestEvery: ip(2), StartDate: "2026-01-01"},
		"steps on a bonus":    {Kind: "bonus", Name: "b", Amount: 0.1, Month: ip(3), PayoutFactor: fp(1), StartDate: "2026-01-01", Steps: []incomeStepJSON{{Date: "2027-01-01", Amount: 1}}},
		"duplicate steps":     {Kind: "salary", Name: "s", Amount: 1, StartDate: "2026-01-01", Steps: []incomeStepJSON{{Date: "2027-01-01", Amount: 1}, {Date: "2027-01-01", Amount: 2}}},
		"end before start":    {Kind: "salary", Name: "s", Amount: 1, StartDate: "2026-01-01", EndDate: sp("2025-01-01")},
		"yen":                 {Kind: "salary", Name: "s", Currency: "JPY", Amount: 1, StartDate: "2026-01-01"},
		"bad month":           {Kind: "salary", Name: "s", Amount: 1, Month: ip(13), StartDate: "2026-01-01"},
	}
	for name, s := range bad {
		if _, err := s.parse(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPlanningProfileValidate(t *testing.T) {
	if err := defaultPlanningProfile().validate(); err != nil {
		t.Fatalf("default profile invalid: %v", err)
	}
	bad := []func(*planningProfile){
		func(p *planningProfile) { p.ChurchRate = 0.07 },
		func(p *planningProfile) { p.HealthKind = "none" },
		func(p *planningProfile) { p.SavingsMode = "vibes" },
		func(p *planningProfile) { p.SavingsRate = 1.5 },
		func(p *planningProfile) { p.RetirementAge = 30 },
		func(p *planningProfile) { v := 0.0; p.USDPerEUR = &v },
		func(p *planningProfile) { y := 1800; p.BirthYear = &y },
	}
	for i, f := range bad {
		p := defaultPlanningProfile()
		f(&p)
		if p.validate() == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestRetirementStart(t *testing.T) {
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	p := defaultPlanningProfile()
	if got := p.retirementStart(now); got.Year() != 2066 {
		t.Errorf("without a birth year: %s", got)
	}
	y := 1991
	p.BirthYear = &y
	if got := p.retirementStart(now); !got.Equal(time.Date(2058, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("born 1991, retiring at 67: %s", got)
	}
}

func TestFireExpensesToday(t *testing.T) {
	in := incomeInputs{profile: defaultPlanningProfile()}
	in.profile.RunningCostsMonthly = 3400
	if got := in.fireExpensesToday(analytics.Schedule{}); got != 40800 {
		t.Errorf("costs mode: %.2f, want 40800 (today's money, not inflated)", got)
	}
	in.profile.SavingsMode = "rate"
	s := analytics.Schedule{Months: []analytics.ScheduleMonth{{BaseNet: 5000, BonusNet: 1000, CashSavings: 2400, HeldNet: 900}}}
	if got := in.fireExpensesToday(s); got != 3600 {
		t.Errorf("rate mode: %.2f, want the unsaved cash 3600", got)
	}
}

func TestContributionPlan(t *testing.T) {
	s := contributionPlan{Schedule: []float64{100, 200, -50}}
	if s.at(1) != 100 || s.at(3) != -50 || s.at(4) != 0 || s.at(0) != 0 {
		t.Errorf("schedule lookup: %v %v %v %v", s.at(1), s.at(3), s.at(4), s.at(0))
	}
	if got := s.total(3); got != 250 {
		t.Errorf("total %v", got)
	}
	low := s.shifted(-150)
	if low.at(1) != 0 || low.at(2) != 50 || low.at(3) != -200 {
		t.Errorf("a cut keeps saving months at zero or above: %v", low.Schedule)
	}
	flat := contributionPlan{Monthly: 300}.shifted(-500)
	if flat.Monthly != 0 {
		t.Errorf("flat cut: %v", flat.Monthly)
	}
}

func TestSimulateFIREConstantScheduleMatchesFlat(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	p := fireParams{CurrentValue: 200000, MonthlyContrib: 3000, AnnualReturn: 0.07, Inflation: 0.02,
		Expenses: 40000, SWR: 0.035, TaxRate: 0.26375, TFS: 0.3, Allowance: 1000, MaxMonths: 600}
	flat := simulateFIRE(p, now)
	sched := make([]float64, 600)
	for i := range sched {
		sched[i] = 3000
	}
	p.Schedule = sched
	got := simulateFIRE(p, now)
	if got.Month != flat.Month || got.Target != flat.Target {
		t.Errorf("schedule month %d target %.0f, flat month %d target %.0f", got.Month, got.Target, flat.Month, flat.Target)
	}
}

func TestFIREHealthRaisesTarget(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	p := fireParams{CurrentValue: 200000, MonthlyContrib: 3000, AnnualReturn: 0.07, Inflation: 0.02,
		Expenses: 40800, SWR: 0.035, TaxRate: 0.26375, TFS: 0.3, Allowance: 1000, MaxMonths: 600}
	base := simulateFIRE(p, now)
	p.Health = 4800
	with := simulateFIRE(p, now)
	if with.Target <= base.Target || with.Month <= base.Month {
		t.Errorf("health insurance should raise the target: %.0f → %.0f, month %d → %d", base.Target, with.Target, base.Month, with.Month)
	}
	// With a pension, health insurance only adds to the bridge years.
	p.Pension, p.CurrentAge, p.PensionAge = 20000, 35, 67
	if p.longRunGap() != 20800 {
		t.Errorf("long-run gap with a pension: %.0f", p.longRunGap())
	}
}

func TestLifeEventStopsScheduledContributions(t *testing.T) {
	plan := contributionPlan{Schedule: []float64{1000, 1000, 5000, 1000, 1000}}
	vals, _ := simulateLifeEvents(0, plan, 0, []lifeEventSim{{MonthOffset: 3, StopsContrib: true}}, 5)
	if got := vals[5]; got != 2000 {
		t.Errorf("value after stop: %.0f, want 2000 (the stop applies from its own month)", got)
	}
}
