package analytics

import (
	"math"
	"testing"
	"time"
)

// Helper to build daily snapshots from a start date and daily values.
func makeSnapshots(start time.Time, values []float64) []DailyValuation {
	pts := make([]DailyValuation, len(values))
	for i, v := range values {
		pts[i] = DailyValuation{Date: start.AddDate(0, 0, i), Value: v}
	}
	return pts
}

func TestComputeRiskMetrics_EmptyInput(t *testing.T) {
	result := ComputeRiskMetrics(nil, 0.03, 0)
	if result.AnnualizedVolatility != 0 {
		t.Errorf("expected zero volatility for nil input, got %f", result.AnnualizedVolatility)
	}

	result = ComputeRiskMetrics([]DailyValuation{{Date: time.Now(), Value: 100}}, 0.03, 100)
	if result.AnnualizedVolatility != 0 {
		t.Errorf("expected zero volatility for single point, got %f", result.AnnualizedVolatility)
	}
}

func TestComputeRiskMetrics_FlatPortfolio(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	values := make([]float64, 252)
	for i := range values {
		values[i] = 100000
	}
	snapshots := makeSnapshots(start, values)

	result := ComputeRiskMetrics(snapshots, 0.03, 100000)

	if result.AnnualizedVolatility != 0 {
		t.Errorf("flat portfolio should have zero volatility, got %f", result.AnnualizedVolatility)
	}
	if result.MaxDrawdown != 0 {
		t.Errorf("flat portfolio should have zero max drawdown, got %f", result.MaxDrawdown)
	}
}

func TestComputeRiskMetrics_SteadyGrowth(t *testing.T) {
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	// ~10% annual return: daily factor = (1.1)^(1/365)
	dailyFactor := math.Pow(1.1, 1.0/365.0)
	values := make([]float64, 365)
	values[0] = 100000
	for i := 1; i < len(values); i++ {
		values[i] = values[i-1] * dailyFactor
	}
	snapshots := makeSnapshots(start, values)

	result := ComputeRiskMetrics(snapshots, 0.03, values[len(values)-1])

	// Volatility should be very low (tiny daily variation from compounding)
	if result.AnnualizedVolatility > 1 {
		t.Errorf("steady growth volatility should be near zero, got %f%%", result.AnnualizedVolatility)
	}

	// Sharpe ratio should be very high (good return, near-zero vol)
	if result.SharpeRatio < 1 {
		t.Errorf("steady growth Sharpe should be high, got %f", result.SharpeRatio)
	}

	// Max drawdown should be 0 for monotonically increasing
	if result.MaxDrawdown != 0 {
		t.Errorf("monotonically increasing portfolio should have zero max drawdown, got %f", result.MaxDrawdown)
	}
}

func TestMaxDrawdown(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	// Portfolio rises to 110k, drops to 88k (20% drawdown), recovers
	values := []float64{
		100000, 105000, 110000, 100000, 95000, 88000, 90000, 95000, 100000, 110000,
	}
	snapshots := makeSnapshots(start, values)

	ddStart, ddEnd, dd := maxDrawdown(snapshots)

	expectedDD := (110000.0 - 88000.0) / 110000.0 // = 20%
	if math.Abs(dd-expectedDD) > 0.001 {
		t.Errorf("max drawdown = %f, want %f", dd, expectedDD)
	}

	// Peak is at day 2 (110k), trough at day 5 (88k)
	expectedStart := start.AddDate(0, 0, 2)
	expectedEnd := start.AddDate(0, 0, 5)
	if !ddStart.Equal(expectedStart) {
		t.Errorf("drawdown start = %v, want %v", ddStart, expectedStart)
	}
	if !ddEnd.Equal(expectedEnd) {
		t.Errorf("drawdown end = %v, want %v", ddEnd, expectedEnd)
	}
}

func TestMaxDrawdown_NoDrawdown(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	values := []float64{100, 110, 120, 130}
	snapshots := makeSnapshots(start, values)

	_, _, dd := maxDrawdown(snapshots)
	if dd != 0 {
		t.Errorf("monotonically increasing portfolio should have zero drawdown, got %f", dd)
	}
}

