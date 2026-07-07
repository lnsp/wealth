package handler

import (
	"math"
	"testing"
)

// switchTaxCost is the locked-down formula for "Product Switch" tax cost:
//
//	taxable_after_TFS = unrealized × (1 − teilfreistellung)
//	taxable_after_FSA = max(0, taxable_after_TFS − remaining_freibetrag)
//	tax               = taxable_after_FSA × 26.375 %
//
// These tests pin each multiplier so a future tweak (rate change, missing
// floor, etc.) surfaces in CI.

func TestSwitchTaxCost_EquityWithFullFSA(t *testing.T) {
	// 10_000 EUR gain on equity ETF, full 1000 EUR FSA available.
	// taxable_after_TFS = 10_000 × 0.70 = 7000
	// taxable_after_FSA = 7000 − 1000 = 6000
	// tax = 6000 × 0.26375 = 1582.50
	got := switchTaxCost(10000, 0.30, 1000)
	if math.Abs(got-1582.5) > 0.01 {
		t.Errorf("equity + full FSA: tax=%.4f, want 1582.50", got)
	}
}

func TestSwitchTaxCost_EquityWithDepletedFSA(t *testing.T) {
	// Same gain but the user already used the full FSA via dividends.
	// taxable_after_TFS = 7000
	// taxable_after_FSA = 7000 − 0 = 7000
	// tax = 7000 × 0.26375 = 1846.25
	got := switchTaxCost(10000, 0.30, 0)
	if math.Abs(got-1846.25) > 0.01 {
		t.Errorf("equity + 0 FSA: tax=%.4f, want 1846.25", got)
	}
}

func TestSwitchTaxCost_BondNoTeilfreistellung(t *testing.T) {
	// Bond fund: 0% Teilfreistellung, 500 EUR FSA remaining.
	// taxable_after_TFS = 10_000 × 1.0 = 10_000
	// taxable_after_FSA = 10_000 − 500 = 9500
	// tax = 9500 × 0.26375 = 2505.625
	got := switchTaxCost(10000, 0, 500)
	if math.Abs(got-2505.625) > 0.01 {
		t.Errorf("bond + partial FSA: tax=%.4f, want 2505.625", got)
	}
}

func TestSwitchTaxCost_FSACoversAllTaxable(t *testing.T) {
	// Small gain entirely covered by the remaining FSA.
	// gain 1000 × (1 − 0.30) = 700 taxable; FSA 1000 covers it; tax 0.
	got := switchTaxCost(1000, 0.30, 1000)
	if got != 0 {
		t.Errorf("FSA covers taxable: tax=%.4f, want 0", got)
	}
}

func TestSwitchTaxCost_NoGainNoTax(t *testing.T) {
	if got := switchTaxCost(0, 0.30, 1000); got != 0 {
		t.Errorf("zero gain: tax=%.4f, want 0", got)
	}
	if got := switchTaxCost(-500, 0.30, 1000); got != 0 {
		t.Errorf("loss: tax=%.4f, want 0 (no tax on a loss)", got)
	}
}

func TestSwitchTaxCost_AbgeltungssteuerRate(t *testing.T) {
	// Lock the 26.375% rate explicitly — 25% KapErtSt + 5.5% Soli.
	// gain 10_000, no TFS, no FSA → all taxable, tax = 10_000 × 0.26375
	got := switchTaxCost(10000, 0, 0)
	if math.Abs(got-2637.5) > 0.01 {
		t.Errorf("bare Abgeltungssteuer rate check: tax=%.4f, want 2637.5", got)
	}
}
