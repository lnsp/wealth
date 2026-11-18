package handler

import (
	"math"
	"testing"

	"github.com/lnsp/wealth/internal/analytics"
)

func TestCapitalIncomeFromPots_CarryPlusThisYear(t *testing.T) {
	pots := []analytics.LossPotYear{
		{Year: 2024, CarryForwardEquity: -900, CarryForwardGeneral: -100},
		{Year: 2025, CarryForwardEquity: -500, CarryForwardGeneral: 0},
		{Year: 2026, EquityGains: 800, GeneralGains: 300, GeneralLosses: -50},
	}
	st := capitalIncomeFromPots(pots, 2026)
	if st.shareRaw != 300 || st.otherRaw != 250 {
		t.Errorf("state = %+v, want share 800−500 = 300, other 300−50 = 250", st)
	}
	if got := capitalIncomeFromPots(pots, 2027); got.shareRaw != 0 || got.otherRaw != 0 {
		t.Errorf("2027 state = %+v, want the 2026 carry-forward (none)", got)
	}
}

// Regression for Net Worth F11: a harvest saving was |loss| × (1 − TFS) ×
// 26.375 % even with nothing realised to offset (2,000 € fund loss → 369 €).
func TestHarvestSavings_CappedAtSamePotGainsThisYear(t *testing.T) {
	rate := analytics.CapitalIncomeTaxRate(0)
	fundLoss := harvestCandidate{ISIN: "FUND", Loss: 2000, TFS: 0.3}

	if got := harvestSavings([]harvestCandidate{fundLoss}, capitalIncomeState{}, 1000, rate)[0]; got != 0 {
		t.Errorf("no gains realised: saving = %.2f, want 0", got)
	}
	// Gains below the allowance are untaxed anyway.
	if got := harvestSavings([]harvestCandidate{fundLoss}, capitalIncomeState{otherRaw: 600}, 1000, rate)[0]; got != 0 {
		t.Errorf("income within the allowance: saving = %.2f, want 0", got)
	}
	// Share losses never offset dividends or fund gains.
	shareLoss := harvestCandidate{ISIN: "STOCK", Loss: 1000, Share: true}
	if got := harvestSavings([]harvestCandidate{shareLoss}, capitalIncomeState{otherRaw: 3000}, 1000, rate)[0]; got != 0 {
		t.Errorf("share loss vs dividends: saving = %.2f, want 0", got)
	}

	// Share gains 3,000 and dividends 500 realised: 2,500 € above the
	// allowance is all there is to save on, however large the losses.
	st := capitalIncomeState{shareRaw: 3000, otherRaw: 500}
	bigFundLoss := harvestCandidate{ISIN: "FUND", Loss: 4000, TFS: 0.3}
	got := harvestSavings([]harvestCandidate{shareLoss, bigFundLoss}, st, 1000, rate)
	if math.Abs(got[0]-1000*rate) > 1e-6 {
		t.Errorf("share loss saving = %.2f, want %.2f", got[0], 1000*rate)
	}
	if math.Abs(got[1]-1500*rate) > 1e-6 {
		t.Errorf("fund loss saving = %.2f, want the remaining %.2f (not 2,800 × rate)", got[1], 1500*rate)
	}
}

func TestAllowanceGainRoom(t *testing.T) {
	cases := []struct {
		st                capitalIncomeState
		share, other, out float64
	}{
		{capitalIncomeState{otherRaw: 200}, 0, 5000, 800},
		{capitalIncomeState{otherRaw: -300}, 0, 5000, 1000}, // first 300 € fill the carry-forward
		{capitalIncomeState{otherRaw: -300}, 0, 800, 500},
		{capitalIncomeState{otherRaw: 1200}, 4000, 0, 0},  // allowance already used
		{capitalIncomeState{shareRaw: -2000}, 1500, 0, 0}, // share gains only fill the share pot
	}
	for _, c := range cases {
		if got := allowanceGainRoom(c.st, c.share, c.other, 1000); math.Abs(got-c.out) > 1e-9 {
			t.Errorf("allowanceGainRoom(%+v, %v, %v) = %v, want %v", c.st, c.share, c.other, got, c.out)
		}
	}
}

// Regression for Net Worth F11: the Vorabpauschale was charged in full with
// no Sparer-Pauschbetrag (it is income of the next year, set against that
// year's allowance) and at 26.375 % regardless of church tax.
func TestVorabpauschaleTax_AfterTeilfreistellungAndNextYearsAllowance(t *testing.T) {
	entries := []analytics.VorabpauschaleEntry{
		{ISIN: "EQUITY", Vorabpauschale: 1500},
		{ISIN: "BOND", Vorabpauschale: 100},
	}
	tfs := map[string]float64{"EQUITY": 0.3}
	rate := analytics.CapitalIncomeTaxRate(0.09)
	taxable, tax := vorabpauschaleTax(entries, tfs, 1000, rate)
	if math.Abs(taxable-1150) > 1e-9 {
		t.Errorf("taxable = %.2f, want 1500 × 0.7 + 100 = 1,150", taxable)
	}
	if math.Abs(tax-150*rate) > 1e-9 {
		t.Errorf("tax = %.2f, want 150 € above the allowance × %.4f", tax, rate)
	}
	if _, tax := vorabpauschaleTax(entries, tfs, 2000, rate); tax != 0 {
		t.Errorf("joint allowance covers it: tax = %.2f, want 0", tax)
	}
}
