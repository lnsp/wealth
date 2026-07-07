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

	vol := annualizedVolatility(returns)

	// Derive annualized return from mean daily returns.
	// Using last/first net worth would include deposits, grossly inflating Sharpe.
	meanDailyReturn := mean(returns)
	annualReturn := meanDailyReturn * float64(tradingDaysPerYear)

	sharpe := 0.0
	if vol > 0 {
		sharpe = (annualReturn - riskFreeRate) / vol
	}

	sortino := sortinoRatio(returns, riskFreeRate, annualReturn)
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
	curDD := 0.0
	if ath > 0 {
		curDD = ((ath - currentValue) / ath) * 100
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

// smoothOutliers replaces data quality outliers with the previous day's value.
// A point is an outlier if its value deviates more than ±50% from the previous
// non-outlier value (backfill artifacts like missing prices).
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
				result[i].Value = prev
				continue
			}
		}
		prev = snapshots[i].Value
	}
	return result
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

func annualizedVolatility(returns []float64) float64 {
	avg := mean(returns)
	daily := stddev(returns, avg)
	return daily * math.Sqrt(tradingDaysPerYear)
}

func sortinoRatio(returns []float64, riskFreeRate, annualReturn float64) float64 {
	dailyRfr := riskFreeRate / float64(tradingDaysPerYear)
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
	downsideDev := math.Sqrt(downsideSq/float64(len(returns))) * math.Sqrt(tradingDaysPerYear)
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

	result := make(map[string][]RollingMetric)
	windows := map[string]int{"30d": 30, "90d": 90, "365d": 252}

	for label, window := range windows {
		if len(returns) < window {
			continue
		}
		var series []RollingMetric
		step := window / 10 // sample every ~10% of window for chart readability
		if step < 1 {
			step = 1
		}

		dailyRfr := riskFreeRate / float64(tradingDaysPerYear)
		for i := window; i <= len(returns); i += step {
			slice := returns[i-window : i]
			avg := mean(slice)
			std := stddev(slice, avg)
			vol := std * math.Sqrt(float64(tradingDaysPerYear)) * 100 // annualized %

			annReturn := avg * float64(tradingDaysPerYear)
			sharpe := 0.0
			if std > 0 {
				sharpe = (annReturn - riskFreeRate) / (std * math.Sqrt(float64(tradingDaysPerYear)))
			}
			_ = dailyRfr

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
