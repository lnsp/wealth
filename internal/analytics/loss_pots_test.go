package analytics

import (
	"math"
	"testing"
)

// German tax law (since 2009) keeps two separate loss-offset pots:
//
//   Aktienverlusttopf — equity-fund/stock losses, can ONLY offset equity gains.
//   Allgemeiner Verlusttopf — losses from non-equity instruments, offsets
//                              dividends + interest + non-equity gains.
//
// These tests pin the pot separation against ComputeLossPots so a refactor
// can't accidentally cross-offset (the most expensive bug class here:
// silently letting an equity loss reduce taxable dividend income would
// dodge real tax).

func TestComputeLossPots_EquityLossDoesNotOffsetGeneralGain(t *testing.T) {
	txns := []TaxTransaction{
		// Build position then sell at a loss: 100 sh @ 100 → 100 sh @ 50.
		{Year: 2024, Type: "buy", ISIN: "EQ1", Quantity: 100, Amount: 10000, IsEquityFund: true},
		{Year: 2024, Type: "sell", ISIN: "EQ1", Quantity: 100, Amount: 5000, IsEquityFund: true},
		// And a large dividend (general gain).
		{Year: 2024, Type: "dividend", ISIN: "EQ2", Quantity: 0, Amount: 2000, IsEquityFund: false},
	}
	pots := ComputeLossPots(txns)
	if len(pots) != 1 {
		t.Fatalf("expected 1 year, got %d", len(pots))
	}
	p := pots[0]
	// Equity loss: 5000 - 10000 = -5000, with TF 30% → -3500.
	if math.Abs(p.EquityLosses-(-3500)) > 1 {
		t.Errorf("equity losses = %.2f, want -3500 (after Teilfreistellung)", p.EquityLosses)
	}
	// General gain: 2000 dividend; the equity loss MUST NOT reduce it.
	if math.Abs(p.GeneralGains-2000) > 1 {
		t.Errorf("general gains = %.2f, want 2000 (dividend untouched by equity loss)", p.GeneralGains)
	}
	// General balance stays positive (2000 dividend, no general losses).
	if math.Abs(p.GeneralBalance-2000) > 1 {
		t.Errorf("general balance = %.2f, want 2000 (no general losses to offset)", p.GeneralBalance)
	}
	// Equity carry-forward: -3500 (no equity gains in 2024 to absorb it).
	if math.Abs(p.CarryForwardEquity-(-3500)) > 1 {
		t.Errorf("equity carry-forward = %.2f, want -3500", p.CarryForwardEquity)
	}
}

func TestComputeLossPots_GeneralLossOffsetsDividendAndInterest(t *testing.T) {
	txns := []TaxTransaction{
		// Non-equity (e.g. single stock or bond fund) — loss in general pot.
		{Year: 2024, Type: "buy", ISIN: "GEN1", Quantity: 100, Amount: 10000, IsEquityFund: false},
		{Year: 2024, Type: "sell", ISIN: "GEN1", Quantity: 100, Amount: 8000, IsEquityFund: false},
		// Dividend and interest go to general pot.
		{Year: 2024, Type: "dividend", ISIN: "STK", Quantity: 0, Amount: 800, IsEquityFund: false},
		{Year: 2024, Type: "interest", ISIN: "", Quantity: 0, Amount: 500, IsEquityFund: false},
	}
	pots := ComputeLossPots(txns)
	if len(pots) != 1 {
		t.Fatalf("expected 1 year, got %d", len(pots))
	}
	p := pots[0]
	// General loss: -2000 (no Teilfreistellung on non-equity).
	if math.Abs(p.GeneralLosses-(-2000)) > 1 {
		t.Errorf("general losses = %.2f, want -2000", p.GeneralLosses)
	}
	// General gains: 800 div + 500 interest = 1300.
	if math.Abs(p.GeneralGains-1300) > 1 {
		t.Errorf("general gains = %.2f, want 1300 (dividend + interest)", p.GeneralGains)
	}
	// Net balance: -2000 + 1300 = -700; should carry forward.
	if math.Abs(p.CarryForwardGeneral-(-700)) > 1 {
		t.Errorf("general carry-forward = %.2f, want -700", p.CarryForwardGeneral)
	}
}

