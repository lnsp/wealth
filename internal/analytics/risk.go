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
	maxDrawdownPts     = 365 // downsample drawdown series to this many points
)

// RiskMetrics contains computed portfolio risk statistics.
type RiskMetrics struct {
	AnnualizedVolatility float64         `json:"annualized_volatility"`
	SharpeRatio          float64         `json:"sharpe_ratio"`
	SortinoRatio         float64         `json:"sortino_ratio"`
	MaxDrawdown          float64         `json:"max_drawdown"`
	MaxDrawdownStart     string          `json:"max_drawdown_start"`
	MaxDrawdownEnd       string          `json:"max_drawdown_end"`
	MaxDrawdownDays      int             `json:"max_drawdown_days"`
	ValueAtRisk95        float64         `json:"value_at_risk_95"`
	CurrentDrawdown      float64         `json:"current_drawdown"`
	AllTimeHigh          float64         `json:"all_time_high"`
	ATHDate              string          `json:"ath_date"`
	DrawdownSeries       []DrawdownPoint `json:"drawdown_series"`
}

// DrawdownPoint represents a single point in the drawdown time series.
type DrawdownPoint struct {
	Date     string  `json:"date"`
	Drawdown float64 `json:"drawdown"`
}

// ComputeRiskMetrics calculates risk statistics from daily portfolio values.
// riskFreeRate is the annualized risk-free rate (e.g., 0.03 for 3%).
// currentValue is the latest portfolio value for VaR computation.
func ComputeRiskMetrics(snapshots []DailyValuation, riskFreeRate float64, currentValue float64) *RiskMetrics {
	if len(snapshots) < 2 {
		return &RiskMetrics{}
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
		return &RiskMetrics{}
	}

	smoothed := smoothOutliers(snapshots)
	returns := dailyReturns(smoothed)
	if len(returns) == 0 {
		return &RiskMetrics{}
	}

	periodsPerYear := inferPeriodsPerYear(smoothed)
	vol := annualizedVolatilityWithPeriods(returns, periodsPerYear)

	// Derive annualized return from mean daily returns.
	// Using last/first net worth would include deposits, grossly inflating Sharpe.
	meanDailyReturn := mean(returns)
	annualReturn := meanDailyReturn * periodsPerYear

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

	meanDaily := mean(returns)
	stdDaily := stddev(returns, meanDaily)
	var95 := currentValue * (meanDaily - zScore95*stdDaily)

	// ATH and current drawdown from smoothed data
	ath := 0.0
	athDate := time.Time{}
	for _, s := range smoothed {
		if s.Value > ath {
			ath = s.Value
			athDate = s.Date
		}
	}
	refCurrent := currentValue
	if refCurrent <= 0 && len(smoothed) > 0 {
		refCurrent = smoothed[len(smoothed)-1].Value
	}
	if refCurrent > ath {
		ath = refCurrent
		if len(smoothed) > 0 {
			athDate = smoothed[len(smoothed)-1].Date
		}
	}
	curDD := 0.0
	if ath > 0 {
		curDD = ((ath - refCurrent) / ath) * 100
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
		AllTimeHigh:          math.Round(ath*100) / 100,
		ATHDate:              formatDate(athDate),
		DrawdownSeries:       drawdownSeries,
	}
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

// BuildCashflowAdjustedSeries converts a raw net-worth valuation series plus
// external cashflows (+deposit, -withdrawal) into a cashflow-neutral TWR index
// series scaled to end at the latest portfolio value, preventing deposits and
// withdrawals from distorting volatility, Sharpe, Sortino, and MaxDrawdown.
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
					if cf.Amount > 0 {
						w = 1.0
					}
				} else if spanDays > 0 {
					w = curDate.Sub(cfDate).Hours() / (spanDays * 24.0)
				}
				weightedFlow += cf.Amount * w
			}
		}
		factor := 1.0
		if prevVal > 0 && curVal > 0 {
			gain := curVal - prevVal - netFlow
			denom := prevVal + weightedFlow
			if denom > 0 && denom+gain > 0 {
				factor = 1.0 + gain/denom
			} else if prevVal+posFlow > 0 {
				factor = 1.0 + gain/(prevVal+posFlow)
			}
			if factor <= 0 || math.IsNaN(factor) || math.IsInf(factor, 0) {
				factor = 1.0
			}
		}
		idx[i] = idx[i-1] * factor
		prevDate = curDate
		if curVal > 0 {
			prevVal = curVal
		}
	}

	endIdx := idx[len(snapshots)-1]
	endVal := snapshots[len(snapshots)-1].Value
	if endIdx <= 0 || endVal <= 0 {
		return snapshots
	}
	scale := endVal / endIdx
	out := make([]DailyValuation, 0, len(snapshots)-firstPos)
	for i := firstPos; i < len(snapshots); i++ {
		out = append(out, DailyValuation{
			Date:  snapshots[i].Date,
			Value: idx[i] * scale,
		})
	}
	return out
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

func inferPeriodsPerYear(snapshots []DailyValuation) float64 {
	if len(snapshots) >= 10 {
		spanDays := snapshots[len(snapshots)-1].Date.Sub(snapshots[0].Date).Hours() / 24.0
		if spanDays >= 14 {
			obsPerYear := float64(len(snapshots)-1) / (spanDays / 365.25)
			if obsPerYear > 300 {
				return 365.25
			}
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

func computeDrawdownSeries(snapshots []DailyValuation) []DrawdownPoint {
	if len(snapshots) < 2 {
		return nil
	}

	step := 1
	if len(snapshots) > maxDrawdownPts {
		step = len(snapshots) / maxDrawdownPts
	}

	peak := snapshots[0].Value
	var series []DrawdownPoint
	for i, s := range snapshots {
		if s.Value > peak {
			peak = s.Value
		}
		if i%step != 0 && i != len(snapshots)-1 {
			continue
		}
		dd := 0.0
		if peak > 0 {
			dd = -((peak - s.Value) / peak) * 100
		}
		series = append(series, DrawdownPoint{
			Date:     s.Date.Format("2006-01-02"),
			Drawdown: math.Round(dd*100) / 100,
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

	// Skip initial ramp-up period (same as ComputeRiskMetrics) to avoid
	// distortion from early low-value snapshots that break outlier smoothing.
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
	if len(snapshots) < 60 {
		return nil
	}

	smoothed := smoothOutliers(snapshots)
	returns := dailyReturns(smoothed)
	if len(returns) < 60 {
		return nil
	}

	periodsPerYear := inferPeriodsPerYear(smoothed)
	yearWindow := int(math.Round(periodsPerYear))
	if yearWindow < 252 {
		yearWindow = 252
	}
	result := make(map[string][]RollingMetric)
	windows := map[string]int{"30d": 30, "90d": 90, "365d": yearWindow}

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
