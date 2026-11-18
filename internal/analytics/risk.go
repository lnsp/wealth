package analytics

import (
	"math"
	"time"
)

const (
	tradingDaysPerYear = 252
	outlierThreshold   = 0.50 // ±50% day-over-day change = data quality artifact
	rampUpThreshold    = 0.10 // skip initial period below 10% of peak
	zScore95           = 1.645
	maxDrawdownPts     = 365 // downsample the drawdown series into this many buckets
)

// RiskMetrics contains computed portfolio risk statistics.
type RiskMetrics struct {
	AnnualizedVolatility float64 `json:"annualized_volatility"`
	SharpeRatio          float64 `json:"sharpe_ratio"`
	SortinoRatio         float64 `json:"sortino_ratio"`
	MaxDrawdown          float64 `json:"max_drawdown"`
	MaxDrawdownStart     string  `json:"max_drawdown_start"`
	MaxDrawdownEnd       string  `json:"max_drawdown_end"`
	MaxDrawdownDays      int     `json:"max_drawdown_days"`
	// ValueAtRisk95 is the parametric 95 % value at risk over one trading
	// day for currentValue, in currency units (negative = loss).
	ValueAtRisk95 float64 `json:"value_at_risk_95"`
	// CurrentDrawdown is how far (in %) the last point of the series is below
	// the series' highest point, which was reached on PeakDate.
	CurrentDrawdown float64 `json:"current_drawdown"`
	PeakDate        string  `json:"peak_date"`
	// AllTimeHigh and ATHDate are the highest value of the input series. For
	// a flow-adjusted return index that is not a value the portfolio ever
	// had, so callers passing such a series overwrite both with the highest
	// recorded value.
	AllTimeHigh float64 `json:"all_time_high"`
	ATHDate     string  `json:"ath_date"`
	// SeriesStart is the first date the metrics use, after the ramp-up skip.
	SeriesStart    string          `json:"series_start"`
	DrawdownSeries []DrawdownPoint `json:"drawdown_series"`
}

// DrawdownPoint represents a single point in the drawdown time series.
// Value is the series level on that date, so a client can measure the
// peak-to-trough drop inside a sub-period instead of against the all-time
// peak.
type DrawdownPoint struct {
	Date     string  `json:"date"`
	Drawdown float64 `json:"drawdown"`
	Value    float64 `json:"value"`
}

// ECB deposit facility rate, the euro risk-free rate used for Sharpe and
// Sortino. Each entry is the rate in force from that date ("with effect
// from" in the ECB press releases). Sources: ECB monetary policy decisions,
// ecb.europa.eu/press/pr; latest checked 2026-11-18: the 29 Oct 2026
// decision kept 2.50 % (in force since 16 Sep 2026, decision of 10 Sep
// 2026; 2.25 % from 17 Jun 2026, decision of 11 Jun 2026).
var ecbDepositFacilityRates = []struct {
	from time.Time
	rate float64
}{
	{time.Date(2019, 9, 18, 0, 0, 0, 0, time.UTC), -0.0050},
	{time.Date(2022, 7, 27, 0, 0, 0, 0, time.UTC), 0.0000},
	{time.Date(2022, 9, 14, 0, 0, 0, 0, time.UTC), 0.0075},
	{time.Date(2022, 11, 2, 0, 0, 0, 0, time.UTC), 0.0150},
	{time.Date(2022, 12, 21, 0, 0, 0, 0, time.UTC), 0.0200},
	{time.Date(2023, 2, 8, 0, 0, 0, 0, time.UTC), 0.0250},
	{time.Date(2023, 3, 22, 0, 0, 0, 0, time.UTC), 0.0300},
	{time.Date(2023, 5, 10, 0, 0, 0, 0, time.UTC), 0.0325},
	{time.Date(2023, 6, 21, 0, 0, 0, 0, time.UTC), 0.0350},
	{time.Date(2023, 8, 2, 0, 0, 0, 0, time.UTC), 0.0375},
	{time.Date(2023, 9, 20, 0, 0, 0, 0, time.UTC), 0.0400},
	{time.Date(2024, 6, 12, 0, 0, 0, 0, time.UTC), 0.0375},
	{time.Date(2024, 9, 18, 0, 0, 0, 0, time.UTC), 0.0350},
	{time.Date(2024, 10, 23, 0, 0, 0, 0, time.UTC), 0.0325},
	{time.Date(2024, 12, 18, 0, 0, 0, 0, time.UTC), 0.0300},
	{time.Date(2025, 2, 5, 0, 0, 0, 0, time.UTC), 0.0275},
	{time.Date(2025, 3, 12, 0, 0, 0, 0, time.UTC), 0.0250},
	{time.Date(2025, 4, 23, 0, 0, 0, 0, time.UTC), 0.0225},
	{time.Date(2025, 6, 11, 0, 0, 0, 0, time.UTC), 0.0200},
	{time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC), 0.0225},
	{time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC), 0.0250},
}

