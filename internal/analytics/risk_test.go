package analytics

import (
	"math"
	"math/rand"
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

// Audit 2026-11-18 F1: a Morgan Stanley sell-all + wire (value → 0) followed
// weeks later by a new vest used to produce a fake −77 % day, because steps
// touching a 0 value were forced to factor 1 and the 0 was never carried
// forward. Seed example: 3,683 → 0 with a −3,632 wire, then 0 → 1,046 with a
// +974 vest.
func TestBuildCashflowAdjustedSeries_LiquidateThenReenter(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2023, 3, day, 0, 0, 0, 0, time.UTC) }
	snaps := []DailyValuation{{Date: d(7), Value: 3683}, {Date: d(8), Value: 0}}
	for day := 9; day <= 24; day++ {
		snaps = append(snaps, DailyValuation{Date: d(day), Value: 0})
	}
	snaps = append(snaps, DailyValuation{Date: d(25), Value: 1046}, DailyValuation{Date: d(26), Value: 1046})
	flows := []CashFlow{{Date: d(8), Amount: -3632}, {Date: d(25), Amount: 974}}

	adj := BuildCashflowAdjustedSeries(snaps, flows)
	if len(adj) != len(snaps) {
		t.Fatalf("len = %d, want %d", len(adj), len(snaps))
	}
	ret := func(i int) float64 { return adj[i].Value/adj[i-1].Value - 1 }

	// Sale day: (0 − 3,683 + 3,632) / 3,683 = −1.38 %.
	if got, want := ret(1), -51.0/3683; math.Abs(got-want) > 1e-9 {
		t.Errorf("sale-day return = %.4f, want %.4f", got, want)
	}
	// Flat while nothing is held.
	for i := 2; i < len(snaps)-2; i++ {
		if ret(i) != 0 {
			t.Errorf("day %d with nothing held: return %.4f, want 0", i, ret(i))
		}
	}
	// Re-entry: (1,046 − 0 − 974) / (0 + 974) = +7.39 %, not −77 %.
	if got, want := ret(len(snaps)-2), 72.0/974; math.Abs(got-want) > 1e-9 {
		t.Errorf("re-entry return = %.4f, want %.4f", got, want)
	}
	// The series ends at the latest value.
	if got := adj[len(adj)-1].Value; math.Abs(got-1046) > 1e-6 {
		t.Errorf("last adjusted value = %.2f, want 1046", got)
	}
	m := ComputeRiskMetrics(adj, 0.02, 1046)
	if m.MaxDrawdown > 2 {
		t.Errorf("max drawdown = %.2f %%, want the 1.38 %% sale-day dip only", m.MaxDrawdown)
	}
}

// After a full sale the index still ends on the last day something was held,
// rather than giving up and returning the raw series.
func TestBuildCashflowAdjustedSeries_EndsLiquidated(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	snaps := makeSnapshots(start, []float64{1000, 1010, 0, 0})
	flows := []CashFlow{{Date: start.AddDate(0, 0, 2), Amount: -1020}}
	adj := BuildCashflowAdjustedSeries(snaps, flows)
	if got := adj[2].Value/adj[1].Value - 1; math.Abs(got-10.0/1010) > 1e-9 {
		t.Errorf("sale-day return = %.5f, want %.5f", got, 10.0/1010)
	}
	if adj[1].Value != 1010 {
		t.Errorf("anchor value = %.2f, want 1010", adj[1].Value)
	}
}

// Audit F7: weekly closes were annualised with √252, showing a 15 % series
// as 31 %. Annualisation now follows the observed sampling frequency.
func TestComputeRiskMetrics_WeeklySeriesAnnualisedWeekly(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	d := time.Date(2021, 11, 19, 0, 0, 0, 0, time.UTC)
	p := 100.0
	var pts []DailyValuation
	for i := 0; i < 261; i++ {
		pts = append(pts, DailyValuation{Date: d, Value: p})
		p *= 1 + 0.08/52 + 0.15/math.Sqrt(52)*r.NormFloat64()
		d = d.AddDate(0, 0, 7)
	}
	if ppy := inferPeriodsPerYear(pts); math.Abs(ppy-52.18) > 0.1 {
		t.Errorf("periods per year = %.2f, want ≈52.18", ppy)
	}
	rets := dailyReturns(pts)
	want := stddev(rets, mean(rets)) * math.Sqrt(inferPeriodsPerYear(pts)) * 100
	m := ComputeRiskMetrics(pts, 0.02, p)
	if math.Abs(m.AnnualizedVolatility-want) > 0.01 {
		t.Errorf("volatility = %.2f %%, want %.2f %%", m.AnnualizedVolatility, want)
	}
	if m.AnnualizedVolatility < 12 || m.AnnualizedVolatility > 18 {
		t.Errorf("volatility = %.2f %%, want close to the true 15 %%", m.AnnualizedVolatility)
	}
}

