package analytics

import (
	"math"
	"sort"
	"testing"
)

func TestComputeCountryExposure(t *testing.T) {
	tests := []struct {
		name     string
		holdings []Holding
		wantNil  bool
		want     map[string]float64
	}{
		{
			name:    "nil holdings returns nil",
			wantNil: true,
		},
		{
			name: "zero total value returns nil",
			holdings: []Holding{
				{MarketValue: 0, CountryWeights: map[string]float64{"US": 100}},
			},
			wantNil: true,
		},
		{
			name: "single holding",
			holdings: []Holding{
				{MarketValue: 1000, CountryWeights: map[string]float64{"US": 60, "DE": 40}},
			},
			want: map[string]float64{"US": 0.6, "DE": 0.4},
		},
		{
			name: "two equal weight holdings",
			holdings: []Holding{
				{MarketValue: 1000, CountryWeights: map[string]float64{"US": 60, "DE": 40}},
				{MarketValue: 1000, CountryWeights: map[string]float64{"US": 20, "JP": 80}},
			},
			want: map[string]float64{
				"US": 0.4, // 0.5*60/100 + 0.5*20/100 = 0.3+0.1
				"DE": 0.2, // 0.5*40/100
				"JP": 0.4, // 0.5*80/100
			},
		},
		{
			name: "unequal weights",
			holdings: []Holding{
				{MarketValue: 3000, CountryWeights: map[string]float64{"US": 100}},
				{MarketValue: 1000, CountryWeights: map[string]float64{"DE": 100}},
			},
			want: map[string]float64{
				"US": 0.75, // 0.75*100/100
				"DE": 0.25, // 0.25*100/100
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeCountryExposure(tt.holdings)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			for k, v := range tt.want {
				if math.Abs(got[k]-v) > 0.001 {
					t.Errorf("exposure[%q] = %f, want %f", k, got[k], v)
				}
			}
		})
	}
}

func TestComputeSectorExposure_Table(t *testing.T) {
	tests := []struct {
		name     string
		holdings []Holding
		wantNil  bool
		want     map[string]float64
	}{
		{
			name:    "nil holdings returns nil",
			wantNil: true,
		},
		{
			name: "single holding all tech",
			holdings: []Holding{
				{MarketValue: 1000, SectorWeights: map[string]float64{"Tech": 100}},
			},
			want: map[string]float64{"Tech": 1.0},
		},
		{
			name: "three holdings",
			holdings: []Holding{
				{MarketValue: 500, SectorWeights: map[string]float64{"Tech": 80, "Health": 20}},
				{MarketValue: 300, SectorWeights: map[string]float64{"Finance": 100}},
				{MarketValue: 200, SectorWeights: map[string]float64{"Tech": 50, "Energy": 50}},
			},
			want: map[string]float64{
				"Tech":    0.50, // 0.5*80/100 + 0.2*50/100 = 0.4+0.1
				"Health":  0.10, // 0.5*20/100
				"Finance": 0.30, // 0.3*100/100
				"Energy":  0.10, // 0.2*50/100
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeSectorExposure(tt.holdings)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			for k, v := range tt.want {
				if math.Abs(got[k]-v) > 0.001 {
					t.Errorf("exposure[%q] = %f, want %f", k, got[k], v)
				}
			}
		})
	}
}