func TestComputeLossPots_EquityLossOffsetsLaterEquityGain(t *testing.T) {
	// Year 1: equity loss. Year 2: equity gain that should consume the
	// carry-forward.
	txns := []TaxTransaction{
		{Year: 2023, Type: "buy", ISIN: "EQ1", Quantity: 100, Amount: 10000, IsEquityFund: true},
		{Year: 2023, Type: "sell", ISIN: "EQ1", Quantity: 100, Amount: 4000, IsEquityFund: true},
		// 2024: a winning equity trade.
		{Year: 2024, Type: "buy", ISIN: "EQ2", Quantity: 50, Amount: 5000, IsEquityFund: true},
		{Year: 2024, Type: "sell", ISIN: "EQ2", Quantity: 50, Amount: 10000, IsEquityFund: true},
	}
	pots := ComputeLossPots(txns)
	if len(pots) != 2 {
		t.Fatalf("expected 2 years, got %d", len(pots))
	}
	// 2023 loss: -6000 × 0.70 (TF) = -4200; carry-forward = -4200.
	if math.Abs(pots[0].CarryForwardEquity-(-4200)) > 1 {
		t.Errorf("2023 equity carry-forward = %.2f, want -4200", pots[0].CarryForwardEquity)
	}
	// 2024 gain: 5000 × 0.70 = 3500. Carry-forward 4200 covers it entirely;
	// remaining carry = 4200 - 3500 = 700 (still negative).
	if math.Abs(pots[1].CarryForwardEquity-(-700)) > 1 {
		t.Errorf("2024 equity carry-forward = %.2f, want -700 (3500 gain offset by 4200 carry, 700 remaining)", pots[1].CarryForwardEquity)
	}
}

func TestComputeLossPots_GeneralLossCannotOffsetEquityGain(t *testing.T) {
	// Inverse of the first test: a general loss must NOT reduce an equity
	// gain. The pots are one-way (Aktien only Aktien).
	txns := []TaxTransaction{
		// General loss.
		{Year: 2024, Type: "buy", ISIN: "GEN1", Quantity: 100, Amount: 10000, IsEquityFund: false},
		{Year: 2024, Type: "sell", ISIN: "GEN1", Quantity: 100, Amount: 8000, IsEquityFund: false},
		// Equity gain.
		{Year: 2024, Type: "buy", ISIN: "EQ1", Quantity: 50, Amount: 5000, IsEquityFund: true},
		{Year: 2024, Type: "sell", ISIN: "EQ1", Quantity: 50, Amount: 10000, IsEquityFund: true},
	}
	pots := ComputeLossPots(txns)
	if len(pots) != 1 {
		t.Fatalf("expected 1 year, got %d", len(pots))
	}
	p := pots[0]
	// Equity gain stays at 3500 (5000 × 0.70 after Teilfreistellung). The
	// -2000 general loss MUST NOT touch it.
	if math.Abs(p.EquityGains-3500) > 1 {
		t.Errorf("equity gains = %.2f, want 3500 (untouched by general loss)", p.EquityGains)
	}
	// General loss should be -2000 (carry-forward), unable to offset the
	// equity gain.
	if math.Abs(p.CarryForwardGeneral-(-2000)) > 1 {
		t.Errorf("general carry-forward = %.2f, want -2000 (equity gain doesn't absorb it)", p.CarryForwardGeneral)
	}
}

func TestComputeLossPots_EmptyAndUnknownTypesIgnored(t *testing.T) {
	pots := ComputeLossPots(nil)
	if len(pots) != 0 {
		t.Errorf("empty input → expected 0 years, got %d", len(pots))
	}
	// Random non-tax type shouldn't crash or be counted.
	pots = ComputeLossPots([]TaxTransaction{
		{Year: 2024, Type: "deposit", Amount: 5000},
		{Year: 2024, Type: "fee", Amount: 5},
	})
	// 2024 enters the year set via either txn (years map registers it),
	// but no pots ledger entries — all zero.
	if len(pots) == 1 {
		p := pots[0]
		if p.EquityLosses != 0 || p.EquityGains != 0 || p.GeneralLosses != 0 || p.GeneralGains != 0 {
			t.Errorf("non-tax types should not affect pots, got %+v", p)
		}
	}
}