// ECBDepositFacilityRate returns the ECB deposit facility rate (as a
// fraction) in force on date d.
func ECBDepositFacilityRate(d time.Time) float64 {
	rate := ecbDepositFacilityRates[0].rate
	for _, step := range ecbDepositFacilityRates {
		if d.Before(step.from) {
			break
		}
		rate = step.rate
	}
	return rate
}

// AverageRiskFreeRate returns the time-weighted average ECB deposit facility
// rate between from and to, so a Sharpe ratio over a multi-year window is
// measured against the rates that were actually available over that window.
func AverageRiskFreeRate(from, to time.Time) float64 {
	if !to.After(from) {
		return ECBDepositFacilityRate(to)
	}
	total := 0.0
	cur := from
	for cur.Before(to) {
		next := to
		for _, step := range ecbDepositFacilityRates {
			if step.from.After(cur) {
				if step.from.Before(next) {
					next = step.from
				}
				break
			}
		}
		total += ECBDepositFacilityRate(cur) * next.Sub(cur).Hours()
		cur = next
	}
	return total / to.Sub(from).Hours()
}

// PrepareRiskSeries applies the same preparation ComputeRiskMetrics uses:
// it skips the initial ramp-up (points below 10 % of the series peak) and
// smooths transient data-quality outliers. Callers that compute their own
// statistics (e.g. the volatility callout) use it so their figures match the
// KPI tiles.
func PrepareRiskSeries(snapshots []DailyValuation) []DailyValuation {
	if len(snapshots) < 2 {
		return snapshots
	}
	// Skip initial ramp-up: start from the first point where the portfolio
	// reaches at least 10% of its peak value, avoiding distortion from
	// the early period when only a few small transactions exist.
	peak := 0.0
	for _, s := range snapshots {
		if s.Value > peak {
			peak = s.Value
		}
	}
	threshold := peak * rampUpThreshold
	start := 0
	for start < len(snapshots) && snapshots[start].Value < threshold {
		start++
	}
	snapshots = snapshots[start:]
	if len(snapshots) < 2 {
		return snapshots
	}
	return smoothOutliers(snapshots)
}

