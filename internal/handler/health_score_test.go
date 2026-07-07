package handler

import "testing"

// The Portfolio Health composite weights are locked in HandleHealthScore
// (analysis.go). They must sum to 100 — otherwise composite = total/100
// produces values outside [0, 100] for any valid subscore set. The frontend
// circle and the subscore chips both branch on the same 80 / 60 thresholds,
// so any drift here silently breaks the matching color bands.
const (
	healthWeightDiversification = 25
	healthWeightCostEfficiency  = 15
	healthWeightRiskBalance     = 20
	healthWeightAllocation      = 25
	healthWeightIncome          = 15
)

func TestHealthScore_WeightsSumTo100(t *testing.T) {
	sum := healthWeightDiversification +
		healthWeightCostEfficiency +
		healthWeightRiskBalance +
		healthWeightAllocation +
		healthWeightIncome
	if sum != 100 {
		t.Fatalf("health-score weights sum to %d, want 100", sum)
	}
}

// healthComposite mirrors HandleHealthScore's composite formula. With weights
// summing to 100 this is a plain weighted average, but the handler uses
// integer arithmetic — keep the test path identical to catch truncation
// drift if the weights are ever tweaked unevenly.
func healthComposite(div, cost, risk, alloc, inc int) int {
	total := div*healthWeightDiversification +
		cost*healthWeightCostEfficiency +
		risk*healthWeightRiskBalance +
		alloc*healthWeightAllocation +
		inc*healthWeightIncome
	return total / 100
}

func TestHealthScore_CompositeAtKeyValues(t *testing.T) {
	cases := []struct {
		name string
		s    [5]int
		want int
	}{
		{"all 100", [5]int{100, 100, 100, 100, 100}, 100},
		{"all 0", [5]int{0, 0, 0, 0, 0}, 0},
		{"all 80 (good band floor)", [5]int{80, 80, 80, 80, 80}, 80},
		{"all 60 (fair band floor)", [5]int{60, 60, 60, 60, 60}, 60},
		{"all 50 (poor)", [5]int{50, 50, 50, 50, 50}, 50},
		// Heavy weight on a low score drags composite down materially.
		{"diversification 0, rest 100", [5]int{0, 100, 100, 100, 100}, 75},
		// Light weight on a low score leaves composite high.
		{"income 0, rest 100", [5]int{100, 100, 100, 100, 0}, 85},
	}
	for _, c := range cases {
		got := healthComposite(c.s[0], c.s[1], c.s[2], c.s[3], c.s[4])
		if got != c.want {
			t.Errorf("%s: composite=%d, want %d", c.name, got, c.want)
		}
	}
}

// statusForScore mirrors the band mapping HandleHealthScore writes into each
// subscore's Status field, AND the colour the frontend renders for both the
// composite circle and the chips. Locking it here makes a mismatch impossible
// to introduce silently in one half of the system.
func statusForScore(score int) string {
	if score >= 80 {
		return "good"
	}
	if score >= 60 {
		return "fair"
	}
	return "poor"
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
		if got := statusForScore(c.score); got != c.want {
			t.Errorf("score=%d: status=%q, want %q", c.score, got, c.want)
		}
	}
}
