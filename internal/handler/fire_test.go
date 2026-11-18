package handler

import (
	"math"
	"testing"
	"time"

	"github.com/lnsp/wealth/internal/analytics"
)

// These tests replace the old fire_test.go, which locked in a local copy of
// the marginal-income-tax gross-up (expenses / (1 − 0.42 × 0.7)). That
// treated every euro withdrawn as taxable income (audit C1). They now call
// the real FIRE model used by HandleProjection.

var fireNow = time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)

func germanFire(cv, contrib, expenses float64) fireParams {
	return fireParams{
		CurrentValue:   cv,
		MonthlyContrib: contrib,
		AnnualReturn:   0.07,
		Inflation:      0.02,
		Expenses:       expenses,
		SWR:            0.035,
		TaxRate:        analytics.CapitalIncomeTaxRate(0),
		TFS:            analytics.TeilfreistellungEquity,
		Allowance:      1000,
		MaxMonths:      600,
	}
}

func TestFireTarget_NoGainsIsPlainSWR(t *testing.T) {
	// With nothing but cost basis there is no gain to tax: 36,000 / 3.5 %.
	p := germanFire(2_000_000, 0, 36000)
	target, bridge := p.targetAt(0, 1_000_000, 1_000_000)
	if math.Abs(target-36000/0.035) > 0.01 || bridge != 0 {
		t.Errorf("target = %.2f (bridge %.2f), want 1,028,571.43", target, bridge)
	}
}

// Audit C1: the tax gross-up uses 26.375 % on the gain share after the 30 %
// Teilfreistellung and the saver's allowance — not the 42 % income-tax rate on
// the whole withdrawal. The old model needed 1,456,900; the gain-based target
// for a portfolio that is 60 % gain is far closer to the plain SWR figure.
func TestFireTarget_TaxesOnlyTheGainShare(t *testing.T) {
	p := germanFire(0, 0, 36000)
	p.Inflation = 0
	target, _ := p.targetAt(0, 1_000_000, 400_000) // 60 % gain
	k := 0.6 * 0.7
	rate := analytics.CapitalIncomeTaxRate(0)
	want := (36000 - 1000*rate) / (1 - k*rate) / 0.035
	if math.Abs(target-want) > 0.01 {
		t.Errorf("target = %.2f, want %.2f", target, want)
	}
	old := 36000 / (1 - 0.42*0.7) / 0.035
	if target >= old || target <= 36000/0.035 {
		t.Errorf("target %.0f should lie between the untaxed %.0f and the old %.0f", target, 36000/0.035, old)
	}
}

// Audit C1: "Target portfolio" and "Independence date" must come from the
// same target, and the first year's withdrawal must be the one the target was
// sized for.
func TestSimulateFIRE_TargetDateAndWithdrawalAgree(t *testing.T) {
	p := germanFire(45073, 1557, 36000)
	res := simulateFIRE(p, fireNow)
	if !res.Reached {
		t.Fatal("FIRE not reached within 50 years")
	}
	m := res.Month
	before := planProjectFV(p.CurrentValue, p.MonthlyContrib, 7, 0, m-1)
	at := planProjectFV(p.CurrentValue, p.MonthlyContrib, 7, 0, m)
	basisAt := p.CurrentValue + p.MonthlyContrib*float64(m)
	basisBefore := basisAt - p.MonthlyContrib
	tBefore, _ := p.targetAt(m-1, before, basisBefore)
	tAt, _ := p.targetAt(m, at, basisAt)
	if !(before < tBefore && at >= tAt) {
		t.Errorf("month %d: value %.0f vs target %.0f, previous %.0f vs %.0f", m, at, tAt, before, tBefore)
	}
	if math.Abs(res.TargetNominal-tAt) > 0.01 {
		t.Errorf("reported target %.2f, recomputed %.2f", res.TargetNominal, tAt)
	}
	// The first year's gross withdrawal, in today's money, is the target × SWR.
	if math.Abs(res.FirstYearGross-res.Target*p.SWR) > 1 {
		t.Errorf("first-year gross %.0f, target × SWR %.0f", res.FirstYearGross, res.Target*p.SWR)
	}
	// The tax table's cost basis includes the contributions (audit C1: the old
	// ratio currentValue / fireValue ignored them).
	wantGain := 1 - basisAt/at
	if math.Abs(res.GainShare-wantGain) > 1e-9 {
		t.Errorf("gain share %.4f, want %.4f", res.GainShare, wantGain)
	}
	t.Logf("FIRE: month %d (%s), target %.0f today's €, nominal %.0f, gain share %.1f %%, first-year gross %.0f, tax %.0f",
		m, addMonthsFirstOfMonth(fireNow, m).Format("2006-01"), res.Target, res.TargetNominal, res.GainShare*100, res.FirstYearGross, res.FirstYearTax)
}

