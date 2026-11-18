package analytics

import (
	"math"
	"testing"
)

// German tax law (§20 Abs. 6 EStG) keeps two loss-offset pots:
//
//   Aktienverlusttopf — losses from selling shares (Aktien, asset_class
//                       "stock"). They may ONLY offset gains from selling
//                       shares, now or in later years.
//   Allgemeiner Verlusttopf — every other loss: funds/ETFs (after
//                       Teilfreistellung), bonds, derivatives. These offset
//                       any capital income, share gains included.
//
// Equity ETFs are investment funds, not shares: their losses belong in the
// general pot. Routing is by asset class (IsShareAssetClass), not by
// IsEquityFund. These tests pin that against ComputeLossPots.

func TestComputeLossPots_ShareLossDoesNotOffsetGeneralIncome(t *testing.T) {
	txns := []TaxTransaction{
		// Single stock bought and sold at a loss: share pot.
		{Year: 2024, Type: "buy", ISIN: "STK1", Quantity: 100, Amount: 10000, AssetClass: "stock"},
		{Year: 2024, Type: "sell", ISIN: "STK1", Quantity: 100, Amount: 5000, AssetClass: "stock"},
		// A dividend is general income.
		{Year: 2024, Type: "dividend", ISIN: "STK2", Amount: 2000, AssetClass: "stock"},
	}
	pots := ComputeLossPots(txns)
	if len(pots) != 1 {
		t.Fatalf("expected 1 year, got %d", len(pots))
	}
	p := pots[0]
	if math.Abs(p.EquityLosses-(-5000)) > 0.01 {
		t.Errorf("share losses = %.2f, want -5000 (no Teilfreistellung on stocks)", p.EquityLosses)
	}
	if math.Abs(p.GeneralGains-2000) > 0.01 || math.Abs(p.GeneralBalance-2000) > 0.01 {
		t.Errorf("general gains/balance = %.2f/%.2f, want 2000 (dividend untouched by the share loss)", p.GeneralGains, p.GeneralBalance)
	}
	if math.Abs(p.CarryForwardEquity-(-5000)) > 0.01 {
		t.Errorf("share carry-forward = %.2f, want -5000", p.CarryForwardEquity)
	}
	if math.Abs(p.TaxableIncome-2000) > 0.01 {
		t.Errorf("taxable income = %.2f, want 2000", p.TaxableIncome)
	}
}

func TestComputeLossPots_EquityETFLossIsGeneralAfterTeilfreistellung(t *testing.T) {
	// Regression (audit C1): an equity ETF is a fund, so its loss goes to the
	// general pot — reduced by the 30 % Teilfreistellung — and offsets a
	// dividend. It used to be routed to the share pot.
	txns := []TaxTransaction{
		{Year: 2024, Type: "buy", ISIN: "ETF1", Quantity: 100, Amount: 10000, AssetClass: "etf", IsEquityFund: true, TFS: 0.30, HasExplicitTFS: true},
		{Year: 2024, Type: "sell", ISIN: "ETF1", Quantity: 100, Amount: 8000, AssetClass: "etf", IsEquityFund: true, TFS: 0.30, HasExplicitTFS: true},
		{Year: 2024, Type: "dividend", ISIN: "STK", Amount: 800, AssetClass: "stock"},
		{Year: 2024, Type: "interest", Amount: 500},
	}
	pots := ComputeLossPots(txns)
	p := pots[0]
	if p.EquityLosses != 0 || p.EquityGains != 0 {
		t.Errorf("share pot = %.2f/%.2f, want empty (ETF is not a share)", p.EquityGains, p.EquityLosses)
	}
	if math.Abs(p.GeneralLosses-(-1400)) > 0.01 {
		t.Errorf("general losses = %.2f, want -1400 (-2000 × 0.70)", p.GeneralLosses)
	}
	if math.Abs(p.GeneralGains-1300) > 0.01 {
		t.Errorf("general gains = %.2f, want 1300 (dividend + interest)", p.GeneralGains)
	}
	if math.Abs(p.CarryForwardGeneral-(-100)) > 0.01 {
		t.Errorf("general carry-forward = %.2f, want -100", p.CarryForwardGeneral)
	}
}

func TestComputeLossPots_ShareLossOffsetsLaterShareGain(t *testing.T) {
	txns := []TaxTransaction{
		{Year: 2023, Type: "buy", ISIN: "STK1", Quantity: 100, Amount: 10000, AssetClass: "stock"},
		{Year: 2023, Type: "sell", ISIN: "STK1", Quantity: 100, Amount: 4000, AssetClass: "stock"},
		{Year: 2024, Type: "buy", ISIN: "STK2", Quantity: 50, Amount: 5000, AssetClass: "stock"},
		{Year: 2024, Type: "sell", ISIN: "STK2", Quantity: 50, Amount: 10000, AssetClass: "stock"},
	}
	pots := ComputeLossPots(txns)
	if len(pots) != 2 {
		t.Fatalf("expected 2 years, got %d", len(pots))
	}
	if math.Abs(pots[0].CarryForwardEquity-(-6000)) > 0.01 {
		t.Errorf("2023 share carry-forward = %.2f, want -6000", pots[0].CarryForwardEquity)
	}
	// 2024 gain 5000 is fully absorbed by the 6000 carried loss; 1000 remains.
	if math.Abs(pots[1].CarryForwardEquity-(-1000)) > 0.01 {
		t.Errorf("2024 share carry-forward = %.2f, want -1000", pots[1].CarryForwardEquity)
	}
	if pots[1].TaxableIncome != 0 {
		t.Errorf("2024 taxable = %.2f, want 0", pots[1].TaxableIncome)
	}
}

