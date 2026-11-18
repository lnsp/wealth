package analytics

import (
	"math"
	"testing"
	"time"
)

func cday(y, m, d int) time.Time { return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC) }

// A sub-period at −100 % (value vanished without a recorded outflow) used to
// be skipped silently; now the chain restarts after it and reports where.
func TestChainTWR_ResetAfterTotalLoss(t *testing.T) {
	steps := []TWRStep{
		{Date: cday(2026, 1, 1), Value: 1000},
		{Date: cday(2026, 1, 2), Value: 1100}, // +10 %
		{Date: cday(2026, 1, 3), Value: 0},    // gone, no outflow recorded
		{Date: cday(2026, 1, 4), Value: 500, Flow: 500},
		{Date: cday(2026, 1, 5), Value: 550}, // +10 %
	}
	c := ChainTWR(steps)
	if !c.OK || len(c.Resets) != 1 || !c.Resets[0].Equal(cday(2026, 1, 3)) {
		t.Fatalf("ok=%v resets=%v", c.OK, c.Resets)
	}
	if !c.Start.Equal(cday(2026, 1, 3)) || math.Abs(c.Return-0.10) > 1e-12 {
		t.Errorf("after reset: start %s return %.6f, want 2026-01-03 and 0.10", c.Start.Format("2006-01-02"), c.Return)
	}
	// The chart index carries over the anomaly instead of dropping to 0.
	if math.Abs(c.Index[4]-1.21) > 1e-12 {
		t.Errorf("index %.4f, want 1.21", c.Index[4])
	}
}

// No capital at any point: not "0 %" but not OK.
func TestChainTWR_NoCapitalIsNotOK(t *testing.T) {
	c := ChainTWR([]TWRStep{{Date: cday(2026, 1, 1)}, {Date: cday(2026, 1, 2)}})
	if c.OK || !math.IsNaN(c.Index[1]) {
		t.Errorf("ok=%v index=%v, want not OK and NaN", c.OK, c.Index)
	}
	if r := ComputeTWR(nil, nil); r.OK {
		t.Error("ComputeTWR(nil) must not be OK")
	}
}

func TestComputeTWR_ResetInsteadOfSkip(t *testing.T) {
	vals := []DailyValuation{
		{Date: cday(2026, 1, 1), Value: 1000},
		{Date: cday(2026, 1, 2), Value: 0}, // wiped out; only a 1 € flow recorded
		{Date: cday(2026, 1, 3), Value: 500},
		{Date: cday(2026, 1, 4), Value: 600},
	}
	flows := []CashFlow{{Date: cday(2026, 1, 2), Amount: 1}, {Date: cday(2026, 1, 3), Amount: 500}}
	r := ComputeTWR(vals, flows)
	if !r.OK || len(r.Resets) != 1 || math.Abs(r.Return-0.20) > 1e-9 {
		t.Errorf("%+v, want +20 %% after one reset", r)
	}
}

func TestComputeIRR_ValidityAndWideBracket(t *testing.T) {
	if _, ok := ComputeIRR([]CashFlow{{cday(2025, 1, 1), -100}, {cday(2026, 1, 1), -50}}, 0.05); ok {
		t.Error("all-negative flows have no IRR")
	}
	if _, ok := ComputeIRR([]CashFlow{{cday(2025, 1, 1), -100}}, 0.05); ok {
		t.Error("one flow has no IRR")
	}
	// +50 % in two months annualises to ≈ +1,000 %, far above the old +200 % cap.
	rate, ok := ComputeIRR([]CashFlow{{cday(2026, 1, 1), -1000}, {cday(2026, 3, 1), 1500}}, 0.05)
	years := cday(2026, 3, 1).Sub(cday(2026, 1, 1)).Hours() / (365.25 * 24)
	want := math.Pow(1.5, 1/years) - 1
	if !ok || math.Abs(rate-want)/want > 1e-6 {
		t.Errorf("rate %.4f ok=%v, want %.4f", rate, ok, want)
	}
}

// Modified Dietz on the seed's pattern (vest in, sell + wire out, vest in):
// the gain is divided by the average capital, not by net capital.
func TestModifiedDietz(t *testing.T) {
	start, end := cday(2026, 1, 1), cday(2027, 1, 1)
	flows := []CashFlow{{start, 1000}, {cday(2026, 7, 2), -1100}, {cday(2026, 7, 2), 1000}}
	md, ok := ModifiedDietz(0, 1100, flows, start, end)
	// gain = 1100 − 900 = 200; avg capital ≈ 1000 − 100·½ = 950
	if !ok || math.Abs(md-200.0/950.0) > 0.01 {
		t.Errorf("MD %.4f ok=%v, want ≈ 0.2105", md, ok)
	}
	if _, ok := ModifiedDietz(0, 0, nil, start, end); ok {
		t.Error("no capital: not OK")
	}
}
