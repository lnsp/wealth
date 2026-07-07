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

// CalculateTWR computes the Time-Weighted Return.
func CalculateTWR(valuations []DailyValuation, cashflows []CashFlow) float64 {
	if len(valuations) < 2 {
		return 0
	}

	periods := splitByCashflows(valuations, cashflows)
	twr := 1.0
	for _, p := range periods {
		if p.StartValue == 0 {
			continue
		}
		r := (p.EndValue - p.StartValue - p.NetFlow) / p.StartValue
		twr *= (1 + r)
	}
	return twr - 1.0
}

type period struct {
	StartValue float64
	EndValue   float64
	NetFlow    float64
}

func splitByCashflows(valuations []DailyValuation, cashflows []CashFlow) []period {
	if len(valuations) < 2 {
		return nil
	}

	// Build a map of cashflows by date
	cfByDate := make(map[string]float64)
	for _, cf := range cashflows {
		cfByDate[cf.Date.Format("2006-01-02")] += cf.Amount
	}

	var periods []period
	startVal := valuations[0].Value

	for i := 1; i < len(valuations); i++ {
		dateKey := valuations[i].Date.Format("2006-01-02")
		netFlow := cfByDate[dateKey]

		if netFlow != 0 || i == len(valuations)-1 {
			periods = append(periods, period{
				StartValue: startVal,
				EndValue:   valuations[i].Value,
				NetFlow:    netFlow,
			})
			startVal = valuations[i].Value
		}
	}

	return periods
}

// CalculateIRR computes the Internal Rate of Return using Newton-Raphson
// with bounds clamping to prevent divergence.
func CalculateIRR(cashflows []CashFlow, guess float64) float64 {
	if len(cashflows) < 2 {
		return 0
	}

	npvAt := func(rate float64) float64 {
		npv := 0.0
		for _, cf := range cashflows {
			t := cf.Date.Sub(cashflows[0].Date).Hours() / (365.25 * 24)
			npv += cf.Amount / math.Pow(1+rate, t)
		}
		return npv
	}

	// Newton-Raphson with clamping
	rate := guess
	for i := 0; i < 200; i++ {
		npv, dnpv := 0.0, 0.0
		for _, cf := range cashflows {
			t := cf.Date.Sub(cashflows[0].Date).Hours() / (365.25 * 24)
			npv += cf.Amount / math.Pow(1+rate, t)
			dnpv += -t * cf.Amount / math.Pow(1+rate, t+1)
		}
		if math.Abs(npv) < 1e-6 {
			break
		}
		if dnpv == 0 {
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

	// Validate: if NPV at found rate is still large, fall back to bisection
	if math.Abs(npvAt(rate)) > 1.0 {
		lo, hi := -0.5, 2.0
		for i := 0; i < 100; i++ {
			mid := (lo + hi) / 2
			if npvAt(mid) > 0 {
				lo = mid
			} else {
				hi = mid
			}
			if hi-lo < 1e-6 {
				break
			}
		}
		rate = (lo + hi) / 2
	}

	return rate
}