func TestBuildOverlapMatrix_Table(t *testing.T) {
	tests := []struct {
		name string
		etfs []ETFWithHoldings
		want [][]float64
	}{
		{
			name: "empty",
			etfs: nil,
			want: nil,
		},
		{
			name: "single ETF",
			etfs: []ETFWithHoldings{
				{ISIN: "A", Holdings: map[string]float64{"X": 50, "Y": 50}},
			},
			want: [][]float64{{100}},
		},
		{
			name: "two ETFs with partial overlap",
			etfs: []ETFWithHoldings{
				{ISIN: "A", Holdings: map[string]float64{"X": 50, "Y": 50}},
				{ISIN: "B", Holdings: map[string]float64{"X": 30, "Z": 70}},
			},
			want: [][]float64{
				{100, 30},
				{30, 100},
			},
		},
		{
			name: "three ETFs",
			etfs: []ETFWithHoldings{
				{ISIN: "A", Holdings: map[string]float64{"X": 40, "Y": 60}},
				{ISIN: "B", Holdings: map[string]float64{"Y": 30, "Z": 70}},
				{ISIN: "C", Holdings: map[string]float64{"X": 20, "Z": 80}},
			},
			want: [][]float64{
				{100, 30, 20}, // A-B: min(60,30)=30, A-C: min(40,20)=20
				{30, 100, 70}, // B-C: min(70,80)=70
				{20, 70, 100},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildOverlapMatrix(tt.etfs)
			if tt.want == nil {
				if len(got) != 0 {
					t.Errorf("expected empty matrix, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("matrix size = %d, want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				for j := range tt.want[i] {
					if got[i][j] != tt.want[i][j] {
						t.Errorf("matrix[%d][%d] = %f, want %f", i, j, got[i][j], tt.want[i][j])
					}
				}
			}
		})
	}
}

func TestBuildAllocationTreemap(t *testing.T) {
	tests := []struct {
		name      string
		etfs      []ETFForTreemap
		wantNodes int
	}{
		{
			name:      "empty input",
			etfs:      nil,
			wantNodes: 0,
		},
		{
			name: "ETF without sector data becomes leaf node",
			etfs: []ETFForTreemap{
				{Name: "VWCE", MarketValue: 60000},
			},
			wantNodes: 1,
		},
		{
			name: "ETF with zero value is skipped",
			etfs: []ETFForTreemap{
				{Name: "Empty", MarketValue: 0},
			},
			wantNodes: 0,
		},
		{
			name: "ETF with sector breakdown creates hierarchy",
			etfs: []ETFForTreemap{
				{
					Name:        "S&P 500",
					MarketValue: 40000,
					HoldingsBySector: map[string][]TreemapNode{
						"Technology": {
							{Name: "Apple", Value: 2800},
							{Name: "Microsoft", Value: 2600},
						},
						"Healthcare": {
							{Name: "UnitedHealth", Value: 1200},
						},
					},
				},
			},
			wantNodes: 1,
		},
		{
			name: "multiple ETFs create multiple top-level nodes",
			etfs: []ETFForTreemap{
				{
					Name:        "VWCE",
					MarketValue: 60000,
					HoldingsBySector: map[string][]TreemapNode{
						"Tech": {{Name: "Apple", Value: 2700}},
					},
				},
				{
					Name:        "SXR8",
					MarketValue: 40000,
					HoldingsBySector: map[string][]TreemapNode{
						"Tech": {{Name: "Apple", Value: 2800}},
					},
				},
			},
			wantNodes: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes := BuildAllocationTreemap(tt.etfs)
			if len(nodes) != tt.wantNodes {
				t.Errorf("got %d top-level nodes, want %d", len(nodes), tt.wantNodes)
			}
		})
	}
}

func TestBuildAllocationTreemap_Hierarchy(t *testing.T) {
	etfs := []ETFForTreemap{
		{
			Name:        "S&P 500 ETF",
			MarketValue: 40000,
			HoldingsBySector: map[string][]TreemapNode{
				"Technology": {
					{Name: "Apple", Value: 2800},
					{Name: "Microsoft", Value: 2600},
				},
				"Healthcare": {
					{Name: "UnitedHealth", Value: 1200},
				},
			},
		},
	}

	nodes := BuildAllocationTreemap(etfs)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 top-level node, got %d", len(nodes))
	}

	etfNode := nodes[0]
	if etfNode.Name != "S&P 500 ETF" {
		t.Errorf("top node name = %q, want %q", etfNode.Name, "S&P 500 ETF")
	}
	if etfNode.Value != 0 {
		t.Errorf("parent node should not have value set (children carry values)")
	}
	if len(etfNode.Children) != 2 {
		t.Fatalf("expected 2 sector children, got %d", len(etfNode.Children))
	}

	// Sort sectors for stable test
	sort.Slice(etfNode.Children, func(i, j int) bool {
		return etfNode.Children[i].Name < etfNode.Children[j].Name
	})

	healthSector := etfNode.Children[0]
	techSector := etfNode.Children[1]

	if healthSector.Name != "Healthcare" {
		t.Errorf("first sector = %q, want Healthcare", healthSector.Name)
	}
	if math.Abs(healthSector.Value-1200) > 0.01 {
		t.Errorf("Healthcare value = %f, want 1200", healthSector.Value)
	}
	if len(healthSector.Children) != 1 {
		t.Errorf("Healthcare should have 1 child, got %d", len(healthSector.Children))
	}

	if techSector.Name != "Technology" {
		t.Errorf("second sector = %q, want Technology", techSector.Name)
	}
	if math.Abs(techSector.Value-5400) > 0.01 {
		t.Errorf("Technology value = %f, want 5400", techSector.Value)
	}
	if len(techSector.Children) != 2 {
		t.Errorf("Technology should have 2 children, got %d", len(techSector.Children))
	}
}

func TestBuildAllocationTreemap_LeafNodeForNonETF(t *testing.T) {
	// Non-ETF positions (no HoldingsBySector) should be leaf nodes with Value
	etfs := []ETFForTreemap{
		{Name: "Apple Stock", MarketValue: 5000},
		{Name: "VWCE", MarketValue: 60000, HoldingsBySector: map[string][]TreemapNode{
			"Tech": {{Name: "Apple", Value: 2700}},
		}},
	}

	nodes := BuildAllocationTreemap(etfs)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}

	// Apple Stock should be a leaf (value set, no children)
	leaf := nodes[0]
	if leaf.Name != "Apple Stock" {
		t.Errorf("leaf name = %q, want Apple Stock", leaf.Name)
	}
	if leaf.Value != 5000 {
		t.Errorf("leaf value = %f, want 5000", leaf.Value)
	}
	if len(leaf.Children) != 0 {
		t.Errorf("leaf should have no children, got %d", len(leaf.Children))
	}

	// VWCE should have children
	parent := nodes[1]
	if len(parent.Children) != 1 {
		t.Errorf("VWCE should have 1 sector child, got %d", len(parent.Children))
	}
}
