package handler

import (
	"math"
	"testing"
)

// detectSubscriptions mirrors HandleSpending's subscription rule: same
// (counterparty, amount) appearing ≥3 times, amount between 1 and 500.
// Returns annual cost extrapolated to 12 charges/year if freq ≥ 8, else
// freq × amount.
type subKey struct {
	counterparty string
	amount       float64
}

type subEntry struct {
	Name       string
	Amount     float64
	Frequency  int
	AnnualCost float64
}

func detectSubscriptions(chargeFreq map[subKey]int) []subEntry {
	out := []subEntry{}
	for key, freq := range chargeFreq {
		if freq < 3 || key.amount <= 1 || key.amount >= 500 {
			continue
		}
		annual := key.amount * 12
		if freq < 8 {
			annual = key.amount * float64(freq)
		}
		out = append(out, subEntry{
			Name:       key.counterparty,
			Amount:     key.amount,
			Frequency:  freq,
			AnnualCost: math.Round(annual*100) / 100,
		})
	}
	return out
}

func TestDetectSubscriptions_MeetsMinimumFrequency(t *testing.T) {
	// Netflix-style: same name + amount 3 times → counts as subscription.
	cf := map[subKey]int{
		{counterparty: "Netflix", amount: 15.99}: 3,
	}
	out := detectSubscriptions(cf)
	if len(out) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(out))
	}
	if math.Abs(out[0].AnnualCost-47.97) > 0.01 {
		t.Errorf("3 × 15.99 → annual = %.2f, want 47.97 (freq < 8 uses actual frequency)", out[0].AnnualCost)
	}
}

func TestDetectSubscriptions_TwiceIsNotASubscription(t *testing.T) {
	// 2 charges isn't enough — a one-off purchase repeated could be a fluke.
	cf := map[subKey]int{
		{counterparty: "Random Store", amount: 25}: 2,
	}
	out := detectSubscriptions(cf)
	if len(out) != 0 {
		t.Errorf("frequency 2 should not produce a subscription, got %d", len(out))
	}
}

func TestDetectSubscriptions_FrequencyAtLeast8ExtrapolatesAnnual(t *testing.T) {
	// 8+ charges → assume monthly recurring → annual = amount × 12.
	cf := map[subKey]int{
		{counterparty: "Spotify", amount: 9.99}: 10,
	}
	out := detectSubscriptions(cf)
	if len(out) != 1 {
		t.Fatalf("expected 1 sub, got %d", len(out))
	}
	if math.Abs(out[0].AnnualCost-(9.99*12)) > 0.01 {
		t.Errorf("freq ≥ 8 → annual = %.2f, want 119.88 (extrapolated to 12 months)", out[0].AnnualCost)
	}
}

func TestDetectSubscriptions_AmountFloorAndCeil(t *testing.T) {
	// ≤ 1 EUR (transaction noise / fees) and ≥ 500 EUR (rent-level, usually
	// not a "subscription") are excluded.
	cf := map[subKey]int{
		{counterparty: "Small Fee", amount: 0.99}: 12,
		{counterparty: "Rent", amount: 500.01}:    12,
		{counterparty: "Gym", amount: 39.99}:      12,
	}
	out := detectSubscriptions(cf)
	if len(out) != 1 {
		t.Fatalf("expected 1 sub (only Gym), got %d", len(out))
	}
	if out[0].Name != "Gym" {
		t.Errorf("kept subscription = %s, want Gym", out[0].Name)
	}
}

// windowedSavingsRate mirrors the trailing-N-month computation in
// HandleSpending: take the last N months from chronologically-sorted
// monthlySeries, sum income and expenses, return (income − expenses) /
// income × 100. N is min(12, len).
type monthRow struct {
	Income, Expenses float64
}

func windowedSavingsRate(monthly []monthRow, windowSize int) (rate float64, windowMonths int) {
	start := len(monthly) - windowSize
	if start < 0 {
		start = 0
	}
	income := 0.0
	expenses := 0.0
	for _, m := range monthly[start:] {
		income += m.Income
		expenses += m.Expenses
	}
	if income > 0 {
		rate = (income - expenses) / income * 100
	}
	windowMonths = len(monthly) - start
	return
}

func TestWindowedSavingsRate_LastYearWindowsCorrectly(t *testing.T) {
	// 24 months of data — first 12 with poor savings, last 12 with strong.
	// Lifetime rate would dilute the recent improvement; the 12m window
	// reflects the current trajectory.
	var monthly []monthRow
	for i := 0; i < 12; i++ {
		monthly = append(monthly, monthRow{Income: 3000, Expenses: 2900}) // ~3% savings
	}
	for i := 0; i < 12; i++ {
		monthly = append(monthly, monthRow{Income: 3000, Expenses: 2100}) // 30% savings
	}
	rate, months := windowedSavingsRate(monthly, 12)
	if months != 12 {
		t.Errorf("window = %d months, want 12", months)
	}
	if math.Abs(rate-30) > 0.5 {
		t.Errorf("windowed savings rate = %.2f%%, want ≈30%%", rate)
	}
}

func TestWindowedSavingsRate_FewerMonthsThanWindow(t *testing.T) {
	// 6 months of data, 12-month window → falls back to all 6.
	monthly := []monthRow{
		{Income: 3000, Expenses: 2400}, {Income: 3000, Expenses: 2400},
		{Income: 3000, Expenses: 2400}, {Income: 3000, Expenses: 2400},
		{Income: 3000, Expenses: 2400}, {Income: 3000, Expenses: 2400},
	}
	rate, months := windowedSavingsRate(monthly, 12)
	if months != 6 {
		t.Errorf("expected window to fall back to 6 months, got %d", months)
	}
	if math.Abs(rate-20) > 0.5 {
		t.Errorf("savings rate = %.2f%%, want 20%% (600/3000)", rate)
	}
}

func TestWindowedSavingsRate_ZeroIncomeReturnsZero(t *testing.T) {
	monthly := []monthRow{{Income: 0, Expenses: 100}}
	rate, _ := windowedSavingsRate(monthly, 12)
	if rate != 0 {
		t.Errorf("zero income → rate = %.2f, want 0 (no div-by-zero)", rate)
	}
}

func TestWindowedSavingsRate_NegativeWhenExpensesExceedIncome(t *testing.T) {
	monthly := []monthRow{{Income: 1000, Expenses: 1500}}
	rate, _ := windowedSavingsRate(monthly, 12)
	if rate >= 0 {
		t.Errorf("over-spending → expected negative rate, got %.2f", rate)
	}
	// −500/1000 × 100 = −50%
	if math.Abs(rate-(-50)) > 0.5 {
		t.Errorf("savings rate = %.2f, want -50", rate)
	}
}