func TestComputeLossPots_GeneralLossOffsetsShareGain(t *testing.T) {
	// Regression (audit C1): general-pot losses may offset share gains
	// (§20 Abs. 6 S. 4 restricts only share losses). The old test pinned the
	// opposite.
	txns := []TaxTransaction{
		{Year: 2024, Type: "buy", ISIN: "BND1", Quantity: 100, Amount: 10000, AssetClass: "bond"},
		{Year: 2024, Type: "sell", ISIN: "BND1", Quantity: 100, Amount: 8000, AssetClass: "bond"},
		{Year: 2024, Type: "buy", ISIN: "STK1", Quantity: 50, Amount: 5000, AssetClass: "stock"},
		{Year: 2024, Type: "sell", ISIN: "STK1", Quantity: 50, Amount: 8000, AssetClass: "stock"},
	}
	p := ComputeLossPots(txns)[0]
	if math.Abs(p.EquityGains-3000) > 0.01 {
		t.Errorf("share gains = %.2f, want 3000", p.EquityGains)
	}
	if math.Abs(p.GeneralUsedAgainstShares-2000) > 0.01 {
		t.Errorf("general loss used against shares = %.2f, want 2000", p.GeneralUsedAgainstShares)
	}
	if p.CarryForwardGeneral != 0 || p.CarryForwardEquity != 0 {
		t.Errorf("carry-forward = %.2f/%.2f, want 0/0", p.CarryForwardEquity, p.CarryForwardGeneral)
	}
	if math.Abs(p.TaxableIncome-1000) > 0.01 {
		t.Errorf("taxable = %.2f, want 1000", p.TaxableIncome)
	}
}

func TestComputeLossPots_CarriedGeneralLossOffsetsLaterShareGain(t *testing.T) {
	txns := []TaxTransaction{
		{Year: 2023, Type: "buy", ISIN: "ETF1", Quantity: 10, Amount: 10000, AssetClass: "etf", TFS: 0.30, HasExplicitTFS: true},
		{Year: 2023, Type: "sell", ISIN: "ETF1", Quantity: 10, Amount: 5000, AssetClass: "etf", TFS: 0.30, HasExplicitTFS: true},
		{Year: 2024, Type: "buy", ISIN: "STK1", Quantity: 10, Amount: 1000, AssetClass: "stock"},
		{Year: 2024, Type: "sell", ISIN: "STK1", Quantity: 10, Amount: 5000, AssetClass: "stock"},
	}
	pots := ComputeLossPots(txns)
	if math.Abs(pots[0].CarryForwardGeneral-(-3500)) > 0.01 {
		t.Errorf("2023 general carry = %.2f, want -3500", pots[0].CarryForwardGeneral)
	}
	if pots[1].CarryForwardGeneral != 0 || math.Abs(pots[1].TaxableIncome-500) > 0.01 {
		t.Errorf("2024 carry/taxable = %.2f/%.2f, want 0/500 (4000 share gain − 3500 carried general loss)", pots[1].CarryForwardGeneral, pots[1].TaxableIncome)
	}
}

func TestComputeLossPots_EmptyAndUnknownTypesIgnored(t *testing.T) {
	pots := ComputeLossPots(nil)
	if len(pots) != 0 {
		t.Errorf("empty input → expected 0 years, got %d", len(pots))
	}
	// Non-tax types and purchases alone create no row (audit I3: no all-"None" years).
	pots = ComputeLossPots([]TaxTransaction{
		{Year: 2022, Type: "transfer", ISIN: "STK1", Quantity: 1, Amount: 100, AssetClass: "stock"},
		{Year: 2024, Type: "deposit", Amount: 5000},
		{Year: 2024, Type: "fee", Amount: 5},
	})
	if len(pots) != 0 {
		t.Errorf("no realised events → expected 0 years, got %+v", pots)
	}
}

func TestComputeLossPots_CryptoExcluded(t *testing.T) {
	// §23 assets never enter the §20 pots.
	pots := ComputeLossPots([]TaxTransaction{
		{Year: 2024, Type: "buy", ISIN: "CRYPTO:BTC", Quantity: 1, Amount: 50000, AssetClass: "crypto"},
		{Year: 2024, Type: "sell", ISIN: "CRYPTO:BTC", Quantity: 1, Amount: 20000, AssetClass: "crypto"},
	})
	if len(pots) != 0 {
		t.Errorf("crypto loss must not create a §20 loss pot row, got %+v", pots)
	}
}
