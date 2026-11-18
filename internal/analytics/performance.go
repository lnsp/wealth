package analytics

import (
	"math"
	"time"
)

// CashFlow represents a cash flow event for IRR/TWR calculations.
type CashFlow struct {
	Date   time.Time
	Amount float64
}

// DailyValuation represents portfolio value on a given date.
type DailyValuation struct {
	Date  time.Time
	Value float64
}

// TWRResult is a time-weighted return together with the window it actually
// covers. OK is false when no sub-period had capital at risk, so callers can
// show "not enough history" instead of a fabricated 0 %.
type TWRResult struct {
	Return float64   // cumulative return (fraction) from Start to End
	OK     bool      // false: no sub-period with capital at risk
	Start  time.Time // first date the chained return measures from
	End    time.Time
	// Resets lists sub-periods that lost 100 % or more without a recorded
	// outflow (the value fell to zero and the money left untracked). The
	// chain cannot compound through such a period, so it restarts after it
	// and Start moves past the reset.
	Resets []time.Time
}

// CalculateTWR computes the Time-Weighted Return using Modified Dietz sub-period
// weighting (with start-of-day weighting on daily/weekend snapshot steps where
// deposits execute intraday prior to evening snapshot valuation). It returns 0
// when there is not enough history; use ComputeTWR to tell that apart from a
// genuine 0 % return.
func CalculateTWR(valuations []DailyValuation, cashflows []CashFlow) float64 {
	r := ComputeTWR(valuations, cashflows)
	if !r.OK {
		return 0
	}
	return r.Return
}

// ComputeTWR is CalculateTWR with the measured window and validity reported.
func ComputeTWR(valuations []DailyValuation, cashflows []CashFlow) TWRResult {
	if len(valuations) < 2 {
		return TWRResult{}
	}

	periods := splitByCashflows(valuations, cashflows)
	res := TWRResult{End: valuations[len(valuations)-1].Date}
	twr := 1.0
	for _, p := range periods {
		if p.StartValue <= 0 {
			continue
		}
		gain := p.EndValue - p.StartValue - p.NetFlow
		denom := p.StartValue + p.WeightedFlow
		var r float64
		if denom > 0 && denom+gain > 0 {
			r = gain / denom
		} else if p.StartValue+p.PositiveFlow > 0 {
			r = gain / (p.StartValue + p.PositiveFlow)
		} else {
			continue
		}
		if 1+r <= 0 {
			// Total loss inside one sub-period: the chain cannot compound
			// through it, so restart after it instead of skipping silently.
			twr = 1.0
			res.OK = false
			res.Start = time.Time{}
			res.Resets = append(res.Resets, p.endDate)
			continue
		}
		if !res.OK {
			res.Start = p.startDate
			res.OK = true
		}
		twr *= (1 + r)
	}
	if res.OK {
		res.Return = twr - 1.0
	}
	return res
}

// TWRStep is one point of a dense (typically daily) valuation series. Flow is
// the net external flow (+ in, − out) that arrived after the previous step and
// is already reflected in Value.
type TWRStep struct {
	Date  time.Time
	Value float64
	Flow  float64
}

// TWRChain is a chained time-weighted return over a dense series.
type TWRChain struct {
	// Index is the growth factor at each step relative to the first step with
	// capital at risk (1 = start). It is NaN before capital exists. A step that
	// triggers a reset carries the previous factor so a chart line stays
	// continuous; TWRResult.Resets says where that happened.
	Index []float64
	TWRResult
}

// ChainTWR links daily sub-period returns
//
//	r_i = (V_i − V_{i−1} − F_i) / (V_{i−1} + max(F_i, 0))
//
// where inflows are assumed to arrive at the start of the day (so they earn
// that day's return) and outflows at its end. Periods without capital at risk
// (V_{i−1} + inflow ≤ 0, e.g. between a sell-everything-and-withdraw and the
// next RSU vest) are skipped instead of producing a fake −100 % or +∞ step.
// With this denominator 1 + r = (V_i + outflows) / (V_{i−1} + inflows), so a
// sub-period at −100 % only happens when the value vanished without a recorded
// outflow; the chain restarts after it (see TWRResult.Resets).
func ChainTWR(steps []TWRStep) TWRChain {
	out := TWRChain{Index: make([]float64, len(steps))}
	if len(steps) == 0 {
		return out
	}
	out.End = steps[len(steps)-1].Date
	idx := math.NaN()
	out.Index[0] = idx
	kpi := 1.0
	for i := 1; i < len(steps); i++ {
		prev := steps[i-1].Value
		f := steps[i].Flow
		denom := prev + math.Max(f, 0)
		if denom <= 1e-9 {
			out.Index[i] = idx
			continue
		}
		r := (steps[i].Value - prev - f) / denom
		if 1+r <= 1e-12 {
			out.Resets = append(out.Resets, steps[i].Date)
			out.OK = false
			out.Start = time.Time{}
			kpi = 1.0
			out.Index[i] = idx
			continue
		}
		if math.IsNaN(idx) {
			idx = 1.0
			out.Index[i-1] = 1.0
		}
		if !out.OK {
			out.OK = true
			out.Start = steps[i-1].Date
		}
		idx *= 1 + r
		kpi *= 1 + r
		out.Index[i] = idx
	}
	if out.OK {
		out.Return = kpi - 1
	}
	return out
}

type period struct {
	StartValue   float64
	EndValue     float64
	NetFlow      float64
	WeightedFlow float64
	PositiveFlow float64
	startDate    time.Time
	endDate      time.Time
}

