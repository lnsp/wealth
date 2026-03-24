package analytics

import (
	"encoding/json"
	"math"
)

// ParseWeights parses JSONB sector/country weights into a map.
func ParseWeights(raw json.RawMessage) map[string]float64 {
	if len(raw) == 0 {
		return nil
	}
	var weights map[string]float64
	if err := json.Unmarshal(raw, &weights); err != nil {
		return nil
	}
	return weights
}

// ComputeOverlap calculates the pairwise overlap between two sets of holdings.
// Overlap = sum of min(weight_a_i, weight_b_i) for each holding present in both.
func ComputeOverlap(a, b map[string]float64) float64 {
	overlap := 0.0
	for isin, weightA := range a {
		if weightB, ok := b[isin]; ok {
			overlap += math.Min(weightA, weightB)
		}
	}
	return overlap
}

// ComputeEffectiveExposure calculates per-holding exposure across all portfolio positions.
func ComputeEffectiveExposure(holdings []HoldingWithETF) map[string]float64 {
	totalValue := 0.0
	for _, h := range holdings {
		totalValue += h.MarketValue
	}
	if totalValue == 0 {
		return nil
	}

	exposure := make(map[string]float64)
	for _, h := range holdings {
		portfolioWeight := h.MarketValue / totalValue
		for isin, holdingWeight := range h.ETFHoldings {
			exposure[isin] += portfolioWeight * holdingWeight / 100
		}
	}
	return exposure
}

// HoldingWithETF represents a portfolio holding with its ETF constituent weights.
type HoldingWithETF struct {
	ISIN        string
	MarketValue float64
	ETFHoldings map[string]float64 // constituent ISIN -> weight %
}
