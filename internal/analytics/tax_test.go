package analytics

import (
	"math"
	"testing"
)

func TestComputeTaxSummary_NoTransactions(t *testing.T) {
	result := ComputeTaxSummary(nil, 2025, nil)
	if result.RealizedGains != 0 || result.EstimatedTax != 0 {
		t.Error("expected zero tax for no transactions")
	}
	if result.FreistellungRemain != Sparerpauschbetrag {
		t.Errorf("expected full Freistellungsauftrag remaining, got %f", result.FreistellungRemain)
	}
}

func TestComputeTaxSummary_SimpleGain(t *testing.T) {
	txns := []TaxTransaction{
		{Year: 2025, Type: "buy", ISIN: "TEST", Quantity: 100, Amount: 10000},
		{Year: 2025, Type: "sell", ISIN: "TEST", Quantity: 100, Amount: 12000},
	}
	result := ComputeTaxSummary(txns, 2025, nil)

	if math.Abs(result.RealizedGains-2000) > 0.01 {
		t.Errorf("expected 2000 gains, got %f", result.RealizedGains)
	}
	if result.RealizedLosses != 0 {
		t.Errorf("expected 0 losses, got %f", result.RealizedLosses)
	}
	// 2000 gain - 1000 Freistellung = 1000 taxable × 26.375% = 263.75
	if math.Abs(result.EstimatedTax-263.75) > 0.1 {
		t.Errorf("expected ~263.75 tax, got %f", result.EstimatedTax)
	}
	if math.Abs(result.FreistellungUsed-1000) > 0.01 {
		t.Errorf("expected 1000 Freistellung used, got %f", result.FreistellungUsed)
	}
}

func TestComputeTaxSummary_GainBelowFreistellung(t *testing.T) {
	txns := []TaxTransaction{
		{Year: 2025, Type: "buy", ISIN: "TEST", Quantity: 100, Amount: 10000},
		{Year: 2025, Type: "sell", ISIN: "TEST", Quantity: 100, Amount: 10500},
	}
	result := ComputeTaxSummary(txns, 2025, nil)

	if math.Abs(result.RealizedGains-500) > 0.01 {
		t.Errorf("expected 500 gains, got %f", result.RealizedGains)
	}
	// 500 gain fully covered by 1000 Freistellung → 0 tax
	if result.EstimatedTax != 0 {
		t.Errorf("expected 0 tax (below Freistellung), got %f", result.EstimatedTax)
	}
	if math.Abs(result.FreistellungRemain-500) > 0.01 {
		t.Errorf("expected 500 Freistellung remaining, got %f", result.FreistellungRemain)
	}
}

func TestComputeTaxSummary_WithTeilfreistellung(t *testing.T) {
	txns := []TaxTransaction{
		{Year: 2025, Type: "buy", ISIN: "ETF1", Quantity: 100, Amount: 10000, IsEquityFund: true},
		{Year: 2025, Type: "sell", ISIN: "ETF1", Quantity: 100, Amount: 15000, IsEquityFund: true},
	}
	result := ComputeTaxSummary(txns, 2025, nil)

	// 5000 gain, 30% Teilfreistellung = 1500 exempt
	if math.Abs(result.TeilfreistellungAmt-1500) > 0.01 {
		t.Errorf("expected 1500 Teilfreistellung, got %f", result.TeilfreistellungAmt)
	}
	// Taxable: 5000 - 1500 = 3500 - 1000 Freistellung = 2500 × 26.375% = 659.38
	if math.Abs(result.EstimatedTax-659.38) > 1 {
		t.Errorf("expected ~659.38 tax, got %f", result.EstimatedTax)
	}
}

func TestComputeTaxSummary_LossOffsetsGain(t *testing.T) {
	txns := []TaxTransaction{
		{Year: 2025, Type: "buy", ISIN: "A", Quantity: 100, Amount: 10000},
		{Year: 2025, Type: "sell", ISIN: "A", Quantity: 100, Amount: 12000},
		{Year: 2025, Type: "buy", ISIN: "B", Quantity: 50, Amount: 5000},
		{Year: 2025, Type: "sell", ISIN: "B", Quantity: 50, Amount: 3500},
	}
	result := ComputeTaxSummary(txns, 2025, nil)

	// Gain 2000 + Loss -1500 = Net 500
	if math.Abs(result.NetGain-500) > 0.01 {
		t.Errorf("expected 500 net gain, got %f", result.NetGain)
	}
	// 500 below Freistellung → 0 tax
	if result.EstimatedTax != 0 {
		t.Errorf("expected 0 tax, got %f", result.EstimatedTax)
	}
}

func TestComputeTaxSummary_FiltersByYear(t *testing.T) {
	txns := []TaxTransaction{
		{Year: 2024, Type: "buy", ISIN: "A", Quantity: 100, Amount: 10000},
		{Year: 2024, Type: "sell", ISIN: "A", Quantity: 100, Amount: 15000},
		{Year: 2025, Type: "buy", ISIN: "B", Quantity: 50, Amount: 5000},
		{Year: 2025, Type: "sell", ISIN: "B", Quantity: 50, Amount: 5100},
	}

	result2024 := ComputeTaxSummary(txns, 2024, nil)
	if math.Abs(result2024.RealizedGains-5000) > 0.01 {
		t.Errorf("2024 gains should be 5000, got %f", result2024.RealizedGains)
	}

	result2025 := ComputeTaxSummary(txns, 2025, nil)
	if math.Abs(result2025.RealizedGains-100) > 0.01 {
		t.Errorf("2025 gains should be 100, got %f", result2025.RealizedGains)
	}
}

func TestComputeTaxSummary_CostBasisCarriesAcrossYears(t *testing.T) {
	txns := []TaxTransaction{
		{Year: 2023, Type: "buy", ISIN: "A", Quantity: 100, Amount: 10000},
		{Year: 2025, Type: "sell", ISIN: "A", Quantity: 100, Amount: 20000},
	}
	result := ComputeTaxSummary(txns, 2025, nil)

	// Cost basis from 2023 (100 EUR/share), sell in 2025 at 200 EUR/share
	if math.Abs(result.RealizedGains-10000) > 0.01 {
		t.Errorf("expected 10000 gains, got %f", result.RealizedGains)
	}
}

func TestComputeTaxSummary_DividendsIncluded(t *testing.T) {
	txns := []TaxTransaction{
		{Year: 2025, Type: "dividend", ISIN: "ETF1", Amount: 500, IsEquityFund: true},
	}
	result := ComputeTaxSummary(txns, 2025, nil)

	if math.Abs(result.DividendIncome-500) > 0.01 {
		t.Errorf("expected 500 dividend income, got %f", result.DividendIncome)
	}
}

func TestEffectiveTaxRate(t *testing.T) {
	expected := 0.26375
	if math.Abs(EffectiveTaxRate-expected) > 0.0001 {
		t.Errorf("EffectiveTaxRate = %f, want %f", EffectiveTaxRate, expected)
	}
}