// ComputeRiskMetrics calculates risk statistics from daily portfolio values.
// riskFreeRate is the annualized risk-free rate (e.g., 0.02 for 2%).
// currentValue is the latest portfolio value for VaR computation.
func ComputeRiskMetrics(snapshots []DailyValuation, riskFreeRate float64, currentValue float64) *RiskMetrics {
	if len(snapshots) < 2 {
		return &RiskMetrics{}
	}

	smoothed := PrepareRiskSeries(snapshots)
	if len(smoothed) < 2 {
		return &RiskMetrics{}
	}
	returns := dailyReturns(smoothed)
	if len(returns) == 0 {
		return &RiskMetrics{}
	}

	periodsPerYear := inferPeriodsPerYear(smoothed)
	vol := annualizedVolatilityWithPeriods(returns, periodsPerYear)

	// Derive annualized return from mean daily returns.
	// Using last/first net worth would include deposits, grossly inflating Sharpe.
	annualReturn := mean(returns) * periodsPerYear

	sharpe := 0.0
	if vol > 0 {
		sharpe = (annualReturn - riskFreeRate) / vol
	}

	sortino := sortinoRatioWithPeriods(returns, riskFreeRate, annualReturn, periodsPerYear)
	ddStart, ddEnd, maxDD := maxDrawdown(smoothed)

	ddDays := 0
	if !ddStart.IsZero() && !ddEnd.IsZero() {
		ddDays = int(ddEnd.Sub(ddStart).Hours() / 24)
	}

	var95 := oneTradingDayVaR(currentValue, annualReturn, vol)

	// Peak and current drawdown of the series itself. For a flow-adjusted
	// index this is "% below the best point of the return index", which is
	// independent of deposits and withdrawals.
	peak := 0.0
	peakDate := time.Time{}
	for _, s := range smoothed {
		if s.Value > peak {
			peak = s.Value
			peakDate = s.Date
		}
	}
	last := smoothed[len(smoothed)-1].Value
	curDD := 0.0
	if peak > 0 {
		curDD = ((peak - last) / peak) * 100
		if curDD < 0 {
			curDD = 0
		}
	}

	drawdownSeries := computeDrawdownSeries(smoothed)

	return &RiskMetrics{
		AnnualizedVolatility: math.Round(vol*10000) / 100,
		SharpeRatio:          math.Round(sharpe*100) / 100,
		SortinoRatio:         math.Round(sortino*100) / 100,
		MaxDrawdown:          math.Round(maxDD*10000) / 100,
		MaxDrawdownStart:     formatDate(ddStart),
		MaxDrawdownEnd:       formatDate(ddEnd),
		MaxDrawdownDays:      ddDays,
		ValueAtRisk95:        math.Round(var95*100) / 100,
		CurrentDrawdown:      math.Round(curDD*100) / 100,
		PeakDate:             formatDate(peakDate),
		AllTimeHigh:          math.Round(peak*100) / 100,
		ATHDate:              formatDate(peakDate),
		SeriesStart:          formatDate(smoothed[0].Date),
		DrawdownSeries:       drawdownSeries,
	}
}

// oneTradingDayVaR is the parametric 95 % VaR over one trading day. The
// annual mean and volatility are scaled down by 252 trading days, so the
// figure does not depend on how often the series was sampled: on a
// calendar-day series the weekend "returns" are zero, and using the raw
// per-observation spread would understate a trading day's risk by ~17 %.
func oneTradingDayVaR(currentValue, annualReturn, annualVol float64) float64 {
	mu := annualReturn / tradingDaysPerYear
	sigma := annualVol / math.Sqrt(tradingDaysPerYear)
	return currentValue * (mu - zScore95*sigma)
}

