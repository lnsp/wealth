package analytics

import (
	"math"
	"testing"
)

// SellTaxRate / SimulateSell contract:
//
//	net_proceeds = gross_sell_amount − tax
//	tax          = gain × (1 − Teilfreistellung) × Abgeltungssteuer
//	                × (1 + Solidaritaetszuschlag + ChurchTaxRate)
//
// Lock the three multipliers + the rate composition so refactors can't
// silently drop a surcharge or apply Teilfreistellung to a non-equity sell.

func TestSellTaxRate_NoChurch(t *testing.T) {
	got := SellTaxRate(0)
	want := 0.26375 // 25% × 1.055
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("SellTaxRate(0) = %.6f, want %.6f", got, want)
	}
}

func TestSellTaxRate_8PercentChurch(t *testing.T) {
	// 25% × (1 + 0.055 + 0.08) = 25% × 1.135 = 0.28375
	got := SellTaxRate(0.08)
	want := 0.28375
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("SellTaxRate(0.08) = %.6f, want %.6f", got, want)
	}
}

func TestSellTaxRate_9PercentChurch(t *testing.T) {
	// 25% × (1 + 0.055 + 0.09) = 25% × 1.145 = 0.28625
	got := SellTaxRate(0.09)
	want := 0.28625
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("SellTaxRate(0.09) = %.6f, want %.6f", got, want)
	}
}

func TestSellTaxRate_NegativeClamped(t *testing.T) {
	if got := SellTaxRate(-0.5); math.Abs(got-0.26375) > 1e-6 {
		t.Errorf("SellTaxRate(-0.5) should clamp to 0 → 0.26375, got %.6f", got)
	}
}

func TestSellTaxRate_AboveMaxClamped(t *testing.T) {
	// Anything above 0.09 clamps to 0.09 (max German Kirchensteuer rate).
	if got := SellTaxRate(0.5); math.Abs(got-0.28625) > 1e-6 {
		t.Errorf("SellTaxRate(0.5) should clamp to 0.09 → 0.28625, got %.6f", got)
	}
}

// SimulateSell end-to-end formula coverage:
// gain 5000 EUR on equity ETF, full sell value 15000 EUR, no church.
// taxable_after_TFS = 5000 × (1 − 0.30) = 3500
// tax = 3500 × 0.26375 = 923.125
// net_proceeds = 15000 − 923.125 = 14076.875
func TestSimulateSell_EquityNoChurch(t *testing.T) {
	lots := []TaxLot{
		{ISIN: "EQ1", Name: "Equity ETF", Quantity: 100, CostBasis: 10000, CurrentValue: 15000, IsEquityFund: true},
	}
	requests := []SellRequest{{ISIN: "EQ1", AmountEUR: 15000}}
	results, totalTax, totalProceeds := SimulateSell(lots, requests, 0)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if math.Abs(r.RealizedGain-5000) > 0.01 {
		t.Errorf("realized_gain = %.2f, want 5000", r.RealizedGain)
	}
	if math.Abs(r.Teilfreistellung-1500) > 0.01 {
		t.Errorf("teilfreistellung = %.2f, want 1500 (gain × 0.30)", r.Teilfreistellung)
	}
	if math.Abs(r.TaxableGain-3500) > 0.01 {
		t.Errorf("taxable_gain = %.2f, want 3500", r.TaxableGain)
	}
	if math.Abs(r.EstimatedTax-923.125) > 0.5 {
		t.Errorf("estimated_tax = %.2f, want 923.125 (3500 × 0.26375)", r.EstimatedTax)
	}
	if math.Abs(r.NetProceeds-14076.875) > 0.5 {
		t.Errorf("net_proceeds = %.2f, want 14076.875 (15000 − 923.125)", r.NetProceeds)
	}
	if math.Abs(totalTax-923.125) > 0.5 {
		t.Errorf("totalTax = %.2f, want 923.125", totalTax)
	}
	if math.Abs(totalProceeds-14076.875) > 0.5 {
		t.Errorf("totalProceeds = %.2f, want 14076.875", totalProceeds)
	}
}

func TestSimulateSell_EquityWith9PercentChurch(t *testing.T) {
	// Same fixture as above, but with 9% church tax.
	// tax = 3500 × 0.28625 = 1001.875
	lots := []TaxLot{
		{ISIN: "EQ1", Name: "Equity ETF", Quantity: 100, CostBasis: 10000, CurrentValue: 15000, IsEquityFund: true},
	}
	requests := []SellRequest{{ISIN: "EQ1", AmountEUR: 15000}}
	results, _, _ := SimulateSell(lots, requests, 0.09)
	if math.Abs(results[0].EstimatedTax-1001.875) > 0.5 {
		t.Errorf("estimated_tax with 9%% church = %.2f, want 1001.875", results[0].EstimatedTax)
	}
}

func TestSimulateSell_NonEquityNoTeilfreistellung(t *testing.T) {
	// Single stock or bond (not an equity fund) — no Teilfreistellung.
	// gain 5000, tax = 5000 × 0.26375 = 1318.75.
	lots := []TaxLot{
		{ISIN: "STK1", Name: "Stock", Quantity: 100, CostBasis: 10000, CurrentValue: 15000, IsEquityFund: false},
	}
	requests := []SellRequest{{ISIN: "STK1", AmountEUR: 15000}}
	results, _, _ := SimulateSell(lots, requests, 0)
	if math.Abs(results[0].Teilfreistellung) > 0.01 {
		t.Errorf("teilfreistellung on non-equity = %.2f, want 0", results[0].Teilfreistellung)
	}
	if math.Abs(results[0].EstimatedTax-1318.75) > 0.5 {
		t.Errorf("non-equity tax = %.2f, want 1318.75", results[0].EstimatedTax)
	}
}

func TestSimulateSell_LossYieldsZeroTax(t *testing.T) {
	// Selling at a loss → no tax (loss could feed the Verlusttopf, not
	// simulated here, but tax must be zero on the simulator output).
	lots := []TaxLot{
		{ISIN: "EQ1", Name: "Equity ETF", Quantity: 100, CostBasis: 10000, CurrentValue: 7000, IsEquityFund: true},
	}
	requests := []SellRequest{{ISIN: "EQ1", AmountEUR: 7000}}
	results, totalTax, _ := SimulateSell(lots, requests, 0.09)
	if results[0].EstimatedTax != 0 {
		t.Errorf("loss → tax = %.2f, want 0", results[0].EstimatedTax)
	}
	if totalTax != 0 {
		t.Errorf("loss → totalTax = %.2f, want 0", totalTax)
	}
}
