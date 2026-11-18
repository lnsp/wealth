package handler

import (
	"math"
	"testing"
	"time"
)

// These tests drive summarizeDividends, the function HandleDividends uses.
// They used to exercise local copies of the formulas; the cumulative copy
// pinned the old behaviour of ignoring reversals, which the 2026-11-18 audit
// flagged (the line must be the running net sum).

func payout(date string, gross float64) dividendPayment {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return dividendPayment{Date: d, ISIN: "DE000XYZ0001", Gross: gross, Net: gross, QtyHeld: 10}
}

func TestTrailing12m_WindowBoundaries(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	s := summarizeDividends([]dividendPayment{
		// Inside trailing 12m window (after 2025-05-18 12:00).
		payout("2026-04-15", 120),
		payout("2025-11-30", 80),
		// Midnight of the cutoff day is before the 12:00 cutoff: prior window.
		payout("2025-05-18", 99),
		// Inside [24m, 12m] prior window.
		payout("2025-03-15", 60),
		payout("2024-06-01", 50),
		// Older than 24m — excluded from both windows.
		payout("2023-11-01", 200),
	}, map[string]float64{"DE000XYZ0001": 10}, nil, now)

	if math.Abs(s.Trailing12m-200) > 0.01 {
		t.Errorf("trailing12m = %.2f, want 200 (120 + 80)", s.Trailing12m)
	}
	if math.Abs(s.Prior12m-209) > 0.01 {
		t.Errorf("prior12m = %.2f, want 209 (99 + 60 + 50)", s.Prior12m)
	}
	if s.Growth == nil || math.Abs(*s.Growth-(200-209)/209.0*100) > 1e-9 {
		t.Errorf("growth = %v, want %.4f", s.Growth, (200-209)/209.0*100)
	}
}

func TestTrailing12m_EmptyAndAllOld(t *testing.T) {
	now := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	s := summarizeDividends(nil, nil, nil, now)
	if s.Trailing12m != 0 || s.Prior12m != 0 || s.Growth != nil {
		t.Errorf("empty: trail=%.2f prior=%.2f growth=%v, want 0 0 nil", s.Trailing12m, s.Prior12m, s.Growth)
	}
	if s.Monthly == nil || s.Yearly == nil || s.Cumulative == nil || s.Calendar == nil {
		t.Error("empty series must be [] in JSON, not null")
	}

	s = summarizeDividends([]dividendPayment{payout("2020-01-01", 500)}, nil, nil, now)
	if s.Trailing12m != 0 || s.Prior12m != 0 {
		t.Errorf("all-old: trail=%.2f prior=%.2f, want 0 0", s.Trailing12m, s.Prior12m)
	}
	if s.Growth != nil {
		t.Errorf("growth with no prior-year payouts = %v, want nil (shown as —)", *s.Growth)
	}
}

func TestYieldOnCost_OnlyHeldISINs(t *testing.T) {
	// Dividends from a position sold since are not part of the yield on the
	// current holdings' cost.
	now := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	sold := payout("2026-03-01", 300)
	sold.ISIN = "SOLD"
	s := summarizeDividends([]dividendPayment{payout("2026-04-01", 200), sold}, map[string]float64{"DE000XYZ0001": 10}, nil, now)
	if s.Trailing12m != 500 || s.Trailing12mHeld != 200 {
		t.Errorf("trailing %.0f held %.0f, want 500 / 200", s.Trailing12m, s.Trailing12mHeld)
	}
}

func TestCumulativeDividends_IsRunningNetSum(t *testing.T) {
	now := time.Date(2024, 4, 10, 0, 0, 0, 0, time.UTC)
	// 50 in Jan, a −20 reversal in Feb, nothing in March, 30 in April.
	s := summarizeDividends([]dividendPayment{
		payout("2024-01-15", 50), payout("2024-02-15", -20), payout("2024-04-05", 30),
	}, nil, nil, now)
	want := []float64{50, 30, 30, 60}
	if len(s.Cumulative) != len(want) {
		t.Fatalf("cumulative has %d months, want %d (zero months filled)", len(s.Cumulative), len(want))
	}
	for i := range want {
		if math.Abs(s.Cumulative[i].Cumulative-want[i]) > 0.01 {
			t.Errorf("month %s: cum=%.2f, want %.2f", s.Cumulative[i].Month, s.Cumulative[i].Cumulative, want[i])
		}
	}
	if s.Monthly[2].Month != "2024-03" || s.Monthly[2].Amount != 0 {
		t.Errorf("March must be present with 0, got %+v", s.Monthly[2])
	}
	monthly, yearly := 0.0, 0.0
	for _, m := range s.Monthly {
		monthly += m.Amount
	}
	for _, y := range s.Yearly {
		yearly += y.Amount
	}
	if math.Abs(monthly-yearly) > 0.01 || math.Abs(monthly-s.Total) > 0.01 {
		t.Errorf("monthly %.2f, yearly %.2f, total %.2f must agree", monthly, yearly, s.Total)
	}
}
