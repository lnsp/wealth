package analytics

import (
	"math"
	"testing"
)

// Audit W6: top holdings divided by the ETF value only and left out direct
// stocks, so Alphabet showed ~1.4 % while GOOG was 70 % of the portfolio.
func TestComputeLookThroughExposure_WholePortfolioDenominator(t *testing.T) {
	holdings := []HoldingWithETF{
		{ISIN: "US02079K1079", Name: "Alphabet C", MarketValue: 7000}, // direct, ETFHoldings nil
		{ISIN: "IE00BK5BQT80", MarketValue: 2000, ETFHoldings: map[string]float64{
			"US02079K1079": 4, // same company inside the fund
			"US0378331005": 5,
		}},
		// A fund without constituent data still counts in the denominator.
	}
	total := 10000.0 // 7,000 + 2,000 + 1,000 in a fund without constituents

	got := ComputeLookThroughExposure(holdings, total)
	if a := got["US02079K1079"]; math.Abs(a-0.708) > 1e-12 {
		t.Errorf("Alphabet = %.4f, want 0.7080 ((7,000 + 2,000 × 4 %%) / 10,000)", a)
	}
	if a := got["US0378331005"]; math.Abs(a-0.01) > 1e-12 {
		t.Errorf("Apple = %.4f, want 0.0100", a)
	}
	if len(got) != 2 {
		t.Errorf("got %d rows, want 2", len(got))
	}

	// The old function divides by the fund value only and ignores direct
	// holdings: Alphabet 4 %.
	if old := ComputeEffectiveExposure(holdings)["US02079K1079"]; math.Abs(old-0.04*2000/9000) > 1e-12 {
		t.Fatalf("setup: ComputeEffectiveExposure = %.4f", old)
	}
	if ComputeLookThroughExposure(holdings, 0) != nil {
		t.Error("zero portfolio value must return nil")
	}
}
