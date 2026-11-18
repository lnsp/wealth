package analytics

import (
	"encoding/json"
	"fmt"
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

// ComputeLookThroughExposure returns each underlying holding's share (0-1)
// of the whole portfolio. Unlike ComputeEffectiveExposure, the denominator is
// totalValue, the value of every position (direct stocks, funds without
// constituent data, bonds, ...), not just the funds passed in. A position
// whose ETFHoldings is nil is a direct holding and counts 100 % towards its
// own ISIN; a fund contributes MarketValue/totalValue × constituent weight.
func ComputeLookThroughExposure(holdings []HoldingWithETF, totalValue float64) map[string]float64 {
	if totalValue <= 0 {
		return nil
	}
	exposure := make(map[string]float64)
	for _, h := range holdings {
		if h.MarketValue <= 0 {
			continue
		}
		share := h.MarketValue / totalValue
		if h.ETFHoldings == nil {
			exposure[h.ISIN] += share
			continue
		}
		for key, weight := range h.ETFHoldings {
			exposure[key] += share * weight / 100
		}
	}
	return exposure
}

// HoldingWithETF represents a portfolio holding with its ETF constituent weights.
type HoldingWithETF struct {
	ISIN        string
	Name        string
	MarketValue float64
	ETFHoldings map[string]float64 // constituent ISIN -> weight %
}

// Alert represents a concentration risk alert.
type Alert struct {
	Type    string  `json:"type"`  // "overlap" or "concentration"
	Level   string  `json:"level"` // "warning" or "critical"
	Message string  `json:"message"`
	Value   float64 `json:"value"` // the metric value (overlap %, exposure %)
}

const (
	OverlapWarningThreshold       = 70.0 // % overlap between two ETFs
	ConcentrationWarningThreshold = 5.0  // % single-stock exposure
)

// ComputeConcentrationAlerts checks for overlap redundancy and single-stock concentration.
func ComputeConcentrationAlerts(
	etfs []ETFWithHoldings,
	holdingsWithETF []HoldingWithETF,
	holdingNames map[string]string,
) []Alert {
	return ComputeConcentrationAlertsWithThresholds(
		etfs,
		holdingsWithETF,
		holdingNames,
		OverlapWarningThreshold,
		ConcentrationWarningThreshold,
	)
}

// ComputeConcentrationAlertsWithThresholds checks for overlap redundancy and single-stock concentration
// using configurable warning thresholds.
func ComputeConcentrationAlertsWithThresholds(
	etfs []ETFWithHoldings,
	holdingsWithETF []HoldingWithETF,
	holdingNames map[string]string,
	overlapWarnPct float64,
	concentrationWarnPct float64,
) []Alert {
	if overlapWarnPct <= 0 {
		overlapWarnPct = OverlapWarningThreshold
	}
	if concentrationWarnPct <= 0 {
		concentrationWarnPct = ConcentrationWarningThreshold
	}
	alerts := make([]Alert, 0)

	// Check pairwise ETF overlap
	for i := 0; i < len(etfs); i++ {
		for j := i + 1; j < len(etfs); j++ {
			overlap := ComputeOverlap(etfs[i].Holdings, etfs[j].Holdings)
			if overlap >= overlapWarnPct {
				level := "warning"
				if overlap >= math.Max(90, overlapWarnPct) {
					level = "critical"
				}
				alerts = append(alerts, Alert{
					Type:    "overlap",
					Level:   level,
					Message: etfs[i].Name + " and " + etfs[j].Name + " overlap by " + formatPct(overlap),
					Value:   overlap,
				})
			}
		}
	}

	// Check single-stock concentration
	exposure := ComputeEffectiveExposure(holdingsWithETF)
	for isin, pct := range exposure {
		exposurePct := pct * 100 // convert from 0-1 to 0-100
		if exposurePct >= concentrationWarnPct {
			level := "warning"
			if exposurePct >= math.Max(10, concentrationWarnPct*2) {
				level = "critical"
			}
			name := isin
			if n, ok := holdingNames[isin]; ok && n != "" {
				name = n
			}
			alerts = append(alerts, Alert{
				Type:    "concentration",
				Level:   level,
				Message: name + " represents " + formatPct(exposurePct) + " of portfolio",
				Value:   exposurePct,
			})
		}
	}

	return alerts
}

// HoldingValue represents a portfolio holding with its market value and sector weights.
type HoldingValue struct {
	ISIN          string
	MarketValue   float64
	SectorWeights map[string]float64 // sector name -> percentage (0-100)
}

// ComputeSectorAllocation calculates portfolio-level sector allocation percentages
// from individual holdings and their sector weights.
// Returns a map of sector name -> percentage (0-100).
func ComputeSectorAllocation(holdings []HoldingValue) map[string]float64 {
	totalValue := 0.0
	for _, h := range holdings {
		totalValue += h.MarketValue
	}
	if totalValue == 0 {
		return nil
	}

	exposure := make(map[string]float64)
	for _, h := range holdings {
		weight := h.MarketValue / totalValue
		for sector, pct := range h.SectorWeights {
			exposure[sector] += weight * pct
		}
	}
	return exposure
}

// RealizedPLResult holds the computed realized P&L from sell transactions.
type RealizedPLResult struct {
	TotalRealizedPL float64
	BySecurity      map[string]float64 // ISIN -> realized P&L
}

// TransactionForPL represents a simplified transaction for P&L computation.
type TransactionForPL struct {
	Type string  // "buy", "savings_plan", "sell", "transfer", "transfer_out"
	ISIN string
	Qty  float64
	Amt  float64
}

// CalculateRealizedPL computes realized gains using the average cost method.
// For each sell, realized P&L = sell_proceeds - (quantity_sold × avg_cost_per_unit).
func CalculateRealizedPL(txns []TransactionForPL) RealizedPLResult {
	type lot struct {
		quantity  float64
		totalCost float64
	}
	holdings := make(map[string]*lot)
	result := RealizedPLResult{BySecurity: make(map[string]float64)}

	for _, txn := range txns {
		if txn.ISIN == "" {
			continue
		}
		switch txn.Type {
		case "buy", "savings_plan":
			l, ok := holdings[txn.ISIN]
			if !ok {
				l = &lot{}
				holdings[txn.ISIN] = l
			}
			l.quantity += txn.Qty
			l.totalCost += txn.Amt
		case "transfer":
			// In-kind transfer in at the given cost basis
			l, ok := holdings[txn.ISIN]
			if !ok {
				l = &lot{}
				holdings[txn.ISIN] = l
			}
			l.quantity += txn.Qty
			l.totalCost += txn.Amt
		case "sell":
			l, ok := holdings[txn.ISIN]
			if !ok || l.quantity <= 0 {
				continue
			}
			sellQty := txn.Qty
			if sellQty > l.quantity {
				sellQty = l.quantity
			}
			avgCost := l.totalCost / l.quantity
			costBasis := sellQty * avgCost
			proceeds := txn.Amt
			if sellQty < txn.Qty && txn.Qty > 0 {
				proceeds = txn.Amt * (sellQty / txn.Qty)
			}
			realizedPL := proceeds - costBasis
			result.TotalRealizedPL += realizedPL
			result.BySecurity[txn.ISIN] += realizedPL
			l.quantity -= sellQty
			l.totalCost -= costBasis
			if l.quantity <= 1e-9 {
				delete(holdings, txn.ISIN)
			}
		case "transfer_out":
			l, ok := holdings[txn.ISIN]
			if !ok || l.quantity <= 0 {
				continue
			}
			outQty := txn.Qty
			if outQty > l.quantity {
				outQty = l.quantity
			}
			avgCost := l.totalCost / l.quantity
			l.quantity -= outQty
			l.totalCost -= outQty * avgCost
			if l.quantity <= 1e-9 {
				delete(holdings, txn.ISIN)
			}
		}
	}

	return result
}

func formatPct(v float64) string {
	s := math.Floor(v*10) / 10
	if s == math.Floor(s) {
		return fmt.Sprintf("%.0f%%", s)
	}
	return fmt.Sprintf("%.1f%%", s)
}