func TestDailyReturns(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	values := []float64{100, 110, 105, 115}
	snapshots := makeSnapshots(start, values)

	returns := dailyReturns(snapshots)
	expected := []float64{0.10, -0.04545454, 0.09523809}

	if len(returns) != len(expected) {
		t.Fatalf("expected %d returns, got %d", len(expected), len(returns))
	}
	for i, r := range returns {
		if math.Abs(r-expected[i]) > 0.0001 {
			t.Errorf("return[%d] = %f, want %f", i, r, expected[i])
		}
	}
}

func TestDailyReturns_SkipsZeroValues(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	values := []float64{0, 0, 100, 110}
	snapshots := makeSnapshots(start, values)

	returns := dailyReturns(snapshots)
	// First two have zero values, so only 100->110 generates a return
	if len(returns) != 1 {
		t.Errorf("expected 1 return (skipping zeros), got %d", len(returns))
	}
}

func TestComputeRiskMetrics_WithDrawdown(t *testing.T) {
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	// Build a realistic-ish scenario with a significant drawdown
	values := make([]float64, 365)
	values[0] = 100000
	for i := 1; i < 180; i++ {
		values[i] = values[i-1] * 1.0005 // slow growth
	}
	// Drop 15% over next 30 days
	for i := 180; i < 210; i++ {
		values[i] = values[i-1] * 0.9945
	}
	// Recover slowly
	for i := 210; i < 365; i++ {
		values[i] = values[i-1] * 1.001
	}
	snapshots := makeSnapshots(start, values)

	result := ComputeRiskMetrics(snapshots, 0.03, values[364])

	// Should have some volatility
	if result.AnnualizedVolatility <= 0 {
		t.Error("expected positive volatility")
	}

	// Max drawdown should be roughly 15%
	if result.MaxDrawdown < 10 || result.MaxDrawdown > 20 {
		t.Errorf("expected max drawdown around 15%%, got %f%%", result.MaxDrawdown)
	}

	// VaR should be negative (a loss)
	if result.ValueAtRisk95 >= 0 {
		t.Errorf("expected negative VaR (a loss), got %f", result.ValueAtRisk95)
	}

	// Drawdown series should be present
	if len(result.DrawdownSeries) == 0 {
		t.Error("expected non-empty drawdown series")
	}

	// Drawdown dates should be populated
	if result.MaxDrawdownStart == "" || result.MaxDrawdownEnd == "" {
		t.Error("expected non-empty drawdown dates")
	}
	if result.MaxDrawdownDays <= 0 {
		t.Error("expected positive drawdown duration")
	}
}

func TestSortinoRatio_NoDownside(t *testing.T) {
	// All positive returns should give zero sortino (no downside deviation)
	returns := []float64{0.01, 0.02, 0.015, 0.005}
	result := sortinoRatio(returns, 0.0, 0.10)
	// With risk-free rate 0, all returns are above threshold, so downside dev = 0
	if result != 0 {
		t.Errorf("expected zero sortino with no downside, got %f", result)
	}
}

func TestFormatDate(t *testing.T) {
	d := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	if formatDate(d) != "2024-03-15" {
		t.Errorf("formatDate = %s, want 2024-03-15", formatDate(d))
	}
	if formatDate(time.Time{}) != "" {
		t.Errorf("formatDate(zero) should be empty")
	}
}

func TestComputeDrawdownSeries(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	values := []float64{100, 110, 100, 105}
	snapshots := makeSnapshots(start, values)

	series := computeDrawdownSeries(snapshots)
	if len(series) == 0 {
		t.Fatal("expected non-empty drawdown series")
	}

	// After peak of 110, value drops to 100 → drawdown = -(10/110)*100 ≈ -9.09%
	// Find the point at index 2 (day 3)
	found := false
	for _, pt := range series {
		if pt.Date == "2024-01-03" {
			found = true
			expected := -((110.0 - 100.0) / 110.0) * 100
			if math.Abs(pt.Drawdown-expected) > 0.1 {
				t.Errorf("drawdown at 2024-01-03 = %f, want ~%f", pt.Drawdown, expected)
			}
		}
	}
	if !found {
		t.Error("expected to find drawdown point at 2024-01-03")
	}
}
