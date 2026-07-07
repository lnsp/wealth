package analytics

import (
	"math"
	"testing"
	"time"
)

// Regression contract: TWR (time-weighted) vs IRR (money-weighted, internal
// rate of return) diverge whenever the portfolio has mid-period contributions.
//
//   - TWR neutralizes cash-flow timing. It chains sub-period returns and is
//     the manager's-skill metric — what a passive index would have done.
//   - IRR is timing-sensitive. It's the investor's-experience metric —
//     reflects how much capital was deployed during gains vs losses.
//
// Two portfolios with identical sub-period returns and identical end values
// have the SAME TWR, but their IRRs differ if the deposit timing differs.
// Conversely, two portfolios with identical cash flows but different
// sub-period returns have different TWRs but may have similar IRRs.
//
// Without these regression tests, a refactor that conflates the two metrics
// (e.g., showing TWR as "your return" when the user expected IRR, or
// substituting one formula for the other in a KPI tile) would silently
// mislead users about whether their capital allocation was lucky vs whether
// their picks were good.
//
// Sign conventions:
//   - TWR cashflows: +amount = deposit (inflow into account)
//   - IRR cashflows: -amount = deposit (outflow from investor's pocket),
//                    +amount = withdrawal or terminal liquidation value

func d(year int, month, day int) time.Time {
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// twrAndIRR computes both metrics for a given scenario. Cashflows are stated
// from the investor's perspective (deposit = negative); the helper flips
// signs for TWR which uses the opposite convention.
func twrAndIRR(t *testing.T, valuations []DailyValuation, investorFlows []CashFlow) (twr, irr float64) {
	t.Helper()
	twrFlows := make([]CashFlow, 0, len(investorFlows))
	for _, cf := range investorFlows {
		if cf.Amount >= 0 {
			continue // withdrawals / terminal liquidations don't show up in TWR's cash-in NetFlow
		}
		twrFlows = append(twrFlows, CashFlow{Date: cf.Date, Amount: -cf.Amount})
	}
	twr = CalculateTWR(valuations, twrFlows)
	// IRR cashflows: investor-perspective deposits (negative) plus a terminal
	// liquidation cashflow equal to the final portfolio value.
	irrFlows := make([]CashFlow, 0, len(investorFlows)+1)
	for _, cf := range investorFlows {
		if cf.Amount < 0 {
			irrFlows = append(irrFlows, cf)
		}
	}
	irrFlows = append(irrFlows, CashFlow{
		Date:   valuations[len(valuations)-1].Date,
		Amount: valuations[len(valuations)-1].Value,
	})
	irr = CalculateIRR(irrFlows, 0.05)
	return
}

func TestTWRIRR_NoMidPeriodFlow_Converge(t *testing.T) {
	// Control: with no mid-period cash flows, TWR == IRR (within numerical
	// noise). A single deposit at t=0 and a single liquidation at t=1
	// reduces both formulas to (end/start)^(1/years) − 1.
	valuations := []DailyValuation{
		{Date: d(2025, 1, 1), Value: 1000},
		{Date: d(2026, 1, 1), Value: 1100},
	}
	investorFlows := []CashFlow{
		{Date: d(2025, 1, 1), Amount: -1000},
	}
	twr, irr := twrAndIRR(t, valuations, investorFlows)
	if math.Abs(twr-0.10) > 0.005 {
		t.Errorf("TWR control = %.4f, want ~0.10", twr)
	}
	if math.Abs(irr-0.10) > 0.005 {
		t.Errorf("IRR control = %.4f, want ~0.10", irr)
	}
	if math.Abs(twr-irr) > 0.005 {
		t.Errorf("TWR (%.4f) and IRR (%.4f) must converge with no mid-period flows", twr, irr)
	}
}

func TestTWRIRR_MidPeriodContributionDiverges(t *testing.T) {
	// Core regression: mid-period contribution causes TWR and IRR to differ.
	//   - Sub-period 1 (Jan→Jul): flat (1000 → 1000)
	//   - $1000 deposited Jul 1 → value 2000 immediately after
	//   - Sub-period 2 (Jul→Jan): +50% (2000 → 3000)
	// TWR = 1.0 × 1.5 − 1 = 0.50 (50%)
	// IRR ≈ 0.70 (~70%) — investor deployed more capital before the gain.
	valuations := []DailyValuation{
		{Date: d(2025, 1, 1), Value: 1000},
		{Date: d(2025, 7, 1), Value: 2000}, // value AFTER the Jul deposit
		{Date: d(2026, 1, 1), Value: 3000},
	}
	investorFlows := []CashFlow{
		{Date: d(2025, 1, 1), Amount: -1000},
		{Date: d(2025, 7, 1), Amount: -1000},
	}
	twr, irr := twrAndIRR(t, valuations, investorFlows)
	if math.Abs(twr-0.50) > 0.01 {
		t.Errorf("TWR = %.4f, want ~0.50 (flat → 50%% gain)", twr)
	}
	if math.Abs(irr-0.70) > 0.05 {
		t.Errorf("IRR = %.4f, want ~0.70 (capital deployed before gain)", irr)
	}
	if math.Abs(twr-irr) < 0.10 {
		t.Errorf("TWR=%.4f IRR=%.4f must differ by ≥10pp under mid-period contribution (regression spec)", twr, irr)
	}
	if irr <= twr {
		t.Errorf("good-timing case: IRR (%.4f) must exceed TWR (%.4f) when capital is deployed before gains", irr, twr)
	}
}

func TestTWRIRR_BadTimingMakesIRRWorseThanTWR(t *testing.T) {
	// Investor's-perspective downside: TWR positive, IRR NEGATIVE because
	// the bulk of capital was deployed right before a loss.
	//   - Sub-period 1: +50% gain (1000 → 1500)
	//   - $5000 deposited Jul 1 → value 6500
	//   - Sub-period 2: −20% loss (6500 → 5200)
	// TWR = 1.5 × 0.8 − 1 = 0.20 (+20%)
	// IRR ≈ −0.21 (investor lost $800 on $6000 deployed)
	valuations := []DailyValuation{
		{Date: d(2025, 1, 1), Value: 1000},
		{Date: d(2025, 7, 1), Value: 6500},
		{Date: d(2026, 1, 1), Value: 5200},
	}
	investorFlows := []CashFlow{
		{Date: d(2025, 1, 1), Amount: -1000},
		{Date: d(2025, 7, 1), Amount: -5000},
	}
	twr, irr := twrAndIRR(t, valuations, investorFlows)
	if twr <= 0 {
		t.Errorf("manager TWR should remain positive (+20%%-ish), got %.4f", twr)
	}
	if irr >= 0 {
		t.Errorf("investor IRR should be NEGATIVE despite positive TWR (bad timing), got %.4f", irr)
	}
	if math.Abs(twr-0.20) > 0.02 {
		t.Errorf("TWR = %.4f, want ~+0.20", twr)
	}
	// IRR landing point depends on Newton-Raphson tolerance; widen the band.
	if irr < -0.35 || irr > -0.10 {
		t.Errorf("IRR = %.4f, want roughly −0.21 (range [−0.35, −0.10])", irr)
	}
}

func TestTWRIRR_GoodTimingMakesIRRBetterThanTWR(t *testing.T) {
	// Mirror of the previous test: TWR NEGATIVE, IRR POSITIVE because the
	// bulk of capital was deployed right after a loss (and before the
	// rebound).
	//   - Sub-period 1: −50% loss (1000 → 500)
	//   - $5000 deposited Jul 1 → value 5500
	//   - Sub-period 2: +20% gain (5500 → 6600)
	// TWR = 0.5 × 1.2 − 1 = −0.40 (−40%)
	// IRR ≈ +0.18 (investor netted $600 on $6000 deployed)
	valuations := []DailyValuation{
		{Date: d(2025, 1, 1), Value: 1000},
		{Date: d(2025, 7, 1), Value: 5500},
		{Date: d(2026, 1, 1), Value: 6600},
	}
	investorFlows := []CashFlow{
		{Date: d(2025, 1, 1), Amount: -1000},
		{Date: d(2025, 7, 1), Amount: -5000},
	}
	twr, irr := twrAndIRR(t, valuations, investorFlows)
	if twr >= 0 {
		t.Errorf("manager TWR should be NEGATIVE (~−40%%), got %.4f", twr)
	}
	if irr <= 0 {
		t.Errorf("investor IRR should be POSITIVE despite negative TWR (good timing), got %.4f", irr)
	}
	if math.Abs(twr-(-0.40)) > 0.02 {
		t.Errorf("TWR = %.4f, want ~−0.40", twr)
	}
	if irr < 0.05 || irr > 0.30 {
		t.Errorf("IRR = %.4f, want roughly +0.18 (range [+0.05, +0.30])", irr)
	}
}

func TestTWRIRR_SameTWRDifferentIRR_EarlyVsLateDeposit(t *testing.T) {
	// Two portfolios with the SAME sub-period returns (hence same TWR) but
	// the second cash flow at very different times — IRRs diverge sharply.
	//   - Both: $1000 at t=0, $1000 mid-deposit at some date, end value
	//     $2250 at t=1. Intermediate value before deposit = $1100 (sub-period 1: +10%)
	//     and post-deposit second sub-period: 2250/2100 − 1 ≈ 7.14%.
	//     TWR ≈ 1.10 × 1.0714 − 1 ≈ 0.1786 for both.
	//   - Portfolio EARLY: deposit on day ~37 (~10% through year). Money
	//     deployed early → IRR LOWER (capital worked longer per unit of
	//     return generated).
	//   - Portfolio LATE: deposit on day ~329 (~90% through year). Money
	//     deployed late → IRR HIGHER.
	early := struct {
		valuations    []DailyValuation
		investorFlows []CashFlow
	}{
		valuations: []DailyValuation{
			{Date: d(2025, 1, 1), Value: 1000},
			{Date: d(2025, 2, 7), Value: 2100}, // ~day 37: value 1100 before deposit, +1000 deposit
			{Date: d(2026, 1, 1), Value: 2250},
		},
		investorFlows: []CashFlow{
			{Date: d(2025, 1, 1), Amount: -1000},
			{Date: d(2025, 2, 7), Amount: -1000},
		},
	}
	late := struct {
		valuations    []DailyValuation
		investorFlows []CashFlow
	}{
		valuations: []DailyValuation{
			{Date: d(2025, 1, 1), Value: 1000},
			{Date: d(2025, 11, 25), Value: 2100}, // ~day 329
			{Date: d(2026, 1, 1), Value: 2250},
		},
		investorFlows: []CashFlow{
			{Date: d(2025, 1, 1), Amount: -1000},
			{Date: d(2025, 11, 25), Amount: -1000},
		},
	}
	twrE, irrE := twrAndIRR(t, early.valuations, early.investorFlows)
	twrL, irrL := twrAndIRR(t, late.valuations, late.investorFlows)

	// Same intermediate values → same sub-period returns → same TWR.
	if math.Abs(twrE-twrL) > 0.005 {
		t.Errorf("TWRs should match across deposit timings: early=%.4f late=%.4f (TWR is timing-neutral)", twrE, twrL)
	}
	if math.Abs(twrE-0.1786) > 0.01 {
		t.Errorf("TWR = %.4f, want ~0.1786 (1.10 × 1.0714 − 1)", twrE)
	}
	// IRRs should differ: late deposit has higher IRR (less time for the
	// money to earn the same total).
	if irrL <= irrE {
		t.Errorf("late-deposit IRR (%.4f) must exceed early-deposit IRR (%.4f) — same TWR, different money-weighted return", irrL, irrE)
	}
	if math.Abs(irrL-irrE) < 0.05 {
		t.Errorf("IRR spread (early=%.4f late=%.4f) too small to demonstrate timing-blindness of TWR", irrE, irrL)
	}
}