func TestInferPeriodsPerYear(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	daily := makeSnapshots(start, make([]float64, 366))
	if got := inferPeriodsPerYear(daily); math.Abs(got-365.25) > 1.5 {
		t.Errorf("calendar-daily = %.2f, want ≈365", got)
	}
	var weekdays []DailyValuation
	for i := 0; i < 730; i++ {
		day := start.AddDate(0, 0, i)
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			weekdays = append(weekdays, DailyValuation{Date: day, Value: 1})
		}
	}
	if got := inferPeriodsPerYear(weekdays); got < 255 || got > 265 {
		t.Errorf("weekday series = %.2f, want ≈261", got)
	}
}

// Audit F12: the "1 day" VaR used calendar-day returns, so the zero-return
// weekends shrank it by ~17 %. It is now a one-trading-day figure.
func TestComputeRiskMetrics_VaRIsOneTradingDay(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	const sigma = 0.01 // per trading day
	d := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	p := 100000.0
	var pts []DailyValuation
	var tradingRets []float64
	for i := 0; i < 730; i++ {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			x := sigma * r.NormFloat64()
			tradingRets = append(tradingRets, x)
			p *= 1 + x
		}
		pts = append(pts, DailyValuation{Date: d, Value: p})
		d = d.AddDate(0, 0, 1)
	}
	m := ComputeRiskMetrics(pts, 0, 100000)
	want := -zScore95 * stddev(tradingRets, mean(tradingRets)) * 100000
	if math.Abs(m.ValueAtRisk95-want)/math.Abs(want) > 0.06 {
		t.Errorf("VaR = %.0f, want ≈ %.0f (one trading day)", m.ValueAtRisk95, want)
	}
}

// Audit F10: the current drawdown is measured on the return index; its peak
// date is reported, and AllTimeHigh is the high of the series passed in.
func TestComputeRiskMetrics_PeakAndCurrentDrawdown(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	m := ComputeRiskMetrics(makeSnapshots(start, []float64{100, 110, 120, 108, 114}), 0, 114)
	if m.PeakDate != "2024-01-03" {
		t.Errorf("peak date = %q, want 2024-01-03", m.PeakDate)
	}
	if math.Abs(m.CurrentDrawdown-5) > 0.01 {
		t.Errorf("current drawdown = %.2f, want 5", m.CurrentDrawdown)
	}
	if m.SeriesStart != "2024-01-01" {
		t.Errorf("series start = %q", m.SeriesStart)
	}
}

// Audit I4: downsampling must keep troughs and peaks, and each point carries
// its level so a period's own peak-to-trough drop can be measured.
func TestComputeDrawdownSeries_DownsamplingKeepsExtremes(t *testing.T) {
	start := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	vals := make([]float64, 1500)
	for i := range vals {
		vals[i] = 100 + float64(i%50)
	}
	vals[1001] = 60 // one-day trough between bucket boundaries
	series := computeDrawdownSeries(makeSnapshots(start, vals))
	if len(series) > 3*maxDrawdownPts {
		t.Fatalf("series has %d points, want at most %d", len(series), 3*maxDrawdownPts)
	}
	found := false
	for _, p := range series {
		if p.Date == start.AddDate(0, 0, 1001).Format("2006-01-02") {
			found = true
			if p.Value != 60 || math.Abs(p.Drawdown-(-((149.0-60)/149)*100)) > 0.01 {
				t.Errorf("trough point = %+v", p)
			}
		}
	}
	if !found {
		t.Error("downsampling dropped the trough")
	}
}

// Audit I3: the risk-free rate follows the ECB deposit facility rate.
func TestECBDepositFacilityRate(t *testing.T) {
	cases := map[string]float64{
		"2022-01-01": -0.005, "2023-10-01": 0.04, "2025-06-11": 0.02,
		"2026-06-16": 0.02, "2026-06-17": 0.0225, "2026-11-18": 0.025,
	}
	for date, want := range cases {
		d, _ := time.Parse("2006-01-02", date)
		if got := ECBDepositFacilityRate(d); math.Abs(got-want) > 1e-12 {
			t.Errorf("rate on %s = %.4f, want %.4f", date, got, want)
		}
	}
	// 2026-03-16 → 2026-09-16 is 93 days at 2.00 % and 91 days at 2.25 %.
	from := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	want := (93*0.02 + 91*0.0225) / 184
	if got := AverageRiskFreeRate(from, to); math.Abs(got-want) > 1e-9 {
		t.Errorf("average = %.6f, want %.6f", got, want)
	}
}

func TestRecentVolatility(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	vals := make([]float64, 100)
	for i := range vals {
		vals[i] = 100
		if i >= 70 && i%2 == 1 {
			vals[i] = 101
		}
	}
	pts := makeSnapshots(start, vals)
	if v := RecentVolatility(pts[:70], 30); v != 0 {
		t.Errorf("flat window volatility = %.4f, want 0", v)
	}
	if v := RecentVolatility(pts, 30); v <= 0 {
		t.Errorf("choppy window volatility = %.4f, want > 0", v)
	}
}
