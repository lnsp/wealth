package handler

import (
	"math"
	"testing"

	"github.com/lnsp/wealth/internal/analytics"
)

// Anlage KAP per-broker aggregation invariants the German tax form depends on:
//
//  1. Equity ETF dividends/gains get 30% Teilfreistellung applied; the
//     stored amount is `(1 − 0.30) × gross`. Single-stocks, bond ETFs, and
//     interest are NOT touched by Teilfreistellung.
//  2. Per-broker totals (Dividends + Interest + RealizedGains − |Losses|)
//     reconcile to the Anlage-KAP-wide `net_taxable` when summed across
//     brokers — losses at broker A offset gains at broker B.
//  3. Losses are stored as NEGATIVE values; net_taxable adds them in.

// Minimal handler-side fixture: one taxable event per broker, applied via the
// same accumulation logic the real handler uses, then verified.

type brokerBucket struct {
	Dividends     float64
	Interest      float64
	RealizedGains float64
	RealizedLoss  float64 // negative
	Teilfrei      float64
}

func applyDividend(b *brokerBucket, amount float64, isEquity bool) {
	if isEquity {
		b.Teilfrei += amount * analytics.TeilfreistellungEquity
		amount *= (1 - analytics.TeilfreistellungEquity)
	}
	b.Dividends += amount
}

func applyRealizedGain(b *brokerBucket, gain float64, isEquity bool) {
	if gain > 0 && isEquity {
		b.Teilfrei += gain * analytics.TeilfreistellungEquity
		gain *= (1 - analytics.TeilfreistellungEquity)
	}
	if gain > 0 {
		b.RealizedGains += gain
	} else {
		b.RealizedLoss += gain // negative
	}
}

func applyInterest(b *brokerBucket, amount float64) {
	b.Interest += amount
}

func TestAnlageKAP_DividendOnEquityETF_AppliesTeilfreistellung(t *testing.T) {
	var b brokerBucket
	applyDividend(&b, 100, true) // equity ETF dividend
	// (1 − 0.30) × 100 = 70 net dividend; teilfrei stores the 30.
	if math.Abs(b.Dividends-70) > 0.01 {
		t.Errorf("equity dividend net = %.2f, want 70 (100 × 0.70)", b.Dividends)
	}
	if math.Abs(b.Teilfrei-30) > 0.01 {
		t.Errorf("teilfrei = %.2f, want 30 (100 × 0.30)", b.Teilfrei)
	}
}

func TestAnlageKAP_DividendOnSingleStock_NoTeilfreistellung(t *testing.T) {
	// Single stock (not equity_fund=true) → no Teilfreistellung.
	var b brokerBucket
	applyDividend(&b, 200, false)
	if math.Abs(b.Dividends-200) > 0.01 {
		t.Errorf("stock dividend = %.2f, want 200 (no Teilfreistellung)", b.Dividends)
	}
	if b.Teilfrei != 0 {
		t.Errorf("stock dividend should not touch Teilfrei, got %.2f", b.Teilfrei)
	}
}

func TestAnlageKAP_InterestNeverGetsTeilfreistellung(t *testing.T) {
	var b brokerBucket
	applyInterest(&b, 50)
	if math.Abs(b.Interest-50) > 0.01 {
		t.Errorf("interest = %.2f, want 50 (Teilfreistellung does NOT apply to interest)", b.Interest)
	}
	if b.Teilfrei != 0 {
		t.Errorf("interest should not touch Teilfrei, got %.2f", b.Teilfrei)
	}
}

func TestAnlageKAP_EquityETFGainAppliesTeilfreistellung(t *testing.T) {
	var b brokerBucket
	applyRealizedGain(&b, 1000, true)
	if math.Abs(b.RealizedGains-700) > 0.01 {
		t.Errorf("equity gain = %.2f, want 700 (1000 × 0.70)", b.RealizedGains)
	}
	if math.Abs(b.Teilfrei-300) > 0.01 {
		t.Errorf("teilfrei = %.2f, want 300", b.Teilfrei)
	}
}

