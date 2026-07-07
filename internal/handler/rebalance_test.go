package handler

import (
	"math"
	"testing"
)

// maxDrift mirrors the metric the Target Allocation widget surfaces:
// the largest |actual_pct - target_pct| across all targeted positions.
// Reducing it is the headline claim of "Compute Trades".
func maxDrift(holdings map[string]rebalanceHolding, targets map[string]float64, totalValue float64) float64 {
	if totalValue <= 0 {
		return 0
	}
	worst := 0.0
	for isin, target := range targets {
		actual := 0.0
		if h, ok := holdings[isin]; ok {
			actual = h.Value / totalValue * 100
		}
		drift := math.Abs(actual - target)
		if drift > worst {
			worst = drift
		}
	}
	return worst
}

// applyTrades simulates the post-trade portfolio so the test can verify
// drift moved in the right direction. Cash is implicit — selling adds it,
// buying consumes it.
func applyTrades(holdings map[string]rebalanceHolding, trades []rebalanceTrade) (map[string]rebalanceHolding, float64) {
	out := make(map[string]rebalanceHolding, len(holdings))
	for k, v := range holdings {
		out[k] = v
	}
	total := 0.0
	for _, h := range out {
		total += h.Value
	}
	for _, t := range trades {
		h, ok := out[t.ISIN]
		if !ok {
			h = rebalanceHolding{ISIN: t.ISIN, Name: t.Name, Price: 0}
		}
		if t.Action == "buy" {
			h.Value += t.Amount
			total += t.Amount
		} else {
			h.Value -= t.Amount
			total -= t.Amount
		}
		out[t.ISIN] = h
	}
	return out, total
}

func TestComputeRebalance_FullRebalanceReducesMaxDrift(t *testing.T) {
	// Unbalanced fixture — IWDA over-weight, IBGM under-weight, target 60/40.
	holdings := map[string]rebalanceHolding{
		"IE00B4L5Y983": {ISIN: "IE00B4L5Y983", Name: "iShares MSCI World", Value: 80_000, Price: 100},
		"IE00B3F81R35": {ISIN: "IE00B3F81R35", Name: "iShares Aggregate Bond", Value: 20_000, Price: 50},
	}
	targets := map[string]float64{
		"IE00B4L5Y983": 60,
		"IE00B3F81R35": 40,
	}
	totalValue := 100_000.0
	before := maxDrift(holdings, targets, totalValue)
	if before < 15 {
		t.Fatalf("setup: expected materially unbalanced fixture, got before-drift %.2f%%", before)
	}

	trades := computeRebalanceTrades(holdings, targets, totalValue, 0)
	if len(trades) == 0 {
		t.Fatal("expected trades to rebalance a strongly drifted portfolio")
	}

	after, postTotal := applyTrades(holdings, trades)
	afterDrift := maxDrift(after, targets, postTotal)
	if afterDrift >= before {
		t.Errorf("max drift went from %.2f%% to %.2f%% — trades didn't reduce drift", before, afterDrift)
	}
}

func TestComputeRebalance_FullRebalanceEmitsBothSides(t *testing.T) {
	// Same fixture as above — confirms the algorithm emits BOTH a buy and a
	// sell side when both deficits and surpluses exist (otherwise rebalancing
	// would be a one-shot deposit and would converge slowly).
	holdings := map[string]rebalanceHolding{
		"IE00B4L5Y983": {ISIN: "IE00B4L5Y983", Name: "World", Value: 80_000, Price: 100},
		"IE00B3F81R35": {ISIN: "IE00B3F81R35", Name: "Bonds", Value: 20_000, Price: 50},
	}
	targets := map[string]float64{"IE00B4L5Y983": 60, "IE00B3F81R35": 40}
	trades := computeRebalanceTrades(holdings, targets, 100_000, 0)

	var sawBuy, sawSell bool
	for _, t := range trades {
		if t.Action == "buy" {
			sawBuy = true
		}
		if t.Action == "sell" {
			sawSell = true
		}
	}
	if !sawBuy || !sawSell {
		t.Errorf("expected both buy and sell trades, got buys=%v sells=%v (trades=%+v)", sawBuy, sawSell, trades)
	}
}

func TestComputeRebalance_DepositNeverSells(t *testing.T) {
	// Even when one position is severely overweight, allocating fresh cash
	// must never produce a sell — the user is depositing, not rebalancing.
	holdings := map[string]rebalanceHolding{
		"IE00B4L5Y983": {ISIN: "IE00B4L5Y983", Name: "World", Value: 95_000, Price: 100},
		"IE00B3F81R35": {ISIN: "IE00B3F81R35", Name: "Bonds", Value: 5_000, Price: 50},
	}
	targets := map[string]float64{"IE00B4L5Y983": 60, "IE00B3F81R35": 40}
	trades := computeRebalanceTrades(holdings, targets, 100_000, 10_000)

	for _, tr := range trades {
		if tr.Action != "buy" {
			t.Errorf("deposit mode produced non-buy trade: %+v", tr)
		}
	}
	// Sanity: with a deficit on the bond side, we must see at least one trade.
	if len(trades) == 0 {
		t.Error("deposit mode produced zero trades despite a heavily-underweight position")
	}
}

func TestComputeRebalance_DepositAllocatesProportionalToDeficit(t *testing.T) {
	// Two underweight positions with different gaps. The 10k deposit must
	// flow disproportionately to the larger gap.
	holdings := map[string]rebalanceHolding{
		"AAA": {ISIN: "AAA", Name: "A", Value: 50_000, Price: 100},
		"BBB": {ISIN: "BBB", Name: "B", Value: 5_000, Price: 100},
		"CCC": {ISIN: "CCC", Name: "C", Value: 45_000, Price: 100},
	}
	targets := map[string]float64{"AAA": 33, "BBB": 33, "CCC": 34}
	trades := computeRebalanceTrades(holdings, targets, 100_000, 10_000)

	allocBy := make(map[string]float64)
	for _, t := range trades {
		allocBy[t.ISIN] = t.Amount
	}
	// BBB has the bigger gap (~33% of 110k = 36.3k vs 5k = 31.3k deficit) so
	// it must receive a larger allocation than AAA (which is just barely
	// under target).
	if allocBy["BBB"] <= allocBy["AAA"] {
		t.Errorf("deposit went to AAA (%.2f) >= BBB (%.2f), expected BBB > AAA (bigger gap)", allocBy["AAA"], allocBy["BBB"])
	}
}

func TestComputeRebalance_BalancedPortfolioReturnsNoTrades(t *testing.T) {
	holdings := map[string]rebalanceHolding{
		"AAA": {ISIN: "AAA", Name: "A", Value: 60_000, Price: 100},
		"BBB": {ISIN: "BBB", Name: "B", Value: 40_000, Price: 100},
	}
	targets := map[string]float64{"AAA": 60, "BBB": 40}
	trades := computeRebalanceTrades(holdings, targets, 100_000, 0)
	if len(trades) > 0 {
		t.Errorf("balanced portfolio yielded %d trades, want 0", len(trades))
	}
}
