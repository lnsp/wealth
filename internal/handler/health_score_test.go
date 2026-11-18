package handler

import "testing"

// These tests drive computeHealthScore, the function HandleHealthScore uses.
// They used to check local copies of the formulas, one of which pinned the
// integer-division composite the 2026-11-18 audit flagged.

func TestHealthScore_WeightsSumTo100(t *testing.T) {
	_, subs := computeHealthScore(healthInputs{ValueByISIN: map[string]float64{"A": 1}})
	if len(subs) != 5 {
		t.Fatalf("%d subscores, want 5", len(subs))
	}
	sum := 0
	for _, s := range subs {
		sum += s.Weight
	}
	if sum != 100 {
		t.Fatalf("health-score weights sum to %d, want 100", sum)
	}
}

func TestHealthScore_CompositeIsRoundedWeightedAverage(t *testing.T) {
	// Five stocks at 20 % each (70 + 15 + 10 = 95), TER 0.15 % (80), no risk
	// data (50), targets matched exactly (95), 24 months of dividends (95):
	// (95·25 + 80·15 + 50·20 + 95·25 + 95·15) / 100 = 83.75 → 84 (integer
	// division used to truncate it to 83).
	in := healthInputs{
		ValueByISIN:    map[string]float64{"A": 1, "B": 1, "C": 1, "D": 1, "E": 1},
		AssetClass:     map[string]string{"A": "stock", "B": "stock", "C": "stock", "D": "stock", "E": "stock"},
		WeightedTER:    0.15,
		Targets:        map[string]float64{"A": 20, "B": 20, "C": 20, "D": 20, "E": 20},
		DividendMonths: 24,
	}
	score, subs := computeHealthScore(in)
	want := []int{95, 80, 50, 95, 95}
	for i, s := range subs {
		if s.Score != want[i] {
			t.Errorf("%s = %d, want %d", s.Name, s.Score, want[i])
		}
	}
	if score != 84 {
		t.Errorf("composite = %d, want 84", score)
	}
}

func TestHealthScore_StatusBandsAtBoundaries(t *testing.T) {
	cases := []struct {
		score int
		want  string
	}{
		{100, "good"},
		{80, "good"}, // ≥80
		{79, "fair"}, // below good floor
		{60, "fair"}, // ≥60
		{59, "poor"}, // below fair floor
		{0, "poor"},
	}
	for _, c := range cases {
		if got := healthStatus(c.score); got != c.want {
			t.Errorf("score=%d: status=%q, want %q", c.score, got, c.want)
		}
	}
}

func TestHealthScore_AllocationUsesSharedDrift(t *testing.T) {
	// A 10 % holding without a target counts as 10 % drift, exactly as the
	// allocation view and the rebalancer see it.
	in := healthInputs{
		ValueByISIN: map[string]float64{"A": 6000, "B": 3000, "C": 1000},
		Targets:     map[string]float64{"A": 60, "B": 40},
	}
	_, subs := computeHealthScore(in)
	if subs[3].Score != 60 || subs[3].Detail != "max drift 10.0%" {
		t.Errorf("allocation %+v, want 60 with max drift 10.0%%", subs[3])
	}
}