func splitByCashflows(valuations []DailyValuation, cashflows []CashFlow) []period {
	if len(valuations) < 2 {
		return nil
	}

	normDate := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}

	var periods []period
	startVal := valuations[0].Value
	startDate := normDate(valuations[0].Date)
	prevDate := startDate

	for i := 1; i < len(valuations); i++ {
		curDate := normDate(valuations[i].Date)
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

		if netFlow != 0 || i == len(valuations)-1 {
			periods = append(periods, period{
				StartValue:   startVal,
				EndValue:     valuations[i].Value,
				NetFlow:      netFlow,
				WeightedFlow: weightedFlow,
				PositiveFlow: posFlow,
				startDate:    startDate,
				endDate:      curDate,
			})
			startVal = valuations[i].Value
			startDate = curDate
		}
		prevDate = curDate
	}

	return periods
}

// ModifiedDietz returns the money-weighted return over [start, end]:
//
//	(EndValue − StartValue − ΣF) / (StartValue + Σ w_i·F_i),  w_i = (end − t_i)/(end − start)
//
// with flows signed + in, − out. It is the gain divided by the average capital
// that was invested over the window, so unlike gain ÷ net capital it does not
// explode after large withdrawals. ok is false when the average capital is not
// positive.
func ModifiedDietz(startValue, endValue float64, flows []CashFlow, start, end time.Time) (ret float64, ok bool) {
	span := end.Sub(start).Hours()
	if span <= 0 {
		return 0, false
	}
	net, weighted := 0.0, 0.0
	for _, f := range flows {
		if f.Date.Before(start) || f.Date.After(end) {
			continue
		}
		net += f.Amount
		weighted += f.Amount * end.Sub(f.Date).Hours() / span
	}
	avgCapital := startValue + weighted
	if avgCapital <= 1e-9 {
		return 0, false
	}
	return (endValue - startValue - net) / avgCapital, true
}

// CalculateIRR computes the Internal Rate of Return using Newton-Raphson
// with bounds clamping to prevent divergence. It returns 0 when no rate can be
// found; use ComputeIRR to tell that apart from a genuine 0 % return.
func CalculateIRR(cashflows []CashFlow, guess float64) float64 {
	rate, _ := ComputeIRR(cashflows, guess)
	return rate
}

// ComputeIRR returns the annualised internal rate of return and whether one
// exists. ok is false with fewer than two flows, when all flows have the same
// sign (no rate solves NPV = 0), or when no root lies in (−99.99 %, +10,000 %).
func ComputeIRR(cashflows []CashFlow, guess float64) (rate float64, ok bool) {
	if len(cashflows) < 2 {
		return 0, false
	}
	hasPos, hasNeg := false, false
	t0 := cashflows[0].Date
	scale := 0.0
	for _, cf := range cashflows {
		if cf.Date.Before(t0) {
			t0 = cf.Date
		}
		if cf.Amount > 0 {
			hasPos = true
		} else if cf.Amount < 0 {
			hasNeg = true
		}
		scale += math.Abs(cf.Amount)
	}
	if !hasPos || !hasNeg {
		return 0, false
	}

	years := func(t time.Time) float64 { return t.Sub(t0).Hours() / (365.25 * 24) }
	npvAt := func(r float64) float64 {
		npv := 0.0
		for _, cf := range cashflows {
			npv += cf.Amount / math.Pow(1+r, years(cf.Date))
		}
		return npv
	}
	tol := math.Max(1e-6, scale*1e-9)

	// Newton-Raphson with clamping
	rate = guess
	for i := 0; i < 200; i++ {
		npv, dnpv := 0.0, 0.0
		for _, cf := range cashflows {
			t := years(cf.Date)
			npv += cf.Amount / math.Pow(1+rate, t)
			dnpv += -t * cf.Amount / math.Pow(1+rate, t+1)
		}
		if math.Abs(npv) < tol {
			break
		}
		if dnpv == 0 || math.IsNaN(dnpv) {
			break
		}
		step := npv / dnpv
		// Dampen large steps to prevent divergence
		if math.Abs(step) > 0.5 {
			step = 0.5 * step / math.Abs(step)
		}
		rate -= step
		// Clamp rate to reasonable bounds [-0.99, 5.0] (max 500% annual return)
		if rate < -0.99 {
			rate = -0.99
		}
		if rate > 5.0 {
			rate = 5.0
		}
	}
	if v := npvAt(rate); !math.IsNaN(v) && math.Abs(v) <= math.Max(1.0, tol) {
		return rate, true
	}

	// Fall back to bisection over a wide bracket. Scan for a sign change first
	// so short or extreme histories (e.g. +300 % within a few months) resolve.
	grid := []float64{-0.9999, -0.99, -0.9, -0.75, -0.5, -0.25, 0, 0.25, 0.5, 1, 2, 5, 10, 25, 50, 100}
	for i := 1; i < len(grid); i++ {
		lo, hi := grid[i-1], grid[i]
		flo, fhi := npvAt(lo), npvAt(hi)
		if math.IsNaN(flo) || math.IsNaN(fhi) || math.IsInf(flo, 0) || math.IsInf(fhi, 0) {
			continue
		}
		if flo == 0 {
			return lo, true
		}
		if (flo > 0) == (fhi > 0) {
			continue
		}
		for j := 0; j < 200 && hi-lo > 1e-10; j++ {
			mid := (lo + hi) / 2
			if fm := npvAt(mid); (fm > 0) == (flo > 0) {
				lo, flo = mid, fm
			} else {
				hi = mid
			}
		}
		return (lo + hi) / 2, true
	}
	return 0, false
}