// RecentVolatility returns the annualised volatility (as a fraction) of the
// returns over the last `days` calendar days of a prepared series, using the
// series' own sampling frequency for annualisation.
func RecentVolatility(series []DailyValuation, days int) float64 {
	if len(series) < 3 || days <= 0 {
		return 0
	}
	periodsPerYear := inferPeriodsPerYear(series)
	cutoff := series[len(series)-1].Date.AddDate(0, 0, -days)
	var returns []float64
	for i := 1; i < len(series); i++ {
		if !series[i].Date.After(cutoff) {
			continue
		}
		if series[i-1].Value > 0 {
			returns = append(returns, (series[i].Value-series[i-1].Value)/series[i-1].Value)
		}
	}
	if len(returns) < 2 {
		return 0
	}
	return annualizedVolatilityWithPeriods(returns, periodsPerYear)
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// smoothOutliers replaces transient data-quality outliers (1–3 day backfill spikes
// or missing-price drops that revert back near `prev`) with `prev`. Legitimate
// permanent step-changes (e.g. deposits, RSU vests, major withdrawals where the
// new level persists) are preserved and update `prev` so subsequent history is
// never flatlined.
func smoothOutliers(snapshots []DailyValuation) []DailyValuation {
	if len(snapshots) < 2 {
		return snapshots
	}
	result := make([]DailyValuation, len(snapshots))
	result[0] = snapshots[0]
	prev := snapshots[0].Value
	for i := 1; i < len(snapshots); i++ {
		result[i] = snapshots[i]
		if prev > 0 {
			change := (snapshots[i].Value - prev) / prev
			if change > outlierThreshold || change < -outlierThreshold {
				reverts := snapshots[i].Value <= 0
				for j := i + 1; j < len(snapshots) && j <= i+3; j++ {
					laterChange := (snapshots[j].Value - prev) / prev
					if laterChange <= outlierThreshold && laterChange >= -outlierThreshold {
						reverts = true
						break
					}
				}
				if reverts {
					result[i].Value = prev
					continue
				}
			}
		}
		prev = snapshots[i].Value
	}
	return result
}

// BuildCashflowAdjustedSeries converts a raw valuation series plus external
// cashflows (+deposit, -withdrawal) into a cashflow-neutral TWR index series
// scaled to end at the latest positive value, preventing deposits and
// withdrawals from distorting volatility, Sharpe, Sortino, and MaxDrawdown.
//
// Each step's return is (end − start − flows) / (start + weighted flows),
// computed even when the start or end value is 0: selling everything and
// wiring the cash out is a real (small) return on the start value, and
// re-entering from 0 is a return on the new money. The previous value is
// always carried forward, including 0, so a later re-entry is measured
// against what was actually there.
func BuildCashflowAdjustedSeries(snapshots []DailyValuation, cashflows []CashFlow) []DailyValuation {
	if len(snapshots) < 2 || len(cashflows) == 0 {
		return snapshots
	}
	normDate := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
	// Find first positive valuation
	firstPos := -1
	for i, s := range snapshots {
		if s.Value > 0 {
			firstPos = i
			break
		}
	}
	if firstPos < 0 || firstPos >= len(snapshots)-1 {
		return snapshots
	}

	idx := make([]float64, len(snapshots))
	idx[firstPos] = 1.0
	prevDate := normDate(snapshots[firstPos].Date)
	prevVal := snapshots[firstPos].Value

	for i := firstPos + 1; i < len(snapshots); i++ {
		curDate := normDate(snapshots[i].Date)
		curVal := snapshots[i].Value
		spanDays := curDate.Sub(prevDate).Hours() / 24.0
		var netFlow, weightedFlow, posFlow float64
		for _, cf := range cashflows {
			cfDate := normDate(cf.Date)
			if cfDate.After(prevDate) && !cfDate.After(curDate) {
				netFlow += cf.Amount
				if cf.Amount > 0 {
					posFlow += cf.Amount
				}
				w := 0.0
				if spanDays <= 4 {
					// Daily snapshots: money in counts from the start of the
					// step, money out from its end.
					if cf.Amount > 0 {
						w = 1.0
					}
				} else if spanDays > 0 {
					w = curDate.Sub(cfDate).Hours() / (spanDays * 24.0)
				}
				weightedFlow += cf.Amount * w
			}
		}
		idx[i] = idx[i-1] * flowAdjustedFactor(prevVal, curVal, netFlow, weightedFlow, posFlow)
		prevDate = curDate
		prevVal = curVal
	}

	// Scale so the index ends at the latest positive value. If everything was
	// sold, anchor on the last day something was held; metrics are ratios and
	// do not depend on the scale.
	anchor := -1
	for i := len(snapshots) - 1; i >= firstPos; i-- {
		if snapshots[i].Value > 0 && idx[i] > 0 {
			anchor = i
			break
		}
	}
	if anchor < 0 {
		return snapshots
	}
	scale := snapshots[anchor].Value / idx[anchor]
	out := make([]DailyValuation, 0, len(snapshots)-firstPos)
	for i := firstPos; i < len(snapshots); i++ {
		out = append(out, DailyValuation{
			Date:  snapshots[i].Date,
			Value: idx[i] * scale,
		})
	}
	return out
}

// flowAdjustedFactor returns the growth factor of one step of a flow-adjusted
// index: 1 + gain / capital, where gain excludes the external flows and
// capital is the start value plus the time-weighted flows. When the weighted
// capital is not positive (e.g. a withdrawal weighted against a tiny start
// value) it falls back to the start value plus all inflows. With no capital
// at all nothing was invested over the step, so there is no return (1).
func flowAdjustedFactor(prevVal, curVal, netFlow, weightedFlow, posFlow float64) float64 {
	gain := curVal - prevVal - netFlow
	denom := prevVal + weightedFlow
	if denom <= 0 || denom+gain <= 0 {
		denom = prevVal + posFlow
	}
	if denom <= 0 {
		return 1.0
	}
	factor := 1.0 + gain/denom
	if factor <= 0 || math.IsNaN(factor) || math.IsInf(factor, 0) {
		return 1.0
	}
	return factor
}

// dailyReturns computes simple daily returns from a sorted slice of valuations.
func dailyReturns(snapshots []DailyValuation) []float64 {
	var returns []float64
	for i := 1; i < len(snapshots); i++ {
		if snapshots[i-1].Value > 0 {
			r := (snapshots[i].Value - snapshots[i-1].Value) / snapshots[i-1].Value
			returns = append(returns, r)
		}
	}
	return returns
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func stddev(values []float64, avg float64) float64 {
	if len(values) < 2 {
		return 0
	}
	sumSq := 0.0
	for _, v := range values {
		d := v - avg
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(values)-1))
}

// inferPeriodsPerYear returns how many observations per year the series
// actually has, so variance is annualised per elapsed time whatever the
// sampling: ≈365 for calendar-day snapshots, ≈252-261 for trading-day
// prices, ≈52 for weekly closes. Gaps lower the count, which is right: the
// return across a gap carries the variance of every day it spans. Only a
// series too short to tell falls back to 252.
func inferPeriodsPerYear(snapshots []DailyValuation) float64 {
	if len(snapshots) >= 3 {
		spanDays := snapshots[len(snapshots)-1].Date.Sub(snapshots[0].Date).Hours() / 24.0
		if spanDays > 0 {
			return float64(len(snapshots)-1) / (spanDays / 365.25)
		}
	}
	return float64(tradingDaysPerYear)
}

func annualizedVolatility(returns []float64) float64 {
	return annualizedVolatilityWithPeriods(returns, float64(tradingDaysPerYear))
}

func annualizedVolatilityWithPeriods(returns []float64, periodsPerYear float64) float64 {
	avg := mean(returns)
	daily := stddev(returns, avg)
	return daily * math.Sqrt(periodsPerYear)
}

func sortinoRatio(returns []float64, riskFreeRate, annualReturn float64) float64 {
	return sortinoRatioWithPeriods(returns, riskFreeRate, annualReturn, float64(tradingDaysPerYear))
}

func sortinoRatioWithPeriods(returns []float64, riskFreeRate, annualReturn, periodsPerYear float64) float64 {
	dailyRfr := riskFreeRate / periodsPerYear
	var downsideSq float64
	n := 0
	for _, r := range returns {
		if r < dailyRfr {
			d := r - dailyRfr
			downsideSq += d * d
			n++
		}
	}
	if n == 0 {
		return 0
	}
	downsideDev := math.Sqrt(downsideSq/float64(len(returns))) * math.Sqrt(periodsPerYear)
	if downsideDev <= 0 {
		return 0
	}
	return (annualReturn - riskFreeRate) / downsideDev
}

func maxDrawdown(snapshots []DailyValuation) (start, end time.Time, dd float64) {
	if len(snapshots) < 2 {
		return
	}

	peak := snapshots[0].Value
	peakDate := snapshots[0].Date
	maxDD := 0.0
	var ddStartDate, ddEndDate time.Time

	for _, s := range snapshots[1:] {
		if s.Value > peak {
			peak = s.Value
			peakDate = s.Date
		}
		if peak > 0 {
			drawdown := (peak - s.Value) / peak
			if drawdown > maxDD {
				maxDD = drawdown
				ddStartDate = peakDate
				ddEndDate = s.Date
			}
		}
	}

	return ddStartDate, ddEndDate, maxDD
}

// computeDrawdownSeries returns "% below the running peak" per point. Long
// series are cut into maxDrawdownPts buckets and each bucket keeps its
// lowest value, highest value and deepest drawdown (in date order), so
// downsampling never hides a trough or a peak: a peak-to-trough drop
// measured on the kept points equals the one on the full series.
func computeDrawdownSeries(snapshots []DailyValuation) []DrawdownPoint {
	n := len(snapshots)
	if n < 2 {
		return nil
	}

	dd := make([]float64, n)
	peak := snapshots[0].Value
	for i, s := range snapshots {
		if s.Value > peak {
			peak = s.Value
		}
		if peak > 0 {
			dd[i] = -((peak - s.Value) / peak) * 100
		}
	}

	keep := make([]bool, n)
	if n <= maxDrawdownPts {
		for i := range keep {
			keep[i] = true
		}
	} else {
		step := int(math.Ceil(float64(n) / float64(maxDrawdownPts)))
		for b := 0; b < n; b += step {
			end := b + step
			if end > n {
				end = n
			}
			lo, hi, deep := b, b, b
			for i := b; i < end; i++ {
				if snapshots[i].Value < snapshots[lo].Value {
					lo = i
				}
				if snapshots[i].Value > snapshots[hi].Value {
					hi = i
				}
				if dd[i] < dd[deep] {
					deep = i
				}
			}
			keep[lo], keep[hi], keep[deep] = true, true, true
		}
		keep[0], keep[n-1] = true, true
	}

	series := make([]DrawdownPoint, 0, maxDrawdownPts*2)
	for i, s := range snapshots {
		if !keep[i] {
			continue
		}
		series = append(series, DrawdownPoint{
			Date:     s.Date.Format("2006-01-02"),
			Drawdown: math.Round(dd[i]*100) / 100,
			Value:    math.Round(s.Value*100) / 100,
		})
	}
	return series
}

// RollingMetric represents a single point in a rolling metrics time series.
type RollingMetric struct {
	Date       string  `json:"date"`
	Volatility float64 `json:"volatility"`
	Sharpe     float64 `json:"sharpe"`
}

// ComputeRollingMetrics calculates rolling volatility and Sharpe ratio for
// multiple windows (30, 90, 365 days). Returns a map of window -> time series.
func ComputeRollingMetrics(snapshots []DailyValuation, riskFreeRate float64) map[string][]RollingMetric {
	if len(snapshots) < 60 {
		return nil
	}

	// Same ramp-up skip and outlier smoothing as ComputeRiskMetrics.
	smoothed := PrepareRiskSeries(snapshots)
	if len(smoothed) < 60 {
		return nil
	}
	returns := dailyReturns(smoothed)
	if len(returns) < 60 {
		return nil
	}

	periodsPerYear := inferPeriodsPerYear(smoothed)
	// Windows are calendar spans converted to observations at the series'
	// own sampling frequency.
	obs := func(days int) int {
		n := int(math.Round(float64(days) * periodsPerYear / 365.25))
		if n < 2 {
			n = 2
		}
		return n
	}
	result := make(map[string][]RollingMetric)
	windows := map[string]int{"30d": obs(30), "90d": obs(90), "365d": obs(365)}

	for label, window := range windows {
		if len(returns) < window {
			continue
		}
		var series []RollingMetric
		step := window / 10 // sample every ~10% of window for chart readability
		if step < 1 {
			step = 1
		}

		for i := window; i <= len(returns); i += step {
			slice := returns[i-window : i]
			avg := mean(slice)
			std := stddev(slice, avg)
			vol := std * math.Sqrt(periodsPerYear) * 100 // annualized %

			annReturn := avg * periodsPerYear
			sharpe := 0.0
			if std > 0 {
				sharpe = (annReturn - riskFreeRate) / (std * math.Sqrt(periodsPerYear))
			}

			// Map return index back to date
			dateIdx := i // returns[i] corresponds to snapshots[i+1]
			if dateIdx >= len(smoothed) {
				dateIdx = len(smoothed) - 1
			}

			series = append(series, RollingMetric{
				Date:       smoothed[dateIdx].Date.Format("2006-01-02"),
				Volatility: math.Round(vol*10) / 10,
				Sharpe:     math.Round(sharpe*100) / 100,
			})
		}
		if len(series) > 0 {
			result[label] = series
		}
	}

	return result
}
