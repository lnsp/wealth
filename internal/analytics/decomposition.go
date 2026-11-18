package analytics

import "sort"

// Holding represents a portfolio position with its market value and metadata.
type Holding struct {
	ISIN           string
	MarketValue    float64
	SectorWeights  map[string]float64
	CountryWeights map[string]float64
}

// ComputeSectorExposure calculates weighted sector allocation across all holdings.
func ComputeSectorExposure(holdings []Holding) map[string]float64 {
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
			exposure[sector] += weight * pct / 100
		}
	}
	return exposure
}

// ComputeCountryExposure calculates weighted country allocation across all holdings.
func ComputeCountryExposure(holdings []Holding) map[string]float64 {
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
		for country, pct := range h.CountryWeights {
			exposure[country] += weight * pct / 100
		}
	}
	return exposure
}

// BuildOverlapMatrix computes pairwise overlap for a set of ETFs.
func BuildOverlapMatrix(etfs []ETFWithHoldings) [][]float64 {
	n := len(etfs)
	matrix := make([][]float64, n)
	for i := range matrix {
		matrix[i] = make([]float64, n)
		matrix[i][i] = 100.0
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			o := ComputeOverlap(etfs[i].Holdings, etfs[j].Holdings)
			matrix[i][j] = o
			matrix[j][i] = o
		}
	}
	return matrix
}

// ETFWithHoldings represents an ETF with its constituent holdings' weights.
type ETFWithHoldings struct {
	ISIN     string
	Name     string
	Holdings map[string]float64 // constituent ISIN -> weight %
}

// TreemapNode represents a node in the allocation treemap hierarchy.
type TreemapNode struct {
	Name     string        `json:"name"`
	Value    float64       `json:"value,omitempty"`
	Children []TreemapNode `json:"children,omitempty"`
}

// ETFForTreemap holds the data needed to build a treemap for one ETF position.
type ETFForTreemap struct {
	Name        string
	MarketValue float64
	// Holdings grouped by sector: sector -> [{name, value}]. Used only when
	// every listed constituent has a sector on record.
	HoldingsBySector map[string][]TreemapNode
	// Constituents is the flat list of listed constituents (EUR values),
	// used when no sector breakdown is available.
	Constituents []TreemapNode
}

// TreemapRestOfFund labels the part of a fund not covered by its listed
// constituents (justETF lists only the top 10).
const TreemapRestOfFund = "Rest of fund"

// BuildAllocationTreemap constructs a hierarchical treemap: portfolio → ETF →
// (sector →) stock. Every ETF node carries its full market value, and the
// value not covered by the listed constituents becomes a "Rest of fund"
// child, so a fund is drawn at its real size rather than at the sum of its
// top-10 constituents.
func BuildAllocationTreemap(etfs []ETFForTreemap) []TreemapNode {
	nodes := make([]TreemapNode, 0)

	for _, etf := range etfs {
		if etf.MarketValue <= 0 {
			continue
		}

		var children []TreemapNode
		listed := 0.0
		if len(etf.HoldingsBySector) > 0 {
			sectors := make([]string, 0, len(etf.HoldingsBySector))
			for sector := range etf.HoldingsBySector {
				sectors = append(sectors, sector)
			}
			sort.Strings(sectors)
			for _, sector := range sectors {
				holdings := etf.HoldingsBySector[sector]
				sectorValue := 0.0
				for _, h := range holdings {
					sectorValue += h.Value
				}
				if sectorValue <= 0 {
					continue
				}
				listed += sectorValue
				children = append(children, TreemapNode{
					Name:     sector,
					Value:    sectorValue,
					Children: holdings,
				})
			}
		} else {
			for _, c := range etf.Constituents {
				if c.Value <= 0 {
					continue
				}
				listed += c.Value
				children = append(children, c)
			}
		}

		if len(children) == 0 {
			// No constituent breakdown: show the position as a leaf node.
			nodes = append(nodes, TreemapNode{
				Name:  etf.Name,
				Value: etf.MarketValue,
			})
			continue
		}
		if rest := etf.MarketValue - listed; rest > etf.MarketValue*0.001 {
			children = append(children, TreemapNode{Name: TreemapRestOfFund, Value: rest})
		}
		nodes = append(nodes, TreemapNode{
			Name:     etf.Name,
			Value:    etf.MarketValue,
			Children: children,
		})
	}

	return nodes
}
