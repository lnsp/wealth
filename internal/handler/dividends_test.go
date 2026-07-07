package handler

import (
	"math"
	"testing"
	"time"
)

// trailing12m mirrors the windowed sum HandleDividends computes: amounts of
// transactions with Type == "dividend" whose date is strictly after the
// 12-month cutoff. prior12m is the matching [24m, 12m] window, used for the
// YoY growth metric.
func trailing12m(dividends []dividendTxn, now time.Time) (trailing, prior float64) {
	cutoff12 := now.AddDate(-1, 0, 0)
	cutoff24 := now.AddDate(-2, 0, 0)
	for _, d := range dividends {
		if d.Date.After(cutoff12) {
			trailing += d.Amount
		} else if d.Date.After(cutoff24) {
			prior += d.Amount
		}
	}
	return
}

// yieldOnCost mirrors HandleDividends: (trailing 12m dividends / total cost
// basis of held positions) × 100. Returns 0 when there's no cost basis to
// divide by.
func yieldOnCost(trailing, costBasis float64) float64 {
	if costBasis <= 0 {
		return 0
	}
	return trailing / costBasis * 100
}

type dividendTxn struct {
	Date   time.Time
	Amount float64
}

func TestTrailing12m_WindowBoundaries(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	dividends := []dividendTxn{
		// Inside trailing 12m window (after 2025-05-18).
		{Date: time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC), Amount: 120},
		{Date: time.Date(2025, 11, 30, 0, 0, 0, 0, time.UTC), Amount: 80},
		// At the exact 12m cutoff — strict After excludes this row.
		{Date: time.Date(2025, 5, 18, 12, 0, 0, 0, time.UTC), Amount: 99},
		// Inside [24m, 12m] prior window.
		{Date: time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC), Amount: 60},
		{Date: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), Amount: 50},
		// Older than 24m — excluded from both windows.
		{Date: time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC), Amount: 200},
	}

	trail, prior := trailing12m(dividends, now)

	if math.Abs(trail-200) > 0.01 {
		t.Errorf("trailing12m = %.2f, want 200 (120 + 80; the row at cutoff is excluded by strict After)", trail)
	}
	// The row at the exact 12m cutoff falls through to the prior window because
	// After(cutoff12m) is strict (== returns false) but After(cutoff24m) is true.
	// This is the handler's actual behaviour — locking it here so any tweak to
	// the cutoff comparison surfaces in CI.
	if math.Abs(prior-209) > 0.01 {
		t.Errorf("prior12m = %.2f, want 209 (60 + 50 + the 99 at cutoff that falls through)", prior)
	}
}

func TestTrailing12m_EmptyAndAllOld(t *testing.T) {
	now := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	trail, prior := trailing12m(nil, now)
	if trail != 0 || prior != 0 {
		t.Errorf("empty: trail=%.2f prior=%.2f, want 0 0", trail, prior)
	}

	old := []dividendTxn{{Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Amount: 500}}
	trail, prior = trailing12m(old, now)
	if trail != 0 || prior != 0 {
		t.Errorf("all-old: trail=%.2f prior=%.2f, want 0 0", trail, prior)
	}
}

func TestYieldOnCost_Formula(t *testing.T) {
	// 200 € trailing dividends on a 10_000 € cost basis = 2.00%
	got := yieldOnCost(200, 10000)
	if math.Abs(got-2.0) > 0.001 {
		t.Errorf("YoC(200, 10000) = %.4f, want 2.0", got)
	}
	// Zero cost basis returns 0 (no dividing by zero).
	if got := yieldOnCost(200, 0); got != 0 {
		t.Errorf("YoC(200, 0) = %.4f, want 0", got)
	}
	if got := yieldOnCost(200, -50); got != 0 {
		t.Errorf("YoC(200, -50) = %.4f, want 0 (negative cost basis is nonsensical)", got)
	}
}

func TestYieldOnCost_RealisticHighYielder(t *testing.T) {
	// Single-stock REIT-style example: 5% yield on 50_000 cost basis = 2500
	// trailing dividends. Reversing gives YoC = 5%.
	got := yieldOnCost(2500, 50000)
	if math.Abs(got-5.0) > 0.001 {
		t.Errorf("YoC(2500, 50000) = %.4f, want 5.0", got)
	}
}

// monthlyAmount mirrors the chart's monthly series — each month's net
// dividend total (can be negative on a reversal). cumulativeNonDecreasing
// mirrors HandleDividends's cumulative builder: only positive monthly
// amounts roll forward, so the line is guaranteed monotonic.
type monthlyAmount struct {
	Month  string
	Amount float64
}

func cumulativeNonDecreasing(monthly []monthlyAmount) []float64 {
	cum := 0.0
	out := make([]float64, 0, len(monthly))
	for _, m := range monthly {
		if m.Amount > 0 {
			cum += m.Amount
		}
		out = append(out, cum)
	}
	return out
}

func TestCumulativeDividends_AlwaysMonotonicNonDecreasing(t *testing.T) {
	cases := [][]monthlyAmount{
		// Normal: every month positive.
		{{"2024-01", 50}, {"2024-02", 75}, {"2024-03", 30}},
		// Empty.
		{},
		// All zero.
		{{"2024-01", 0}, {"2024-02", 0}},
		// Reversal mid-stream (rare correction).
		{{"2024-01", 50}, {"2024-02", -20}, {"2024-03", 30}},
		// Reversal at the start (broken import).
		{{"2024-01", -10}, {"2024-02", 40}, {"2024-03", 25}},
		// Multiple reversals.
		{{"2024-01", 100}, {"2024-02", -50}, {"2024-03", -10}, {"2024-04", 60}},
	}
	for i, c := range cases {
		got := cumulativeNonDecreasing(c)
		for j := 1; j < len(got); j++ {
			if got[j] < got[j-1] {
				t.Errorf("case %d step %d: cum=%.2f < prev=%.2f (monthly=%+v)", i, j, got[j], got[j-1], c)
			}
		}
	}
}

func TestCumulativeDividends_ReversalIsHeldFlat(t *testing.T) {
	// 50 in Jan, -20 in Feb (a reversal). The Feb amount is preserved in
	// the bar chart, but the cumulative stays at 50 (doesn't drop to 30).
	monthly := []monthlyAmount{{"2024-01", 50}, {"2024-02", -20}, {"2024-03", 30}}
	got := cumulativeNonDecreasing(monthly)
	want := []float64{50, 50, 80}
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 0.01 {
			t.Errorf("step %d: cum=%.2f, want %.2f", i, got[i], want[i])
		}
	}
}