// Audit W1: withdrawals are indexed to inflation, so the net amount received is
// constant in today's money and a 7 %/3.5 % plan no longer balloons.
func TestSimulateFIRE_WithdrawalsAreInflationIndexed(t *testing.T) {
	p := germanFire(2_000_000, 0, 36000)
	res := simulateFIRE(p, fireNow)
	if res.Month != 0 || len(res.Years) < 40 {
		t.Fatalf("month %d, %d years", res.Month, len(res.Years))
	}
	for _, y := range res.Years[:40] {
		if math.Abs(y.NetReceived-36000) > 0.5 {
			t.Fatalf("year %d nets %.2f in today's money, want 36,000", y.Year, y.NetReceived)
		}
	}
	// Tax rises as the gain share grows.
	if res.Years[39].EstimatedTax <= res.Years[0].EstimatedTax {
		t.Errorf("tax year 40 %.0f should exceed year 1 %.0f", res.Years[39].EstimatedTax, res.Years[0].EstimatedTax)
	}
}

func TestSimulateFIRE_DepletesWhenUnderfunded(t *testing.T) {
	p := germanFire(500_000, 0, 30000)
	p.SWR = 0.06 // 500k ≥ 30k / 6 %, but 6 % does not last at a 4.9 % real return
	res := simulateFIRE(p, fireNow)
	if res.Month != 0 {
		t.Fatalf("month %d, want 0", res.Month)
	}
	if res.LongevityMonths >= 480 || res.LongevityMonths < 12*20 {
		t.Errorf("longevity %d months, want between 20 and 40 years", res.LongevityMonths)
	}
	last := res.Series[len(res.Series)-1]
	if last.Value != 0 {
		t.Errorf("series ends at %v, want 0", last.Value)
	}
}

// Audit W1: with a pension from 67 and independence at ~50, the portfolio
// must fund the full spending until the pension starts.
func TestSimulateFIRE_BridgeYearsBeforePension(t *testing.T) {
	p := germanFire(300_000, 2000, 36000)
	p.Pension = 12000
	p.CurrentAge = 40
	p.PensionAge = 67
	res := simulateFIRE(p, fireNow)
	if !res.Reached || res.BridgeYears <= 0 || res.Bridge <= 0 {
		t.Fatalf("reached %v, bridge %.1f years / %.0f €", res.Reached, res.BridgeYears, res.Bridge)
	}
	noBridge := p
	noBridge.CurrentAge = 0 // pension counted from the independence date (old behaviour)
	plain := simulateFIRE(noBridge, fireNow)
	if res.Target <= plain.Target || res.Month <= plain.Month {
		t.Errorf("bridge target %.0f / month %d should exceed %.0f / %d", res.Target, res.Month, plain.Target, plain.Month)
	}
	ageAtFI := 40 + float64(res.Month)/12
	for i, y := range res.Years {
		age := ageAtFI + float64(i)
		want := 24000.0
		if age < 67 {
			want = 36000
		}
		if math.Abs(y.NetReceived-want) > 0.5 {
			t.Fatalf("year %d (age %.1f) nets %.0f, want %.0f", y.Year, age, y.NetReceived, want)
		}
	}
}

func TestSimulateFIRE_NotReached(t *testing.T) {
	p := germanFire(1000, 0, 100000)
	p.AnnualReturn = 0
	res := simulateFIRE(p, fireNow)
	if res.Reached || res.Month != -1 || len(res.Years) != 0 {
		t.Errorf("reached %v month %d years %d, want not reached", res.Reached, res.Month, len(res.Years))
	}
	if res.Target <= 0 {
		t.Errorf("target %v should still be reported", res.Target)
	}
}
