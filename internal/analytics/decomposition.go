package analytics

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
	// Holdings grouped by sector: sector -> [{name, value}]
	HoldingsBySector map[string][]TreemapNode
}

// BuildAllocationTreemap constructs a hierarchical treemap: portfolio → ETF → sector → stock.
func BuildAllocationTreemap(etfs []ETFForTreemap) []TreemapNode {
	nodes := make([]TreemapNode, 0)

	for _, etf := range etfs {
		if etf.MarketValue <= 0 {
			continue
		}

		var sectorNodes []TreemapNode
		for sector, holdings := range etf.HoldingsBySector {
			sectorValue := 0.0
			for _, h := range holdings {
				sectorValue += h.Value
			}
			if sectorValue <= 0 {
				continue
			}
			sectorNodes = append(sectorNodes, TreemapNode{
				Name:     sector,
				Value:    sectorValue,
				Children: holdings,
			})
		}

		if len(sectorNodes) == 0 {
			// No sector breakdown — show ETF as leaf node
			nodes = append(nodes, TreemapNode{
				Name:  etf.Name,
				Value: etf.MarketValue,
			})
		} else {
			nodes = append(nodes, TreemapNode{
				Name:     etf.Name,
				Children: sectorNodes,
			})
		}
	}

	return nodes
}