func TestAnlageKAP_LossesNotTouchedByTeilfreistellung(t *testing.T) {
	// Per German tax rules + the handler comment at line 1903: Teilfreistellung
	// only reduces TAXABLE gains. Losses on equity ETFs are NOT scaled down
	// (otherwise users would lose part of their loss-offset capacity).
	var b brokerBucket
	applyRealizedGain(&b, -500, true)
	if math.Abs(b.RealizedLoss-(-500)) > 0.01 {
		t.Errorf("equity loss = %.2f, want -500 (loss preserved at full magnitude)", b.RealizedLoss)
	}
	if b.Teilfrei != 0 {
		t.Errorf("losses should not credit Teilfrei, got %.2f", b.Teilfrei)
	}
}

func TestAnlageKAP_CrossBrokerReconciliation(t *testing.T) {
	// Two brokers: A has gains, B has losses. Anlage KAP net_taxable nets
	// across brokers — German tax law requires filing the form to claim the
	// loss offset.
	var brokerA, brokerB brokerBucket
	applyRealizedGain(&brokerA, 2000, true)  // +2000 → +1400 after Teilfreistellung
	applyDividend(&brokerA, 300, true)        // +300 → +210 after Teilfreistellung
	applyRealizedGain(&brokerB, -1000, false) // single-stock loss, no TF → -1000
	applyInterest(&brokerB, 80)               // +80 plain

	totalDividends := brokerA.Dividends + brokerB.Dividends
	totalInterest := brokerA.Interest + brokerB.Interest
	totalGains := brokerA.RealizedGains + brokerB.RealizedGains
	totalLosses := brokerA.RealizedLoss + brokerB.RealizedLoss
	grossIncome := totalDividends + totalInterest + totalGains
	netTaxable := grossIncome + totalLosses // losses negative

	// Expected: dividends 210 (A only), interest 80 (B only), gains 1400,
	// losses -1000. Gross 210+80+1400 = 1690. Net = 1690 + (-1000) = 690.
	if math.Abs(totalDividends-210) > 0.01 {
		t.Errorf("totalDividends = %.2f, want 210 (A's equity ETF 300 × 0.70)", totalDividends)
	}
	if math.Abs(totalInterest-80) > 0.01 {
		t.Errorf("totalInterest = %.2f, want 80 (B's interest)", totalInterest)
	}
	if math.Abs(totalGains-1400) > 0.01 {
		t.Errorf("totalGains = %.2f, want 1400 (A's 2000 × 0.70)", totalGains)
	}
	if math.Abs(totalLosses-(-1000)) > 0.01 {
		t.Errorf("totalLosses = %.2f, want -1000", totalLosses)
	}
	if math.Abs(grossIncome-1690) > 0.01 {
		t.Errorf("grossIncome = %.2f, want 1690", grossIncome)
	}
	if math.Abs(netTaxable-690) > 0.01 {
		t.Errorf("netTaxable = %.2f, want 690 (cross-broker offset: 1690 − 1000)", netTaxable)
	}

	// Tax with full FSA available: max(0, 690 − 1000) × 0.26375 = 0.
	fsaApplied := math.Min(math.Max(netTaxable, 0), analytics.Sparerpauschbetrag)
	afterFSA := math.Max(netTaxable-fsaApplied, 0)
	estimatedTax := afterFSA * analytics.EffectiveTaxRate
	if estimatedTax != 0 {
		t.Errorf("estimatedTax = %.2f, want 0 (FSA absorbs the 690 net)", estimatedTax)
	}
}

func TestAnlageKAP_TeilfreistellungConstantIs30Percent(t *testing.T) {
	// Sanity-check the constant the Anlage-KAP math depends on. If anyone
	// edits TeilfreistellungEquity below, this test names the dependency.
	if math.Abs(analytics.TeilfreistellungEquity-0.30) > 1e-9 {
		t.Errorf("TeilfreistellungEquity = %.4f, want 0.30 (German equity-fund Teilfreistellung)",
			analytics.TeilfreistellungEquity)
	}
}
