package handler

import (
	"math"
	"testing"
)

// fireNumber mirrors the formula in HandleProjection:
//
//	grossExpenses = annualExpenses / (1 − marginalRate × taxPortion)
//	fireNumber    = grossExpenses / (swr / 100)
//
// annualExpenses is post-pension (the gap the portfolio must fund) per the
// frontend's gap-calc. Inflating by the effective tax on each gross euro
// withdrawn produces a more conservative FIRE target than the plain SWR-only
// formula. Locked here so the planner contract doesn't quietly drift.
func fireNumber(annualExpenses, swr, marginalRate, taxPortion float64) float64 {
	effTax := marginalRate * taxPortion
	if effTax >= 0.99 {
		effTax = 0.99
	}
	gross := annualExpenses / (1 - effTax)
	return gross / (swr / 100)
}

func TestFireNumber_SWROnly_NoTax(t *testing.T) {
	// SWR 4% (the classic Trinity rule), no tax: 25× expenses.
	got := fireNumber(40000, 4, 0, 0)
	if math.Abs(got-1_000_000) > 0.5 {
		t.Errorf("FIRE(40k, 4%%, 0 tax) = %.2f, want 1_000_000", got)
	}
}

func TestFireNumber_GermanEquityETFDefaults(t *testing.T) {
	// Approximates the backend default: Abgeltungssteuer-only equity ETF
	// withdrawal — marginal 26.375%, tax portion 0.7 (Teilfreistellung 30%
	// applied implicitly to the withdrawn slice). Effective ≈ 0.18 → FIRE
	// inflates by ~22% over the SWR-only target.
	annual := 36000.0
	swr := 3.5
	want := annual / (1 - 0.26375*0.7) / (swr / 100)
	got := fireNumber(annual, swr, 0.26375, 0.7)
	if math.Abs(got-want) > 0.5 {
		t.Errorf("FIRE(36k, 3.5%%, 0.26375×0.7) = %.2f, want %.2f", got, want)
	}
	// Sanity: this should exceed the SWR-only FIRE (36000/0.035 ≈ 1.029M).
	swrOnly := annual / (swr / 100)
	if got <= swrOnly {
		t.Errorf("tax-aware FIRE (%.2f) should exceed SWR-only (%.2f)", got, swrOnly)
	}
}

func TestFireNumber_HighMarginalBracket(t *testing.T) {
	// 42% marginal × 0.7 tax portion → effective 0.294 on gross → FIRE
	// inflates by ~42%. Useful for users with high other income or those
	// modelling pension drawdown (no Abgeltungssteuer cap there).
	annual := 50000.0
	swr := 3.5
	got := fireNumber(annual, swr, 0.42, 0.7)
	// Sanity: must significantly exceed the post-pension-only SWR target.
	swrOnly := annual / (swr / 100)
	if got/swrOnly < 1.35 || got/swrOnly > 1.5 {
		t.Errorf("FIRE inflation factor = %.2f, want roughly 1.42×", got/swrOnly)
	}
}

func TestFireNumber_PensionGapZero(t *testing.T) {
	// If pension fully covers expenses, the frontend passes zero as the FIRE
	// gap and the formula returns 0 (handler short-circuits on annualExpenses
	// > 0 in production; we just lock the math here).
	got := fireNumber(0, 3.5, 0.42, 0.7)
	if got != 0 {
		t.Errorf("FIRE(0 gap) = %.2f, want 0", got)
	}
}

func TestFireNumber_ClampsEffectiveTax(t *testing.T) {
	// Pathological input where marginal × taxPortion ≥ 1 would otherwise
	// divide by zero. Clamp to 0.99 so the planner stays finite (gross
	// becomes 100× expenses — clearly an unreachable scenario, by design).
	got := fireNumber(10000, 3.5, 1.0, 1.0)
	want := (10000.0 / 0.01) / 0.035
	if math.Abs(got-want) > 1.0 {
		t.Errorf("FIRE with clamped tax = %.2f, want %.2f", got, want)
	}
}
