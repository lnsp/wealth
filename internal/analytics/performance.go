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

// CalculateIRR computes the Internal Rate of Return using Newton-Raphson.
func CalculateIRR(cashflows []CashFlow, guess float64) float64 {
	if len(cashflows) < 2 {
		return 0
	}

	rate := guess
	for i := 0; i < 100; i++ {
		npv, dnpv := 0.0, 0.0
		for _, cf := range cashflows {
			t := cf.Date.Sub(cashflows[0].Date).Hours() / (365.25 * 24)
			npv += cf.Amount / math.Pow(1+rate, t)
			dnpv += -t * cf.Amount / math.Pow(1+rate, t+1)
		}
		if math.Abs(npv) < 1e-8 {
			break
		}
		if dnpv == 0 {
			break
		}
		rate -= npv / dnpv
	}
	return rate
}
